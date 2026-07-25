package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// maxSubscriptionDuration caps a subscription's total lifetime — measured
// from its original creation (subscriptions.created_at, never touched by
// any status/period transition) to its extended period_end — at 2 years.
// Extensions that would push period_end past this are rejected outright
// rather than silently clamped, so the owner always knows exactly how much
// runway an extension purchase bought.
const maxSubscriptionDuration = 2 * 365 * 24 * time.Hour

// maxExtendableMonths answers "how many more months, if any, can this
// subscription be extended by" — the single place that computes this, so
// extendSubscription's reject-if-exceeds check and the frontend-facing
// max_extendable_months field on the subscription GET response can never
// disagree. A bounded loop over n = 0..24 (not a closed-form calculation):
// AddDate's calendar-month semantics (28/30/31-day months) aren't cleanly
// invertible into a formula, and the bound is tiny enough that a loop is
// simpler and provably correct.
func maxExtendableMonths(createdAt, periodEnd, _ time.Time) int {
	maxAllowedEnd := createdAt.Add(maxSubscriptionDuration)
	for n := 24; n >= 0; n-- {
		if !periodEnd.AddDate(0, n, 0).After(maxAllowedEnd) {
			return n
		}
	}
	return 0
}

// computeExtensionSubtotal implements the tiered extension-pricing rule:
// every full 12-month block bills at the plan's yearly price, and any
// remainder bills at the monthly price (e.g. 13 months = 1 yearly block +
// 1 month). months < 12 collapses to blocks == 0, i.e. today's flat
// monthly × months calculation — the same formula handles both cases, no
// separate branch needed.
func computeExtensionSubtotal(planInfo *contracts.PlanInfo, currency string, months int) int64 {
	blocks := months / 12
	remainder := months % 12
	return int64(blocks)*planInfo.Price(currency, cycleYearly) + int64(remainder)*planInfo.Price(currency, cycleMonthly)
}

// insertExtensionLineItems writes the invoice line item(s) for an extension
// purchase, split so every line's unit_price_cents × quantity == its own
// total_cents — the invoice PDF (pdf.LineItem, handler_invoice.go) shows
// quantity/unit-price/amount as separate columns without recomputing them,
// so a mismatch would look like a math error on a document the customer
// reads. Every line's quantity is expressed in months, never blocks,
// specifically so handleWebhook can recover the total extension length by
// summing every one of the invoice's line items' quantities, instead of
// assuming a single line item at a fixed index — see the matching comment
// there.
func (s *service) insertExtensionLineItems(
	ctx context.Context, invoiceID string, planInfo *contracts.PlanInfo, currency string, months int,
) error {
	blocks, remainder := months/12, months%12
	sortOrder := 0

	if blocks > 0 {
		yearlyPrice := planInfo.Price(currency, cycleYearly)
		desc := fmt.Sprintf("%s plan — 12-month block", planInfo.Name)
		if blocks > 1 {
			desc = fmt.Sprintf("%s plan — %d × 12-month blocks", planInfo.Name, blocks)
		}
		// unit_price_cents is a monthly-equivalent display rate (yearlyPrice/12,
		// floor-rounded) — total_cents stays the exact blocks*yearlyPrice
		// regardless, so the actual charge is never affected by this rounding,
		// only the displayed per-month figure (the same "billed annually at $X,
		// ~$Y/mo" rounding any annual-pricing display already carries).
		if err := s.repo.insertLineItem(
			ctx, s.querier(ctx), invoiceID, desc, currency,
			blocks*12, yearlyPrice/12, int64(blocks)*yearlyPrice, sortOrder,
		); err != nil {
			return err
		}
		sortOrder++
	}

	if remainder > 0 {
		monthlyPrice := planInfo.Price(currency, cycleMonthly)
		desc := fmt.Sprintf("%s plan — %d month extension", planInfo.Name, remainder)
		if err := s.repo.insertLineItem(
			ctx, s.querier(ctx), invoiceID, desc, currency,
			remainder, monthlyPrice, int64(remainder)*monthlyPrice, sortOrder,
		); err != nil {
			return err
		}
	}

	return nil
}

