package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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

// extendSubscription buys `months` of additional runway on an active
// subscription's current period, priced at the plan's monthly rate ×
// months (a dedicated invoice, independent of the regular renewal cycle —
// coupons/addon pricing deliberately don't apply here, since their
// cadence/attachment model is built around regular cycle invoices, not ad
// hoc extension purchases). Capped so the subscription's total lifetime
// (from its original creation, never extendable past 2 years) is never
// exceeded — rejected outright rather than silently clamped, so the caller
// always knows exactly how much time an extension bought.
func (s *service) extendSubscription(
	ctx context.Context, subjectType, subjectID string, months int, _ string,
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

	if isPending, _ := s.repo.hasPendingInvoiceOfKind(ctx, s.querier(ctx), sub.ID, "extension"); isPending {
		return nil, ErrExtensionAlreadyPending
	}

	newPeriodEnd := sub.PeriodEnd.AddDate(0, months, 0)
	maxAllowedEnd := sub.CreatedAt.Add(maxSubscriptionDuration)
	if newPeriodEnd.After(maxAllowedEnd) {
		return nil, fmt.Errorf("billing.extendSubscription: %w: at most until %s", ErrExtensionExceedsMaxDuration, maxAllowedEnd.Format(time.RFC3339))
	}

	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, ErrUnknownPlan
	}
	monthlyPrice := planInfo.Price(sub.Currency, cycleMonthly)
	subtotal := monthlyPrice * int64(months)

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
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, subtotal, taxRate, tax, sub.Currency, "extension")
		if err != nil {
			return err
		}
		desc := fmt.Sprintf("%s plan — %d month extension", planInfo.Name, months)
		if err := s.repo.insertLineItem(
			ctx, s.querier(ctx), inv.ID, desc, sub.Currency,
			months, monthlyPrice, subtotal, 0,
		); err != nil {
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
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, composed, taxRate, tax, sub.Currency, "subscription")
		if err != nil {
			return err
		}
		_ = s.insertPlanLineItem(ctx, inv, planInfo)
		s.applyInvoiceCharges(ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents)

		updated, err = s.repo.activateTrialImmediately(ctx, s.querier(ctx), sub.ID, now, periodEnd)
		if err != nil {
			return err
		}
		_ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "activate", &sub.Plan, &sub.Plan, composed, sub.Currency, activatedBy, nil)
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
			_ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
				&sub.Plan, &sub.Plan, 0, sub.Currency, resumedBy, nil)
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
			inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID, sub.ID, composed, taxRate, tax, sub.Currency, "subscription")
			if err != nil {
				return err
			}
			_ = s.insertPlanLineItem(ctx, inv, planInfo)
			s.applyInvoiceCharges(ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents)
			_ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume", &sub.Plan, &sub.Plan, composed, sub.Currency, resumedBy, nil)
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