// extendSubscription buys `months` of additional runway on an active
// subscription's current period (a dedicated invoice, independent of the
// regular renewal cycle — coupons/addon pricing deliberately don't apply
// here, since their cadence/attachment model is built around regular cycle
// invoices, not ad hoc extension purchases), priced by computeExtensionSubtotal.
// Capped so the subscription's total lifetime (from its original creation,
// never extendable past 2 years) is never exceeded — rejected outright
// rather than silently clamped, so the caller always knows exactly how much
// time an extension bought.
//
// switchToAnnual additionally converts the subscription's billing cycle to
// yearly — but not synchronously here: this function only ever creates the
// invoice (see the transaction below); the period/cycle mutation itself
// happens later, asynchronously, in handleWebhook when the invoice is
// actually paid (service_webhook.go). switchToAnnual is persisted onto the
// invoice (invoices.switch_to_annual) precisely so that later, payment-time
// code can read back what was requested here.
func (s *service) extendSubscription(
	ctx context.Context, subjectType, subjectID string, months int, switchToAnnual bool, _ string,
) (inv *invoiceRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		inv = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrSubscriptionNotExtendable):
			err = apperr.Validation("SUBSCRIPTION_NOT_EXTENDABLE", "subscription cannot be extended in its current state")
		case errors.Is(err, ErrExtensionAlreadyPending):
			err = apperr.Validation("EXTENSION_ALREADY_PENDING", "an extension invoice is already pending payment")
		case errors.Is(err, ErrExtensionExceedsMaxDuration):
			err = apperr.Validation("EXTENSION_EXCEEDS_MAX_DURATION", err.Error())
		case errors.Is(err, ErrAlreadyYearly):
			err = apperr.Validation("SUBSCRIPTION_ALREADY_YEARLY", "subscription is already on the yearly cycle")
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		default:
			err = apperr.Internal("SUBSCRIPTION_EXTEND_FAILED", "failed to extend subscription", err)
		}
	}()

	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	if sub.Status != statusActive {
		return nil, ErrSubscriptionNotExtendable
	}
	if sub.PeriodStart == nil || sub.PeriodEnd == nil {
		return nil, ErrSubscriptionNotExtendable
	}

	if switchToAnnual {
		if sub.Cycle != cycleMonthly {
			return nil, ErrAlreadyYearly
		}
		months = 12
	}

	if isPending, _ := s.repo.hasPendingInvoiceOfKind(ctx, s.querier(ctx), sub.ID, "extension"); isPending {
		return nil, ErrExtensionAlreadyPending
	}

	newPeriodEnd := sub.PeriodEnd.AddDate(0, months, 0)
	if months > maxExtendableMonths(sub.CreatedAt, *sub.PeriodEnd, time.Now()) {
		maxAllowedEnd := sub.CreatedAt.Add(maxSubscriptionDuration)
		return nil, fmt.Errorf("billing.extendSubscription: %w: at most until %s", ErrExtensionExceedsMaxDuration, maxAllowedEnd.Format(time.RFC3339))
	}

	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, ErrUnknownPlan
	}
	subtotal := computeExtensionSubtotal(planInfo, sub.Currency, months)

	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(subtotal, taxRate)

	// DB-local writes (invoice + line item + period + history) are wrapped in
	// one transaction so a mid-sequence failure can't leave a partially
	// extended subscription. The payment link (blocking Stripe/Xendit HTTP
	// call) and event publish stay outside — same "persist in tx, integrate
	// after commit" shape as provisionSubscription. This route deliberately
	// doesn't run under the group-level RLS tx (billing/module.go) so that
	// HTTP call never holds a pooled DB connection open.
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, subtotal, taxRate, tax, sub.Currency, "extension", switchToAnnual)
		if err != nil {
			return err
		}
		if err := s.insertExtensionLineItems(ctx, inv.ID, planInfo, sub.Currency, months); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Payment link is a convenience, not load-bearing — a missing/unconfigured
	// provider shouldn't block the extension itself; the invoice is already
	// created and can be paid via the regular POST .../invoices/:id/pay flow,
	// same fail-open convention as provisionSubscription.
	_, _ = s.createPaymentLink(ctx, "", "", inv.ID)

	events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", subjectID,
		events.InvoiceCreated{
			OrgID: subjectID, InvoiceID: inv.ID,
			Plan: sub.Plan, AmountCents: inv.AmountCents, Currency: sub.Currency, DueAt: *inv.DueAt,
		})

	s.scheduleRenewalSequence(ctx, &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &newPeriodEnd,
	})

	return inv, nil
}

// activateTrialNow lets a trialing organization skip the rest of its trial
// and start a paid period immediately: invoices the current plan (full
// cycle price, addons/coupons included via composeInvoiceAmount, same as
// any other invoice-creation site) and converts the subscription to active
// with a fresh period starting now. Previously the only way to reach a
// paid, invoiced state was waiting for the automatic 3-day-before-trial-end
// auto-invoice or trial expiry — there was no way to opt in early.
func (s *service) activateTrialNow(
	ctx context.Context, subjectType, subjectID, activatedBy string,
) (inv *invoiceRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		inv = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrSubscriptionNotTrialing):
			err = apperr.Validation("SUBSCRIPTION_NOT_TRIALING", "subscription is not currently trialing")
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		default:
			err = apperr.Internal("SUBSCRIPTION_ACTIVATE_FAILED", "failed to activate subscription", err)
		}
	}()

	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	if sub.Status != statusTrialing {
		return nil, ErrSubscriptionNotTrialing
	}

	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, ErrUnknownPlan
	}

	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0)
	if sub.Cycle == cycleYearly {
		periodEnd = now.AddDate(1, 0, 0)
	}

	composed, addonLines, couponCode, discountCents := s.composeInvoiceAmount(ctx,
		s.querier(ctx), sub.ID, planInfo, sub.Currency, sub.Cycle)
	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(composed, taxRate)

	// DB-local writes wrapped in one transaction — see extendSubscription for why.
	var updated *subscriptionRecord
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, composed, taxRate, tax, sub.Currency, "subscription", false)
		if err != nil {
			return err
		}
		_ = s.insertPlanLineItem(ctx, inv, planInfo)
		s.applyInvoiceCharges(ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents)

		updated, err = s.repo.activateTrialImmediately(ctx, s.querier(ctx), sub.ID, now, periodEnd)
		if err != nil {
			return err
		}
		_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "activate", &sub.Plan, &sub.Plan, composed, sub.Currency, activatedBy, nil)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Payment link is a convenience, not load-bearing — see extendSubscription.
	_, _ = s.createPaymentLink(ctx, "", "", inv.ID)

	events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeySubscriptionActivated, "billing", subjectID,
		events.SubscriptionActivated{
			OrgID: subjectID, SubscriptionID: updated.ID, Plan: updated.Plan, ActivatedAt: now,
		})
	events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", subjectID,
		events.InvoiceCreated{
			OrgID: subjectID, InvoiceID: inv.ID,
			Plan: sub.Plan, AmountCents: inv.AmountCents, Currency: sub.Currency, DueAt: *inv.DueAt,
		})

	s.scheduleRenewalSequence(ctx, &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
	})

	return inv, nil
}

func (s *service) resumeSubscription(
	ctx context.Context, subjectType, subjectID, resumedBy string,
) (sub *subscriptionRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		sub = nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found", err)
		case errors.Is(err, ErrSubscriptionNotResumable):
			err = apperr.Validation("SUBSCRIPTION_NOT_RESUMABLE", "subscription cannot be resumed in its current state")
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		default:
			err = apperr.Internal("SUBSCRIPTION_RESUME_FAILED", "failed to resume subscription", err)
		}
	}()

	sub, err = s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectType, subjectID)
	if err != nil {
		return nil, err
	}

	switch sub.Status {
	case statusCancelled:
		now := time.Now()

		// A subscription cancelled while still within its original trial
		// window resumes back into "trialing", not "active" with a
		// freshly-started billing period — the trial hasn't actually
		// expired, and marking it "active" would imply a paying customer
		// even though no invoice was ever created. cancelSubscription only
		// ever touches status, never trial_end, so this is a reliable check.
		isStillTrialing := sub.TrialEnd != nil && sub.TrialEnd.After(now)

		newStatus := statusActive
		periodEnd := now.AddDate(0, 1, 0)
		if sub.Cycle == cycleYearly {
			periodEnd = now.AddDate(1, 0, 0)
		}
		if isStillTrialing {
			newStatus = statusTrialing
			periodEnd = *sub.TrialEnd
		}

		var updated *subscriptionRecord
		err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
			ctx := db.WithQuerier(ctx, tx)
			var err error
			updated, err = s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, newStatus)
			if err != nil {
				return err
			}
			_ = s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd)
			_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
				&sub.Plan, &sub.Plan, 0, sub.Currency, changedBySystem, nil)
			return nil
		})
		if err != nil {
			return nil, err
		}

		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeySubscriptionResumed, "billing", subjectID,
			events.SubscriptionResumed{OrgID: subjectID, SubscriptionID: sub.ID, Plan: sub.Plan, ResumedAt: now})

		renewalSub := &subscriptionRecord{
			ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
		}
		if isStillTrialing {
			renewalSub.TrialEnd = sub.TrialEnd
		}
		s.scheduleRenewalSequence(ctx, renewalSub)

		return updated, nil

	case statusExpired:
		planInfo, err := s.planCatalog(ctx, sub.Plan)
		if err != nil {
			return nil, fmt.Errorf("billing.HandleSubscriptionRemind: %w: current plan", ErrUnknownPlan)
		}
		composed, addonLines, couponCode, discountCents := s.composeInvoiceAmount(
			ctx, s.querier(ctx), sub.ID, planInfo, sub.Currency, sub.Cycle)
		taxRate := 0
		if s.taxReader != nil {
			taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
		}
		tax := calculateTax(composed, taxRate)

		var inv *invoiceRecord
		err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
			ctx := db.WithQuerier(ctx, tx)
			var err error
			inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, composed, taxRate, tax, sub.Currency, "subscription", false)
			if err != nil {
				return err
			}
			_ = s.insertPlanLineItem(ctx, inv, planInfo)
			s.applyInvoiceCharges(ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents)
			_, _ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume", &sub.Plan, &sub.Plan, composed, sub.Currency, resumedBy, nil)
			return nil
		})
		if err != nil {
			return nil, err
		}

		// Payment link is a convenience, not load-bearing — see extendSubscription.
		_, _ = s.createPaymentLink(ctx, "", "", inv.ID)

		return sub, nil

	default:
		return nil, ErrSubscriptionNotResumable
	}
}

// scheduleRenewalSequence publishes delayed messages for the renewal flow:
// remind (7 days before a normal period end, 2 days before a trial end —
// trials run exactly 7 days, so a 7-day lead would resolve to ~now and
// never actually fire), 3 days before → auto-invoice, at end → expire check.
func (s *service) scheduleRenewalSequence(ctx context.Context, sub *subscriptionRecord) {
	var endTime time.Time
	isTrial := sub.TrialEnd != nil
	switch {
	case isTrial:
		endTime = *sub.TrialEnd
	case sub.PeriodEnd != nil:
		endTime = *sub.PeriodEnd
	default:
		return
	}

	checkPayload := events.SubscriptionCheck{
		SubscriptionID: sub.ID,
		SubjectType:    sub.SubjectType,
		SubjectID:      sub.SubjectID,
		ExpectedEnd:    endTime,
	}

	remindLeadDays := 7
	if isTrial {
		remindLeadDays = 2
	}
	if d := time.Until(endTime.AddDate(0, 0, -remindLeadDays)); d > 0 {
		events.PublishDelayed(ctx, s.pub, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionRemind, "billing", sub.SubjectID, checkPayload, d)
	}

	if d := time.Until(endTime.AddDate(0, 0, -3)); d > 0 {
		events.PublishDelayed(ctx, s.pub, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionAutoInvoice, "billing", sub.SubjectID, checkPayload, d)
	}

	d := time.Until(endTime)
	if d <= 0 {
		d = time.Second
	}
	events.PublishDelayed(ctx, s.pub, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionCheck, "billing", sub.SubjectID, checkPayload, d)
}
