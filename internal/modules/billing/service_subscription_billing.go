package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// maxRunwayMonths caps prepaid future runway, not subscription lifetime: at
// any point in time, period_end may never sit more than this many calendar
// months ahead of now. A subscription may be renewed indefinitely — each
// extension just isn't allowed to push period_end past the 24-month horizon
// measured from the moment of that extension. Extensions that would exceed
// it are rejected outright rather than silently clamped, so the owner always
// knows exactly how much runway an extension purchase bought.
const maxRunwayMonths = 24

// maxExtendableMonths answers "how many more months, if any, can this
// subscription be extended by right now" — the single place that computes
// this, so extendSubscription's reject-if-exceeds check and the
// frontend-facing max_extendable_months field on the subscription GET
// response can never disagree. A bounded loop over n = 0..maxRunwayMonths
// (not a closed-form calculation): AddDate's calendar-month semantics
// (28/30/31-day months) aren't cleanly invertible into a formula, and the
// bound is tiny enough that a loop is simpler and provably correct.
//
// Both sides of the comparison go through AddDate (calendar arithmetic), not
// a fixed-duration Add: comparing a fixed 24*30-ish-day duration against
// calendar-month increments drifts by a day whenever a leap day falls inside
// the window, which previously undercounted the allowance by a full month
// for anyone whose 24-month horizon crossed a Feb 29. Using AddDate on both
// sides keeps them exactly in step regardless of leap years.
//
// Compared at day granularity, not exact timestamps: period_end's
// time-of-day drifts away from now's whenever a plan change reprorates the
// period (changePlanWithMetadata anchors the new period to time.Now() of the
// change). A few hours of drift shouldn't cost the owner a whole month of
// extendable runway — only full elapsed days should.
func maxExtendableMonths(periodEnd, now time.Time) int {
	maxAllowedEnd := truncateToDay(now.AddDate(0, maxRunwayMonths, 0))
	for n := maxRunwayMonths; n >= 0; n-- {
		if !truncateToDay(periodEnd.AddDate(0, n, 0)).After(maxAllowedEnd) {
			return n
		}
	}
	return 0
}

func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// periodMonths returns the whole number of calendar months periodStart to
// periodEnd most closely spans — nearest, not floor. Ordinary calendar
// variance (28/30/31-day months) means a period landing near, but not
// exactly on, a whole-month boundary is still that month: a subscription's
// single-cycle period is frequently a few days off an exact AddDate
// boundary (e.g. a 30-day test period vs. AddDate's 31-day August month, or
// any period reshaped by prorate()'s day-based math) and flooring such a
// near-miss down would drop an entire pricing tier — for a period just
// under one month, all the way to zero. A period produced by
// extendSubscription (AddDate(0, months, 0)) always lands on an exact
// whole-month boundary, so it always wins its own exact match regardless.
// Mirrors maxExtendableMonths' AddDate-loop technique for calendar-exact
// month counting instead of a fixed-day-length division — AddDate's
// calendar semantics aren't cleanly invertible into a formula, and the
// bound is tiny enough that scanning every candidate is simpler and
// provably correct.
func periodMonths(periodStart, periodEnd time.Time) int {
	end := truncateToDay(periodEnd)
	best := 0
	bestDiff := truncateToDay(periodStart).Sub(end).Abs()
	for n := 1; n <= maxRunwayMonths; n++ {
		diff := truncateToDay(periodStart.AddDate(0, n, 0)).Sub(end).Abs()
		if diff < bestDiff {
			best, bestDiff = n, diff
		}
	}
	return best
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

// computeExtensionAddonSubtotal applies computeExtensionSubtotal's same
// tiered rule to every currently attached addon, so an extension purchase
// can no longer buy addon runway for free: previously extendSubscription
// priced the plan only, and since addons ride the subscription's
// period_end with no expiry of their own, the regular renewal invoice
// (the only thing that normally bills them) got rescheduled past the
// bought window entirely. Uses each addon's live Quantity — never
// ScheduledQuantity/PendingQuantity, which describe changes that aren't
// in effect yet.
func computeExtensionAddonSubtotal(addons []attachedAddonRecord, currency string, months int) int64 {
	blocks := int64(months / 12)
	remainder := int64(months % 12)
	var total int64
	for _, a := range addons {
		prices, ok := a.Prices[currency]
		if !ok {
			continue
		}
		total += (blocks*int64(prices.Yearly) + remainder*int64(prices.Monthly)) * int64(a.Quantity)
	}
	return total
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
// there. addons is only the plan's own block/remainder lines' sibling data;
// each attached addon gets the same block/remainder line treatment appended
// after the plan's lines, priced via computeExtensionAddonSubtotal's same
// rule, using each addon's live Quantity only.
func (s *service) insertExtensionLineItems(
	ctx context.Context, invoiceID string,
	planInfo *contracts.PlanInfo, addons []attachedAddonRecord, currency string, months int,
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
			return fmt.Errorf("billing.insertExtensionLineItems: %w", err)
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
			return fmt.Errorf("billing.insertExtensionLineItems: %w", err)
		}
		sortOrder++
	}

	// Every addon line's Quantity is expressed in months too, matching the
	// plan lines above — never in addon units. applyExtensionPayment
	// (service_webhook.go) recovers the total extension length by summing
	// every one of the invoice's line items' Quantity fields; an addon line
	// quantified by addon-units × months instead of months alone would
	// inflate that sum and roll the period forward further than what was
	// actually paid for. The addon's own attached quantity is folded into
	// total_cents (and named in the description) instead.
	for _, a := range addons {
		prices, ok := a.Prices[currency]
		if !ok || a.Quantity <= 0 {
			continue
		}
		if blocks > 0 {
			qty := blocks * 12
			desc := fmt.Sprintf("%s (×%d) — 12-month block", a.Name, a.Quantity)
			if blocks > 1 {
				desc = fmt.Sprintf("%s (×%d) — %d × 12-month blocks", a.Name, a.Quantity, blocks)
			}
			total := int64(blocks) * int64(prices.Yearly) * int64(a.Quantity)
			if err := s.repo.insertLineItem(
				ctx, s.querier(ctx), invoiceID, desc, currency,
				qty, total/int64(qty), total, sortOrder,
			); err != nil {
				return fmt.Errorf("billing.insertExtensionLineItems: %w", err)
			}
			sortOrder++
		}
		if remainder > 0 {
			desc := fmt.Sprintf("%s (×%d) — %d month extension", a.Name, a.Quantity, remainder)
			total := int64(remainder) * int64(prices.Monthly) * int64(a.Quantity)
			if err := s.repo.insertLineItem(
				ctx, s.querier(ctx), invoiceID, desc, currency,
				remainder, total/int64(remainder), total, sortOrder,
			); err != nil {
				return fmt.Errorf("billing.insertExtensionLineItems: %w", err)
			}
			sortOrder++
		}
	}

	return nil
}

// extendSubscription buys `months` of additional runway on an active
// subscription's current period (a dedicated invoice, independent of the
// regular renewal cycle), priced by computeExtensionSubtotal for the plan
// plus computeExtensionAddonSubtotal for every currently attached addon —
// addons ride the subscription's period_end with no expiry of their own, so
// without this the regular renewal invoice (the only thing that normally
// bills them) would get rescheduled past the bought window and the addon
// would run free for its length. Coupons still don't apply here: unlike
// addons, skipping a coupon only reduces what's charged, it doesn't open a
// free-access path, so there's no equivalent leak to close.
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
// Bare repo-call returns in extendSubscription/activateTrialNow/
// resumeSubscription below are deliberate — same funnel-into-one-deferred-
// apperr-classification tradeoff as changePlanWithMetadata/cancelSubscription
// (service_subscription.go) and redeemCoupon (service_coupon.go): every repo
// call already self-prefixes, and errors.Is classification on the sentinels
// above runs on the raw error before any of this would wrap it anyway.
func (s *service) extendSubscription(
	ctx context.Context, subjectType, subjectID string,
	months int, switchToAnnual bool, _ string,
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
			err = apperr.Validation("EXTENSION_ALREADY_PENDING", "an invoice is already pending payment for this subscription — pay or void it before requesting an extension")
		case errors.Is(err, ErrExtensionExceedsMaxDuration):
			// Deliberately err.Error(), not the bare sentinel — the actual
			// returned error (below) appends dynamic "at most until <date>"
			// detail via %w that the user needs to see, unlike
			// redeemCoupon's ErrCouponNotRedeemable case. The
			// "billing.extendSubscription:" prefix baked into that same
			// construction leaks into this user-facing validation message
			// too — a minor cosmetic wart, left as is.
			err = apperr.Validation("EXTENSION_EXCEEDS_MAX_DURATION", err.Error())
		case errors.Is(err, ErrAlreadyYearly):
			err = apperr.Validation("SUBSCRIPTION_ALREADY_YEARLY", "subscription is already on the yearly cycle")
		case errors.Is(err, ErrUnknownPlan):
			err = apperr.Validation("UNKNOWN_PLAN", "unknown plan")
		default:
			err = apperr.Internal("SUBSCRIPTION_EXTEND_FAILED", "failed to extend subscription", err)
		}
	}()

	var sub *subscriptionRecord
	var isPending bool
	var addons []attachedAddonRecord
	err = s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var findErr error
		sub, findErr = s.repo.findSubscriptionBySubject(txCtx, tx, subjectType, subjectID)
		if findErr != nil {
			return findErr
		}
		if sub.Status != statusActive || sub.PeriodStart == nil || sub.PeriodEnd == nil {
			return nil
		}
		isPending, findErr = s.repo.hasPendingInvoiceBlockingExtend(txCtx, tx, sub.ID)
		if findErr != nil || isPending {
			return findErr
		}
		addons, findErr = s.repo.listAttachedAddonsWithPricing(txCtx, tx, sub.ID)
		return findErr
	})
	if err != nil {
		return nil, err
	}
	if sub.Status != statusActive || sub.PeriodStart == nil || sub.PeriodEnd == nil {
		return nil, ErrSubscriptionNotExtendable
	}
	if switchToAnnual {
		if sub.Cycle != cycleMonthly {
			return nil, ErrAlreadyYearly
		}
		months = 12
	}
	if isPending {
		return nil, ErrExtensionAlreadyPending
	}

	newPeriodEnd := sub.PeriodEnd.AddDate(0, months, 0)
	now := time.Now()
	if months > maxExtendableMonths(*sub.PeriodEnd, now) {
		maxAllowedEnd := truncateToDay(now.AddDate(0, maxRunwayMonths, 0))
		return nil, fmt.Errorf("billing.extendSubscription: %w: at most until %s",
			ErrExtensionExceedsMaxDuration, maxAllowedEnd.Format(time.RFC3339))
	}

	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, ErrUnknownPlan
	}
	if addons == nil {
		addons = []attachedAddonRecord{}
	}
	subtotal := computeExtensionSubtotal(planInfo, sub.Currency, months) +
		computeExtensionAddonSubtotal(addons, sub.Currency, months)

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
	err = s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID,
			sub.ID, subtotal, taxRate, tax, sub.Currency, "extension", switchToAnnual, &months)
		if err != nil {
			return err
		}
		if err := s.insertExtensionLineItems(ctx, inv.ID, planInfo, addons, sub.Currency, months); err != nil {
			return err
		}
		// Enqueued inside this transaction — commits before the payment-link
		// HTTP call below starts, same billingPay-group pattern as every
		// other site here: the durability guarantee only needs to cover the
		// DB write and the outbox row, not a successful provider response.
		return events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", subjectID,
			events.InvoiceCreated{
				OrgID: subjectID, InvoiceID: inv.ID,
				Plan: sub.Plan, AmountCents: inv.AmountCents,
				Currency: sub.Currency, DueAt: *inv.DueAt,
			})
	})
	if err != nil {
		return nil, err
	}

	// Payment link is a convenience, not load-bearing — a missing/unconfigured
	// provider shouldn't block the extension itself; the invoice is already
	// created and can be paid via the regular POST .../invoices/:id/pay flow,
	// same fail-open convention as provisionSubscription.
	_, _ = s.createPaymentLink(ctx, subjectType, subjectID, inv.ID)

	s.scheduleRenewalSequence(ctx, &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType,
		SubjectID: sub.SubjectID, PeriodEnd: &newPeriodEnd,
	})

	return inv, nil
}

// activateTrialNow lets a trialing organization skip the rest of its trial
// and start a paid period immediately: invoices the current plan (full
// cycle price, addons/coupons included via composeInvoiceAmount, same as
// any other invoice-creation site) and converts the subscription to active
// with a fresh period starting now.
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

	var sub *subscriptionRecord
	var composed int64
	var addonLines []lineItemSpec
	var couponCode string
	var discountCents int64

	planInfo := (*contracts.PlanInfo)(nil)

	err = s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var findErr error
		sub, findErr = s.repo.findSubscriptionBySubject(txCtx, tx, subjectType, subjectID)
		if findErr != nil {
			return findErr
		}
		if sub.Status != statusTrialing {
			return nil
		}
		planInfo, findErr = s.planCatalog(txCtx, sub.Plan)
		if findErr != nil {
			return findErr
		}
		composed, addonLines, couponCode, discountCents, findErr = s.composeInvoiceAmount(
			txCtx, tx, sub.ID, planInfo, sub.Currency, sub.Cycle)
		return findErr
	})
	if err != nil {
		return nil, err
	}
	if sub.Status != statusTrialing {
		return nil, ErrSubscriptionNotTrialing
	}
	if planInfo == nil {
		return nil, ErrUnknownPlan
	}

	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0)
	if sub.Cycle == cycleYearly {
		periodEnd = now.AddDate(1, 0, 0)
	}
	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(composed, taxRate)

	// DB-local writes wrapped in one transaction — see extendSubscription for why.
	var updated *subscriptionRecord
	err = s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID,
			sub.ID, composed, taxRate, tax, sub.Currency, "activation", false, nil)
		if err != nil {
			return err
		}
		if err := s.insertPlanLineItem(ctx, inv, planInfo); err != nil {
			return err
		}
		if err := s.applyInvoiceCharges(
			ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency,
			addonLines, couponCode, discountCents,
		); err != nil {
			return err
		}

		updated, err = s.repo.activateTrialImmediately(ctx, s.querier(ctx), sub.ID, now, periodEnd)
		if err != nil {
			return err
		}
		if _, err := s.repo.insertHistory(
			ctx, s.querier(ctx), sub.ID, "activate", &sub.Plan,
			&sub.Plan, composed, sub.Currency, activatedBy, nil,
		); err != nil {
			return err
		}
		// Enqueued inside this transaction — see extendSubscription's
		// comment on why this must commit before the payment-link call below.
		if err := events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, events.RoutingKeySubscriptionActivated, "billing", subjectID,
			events.SubscriptionActivated{
				OrgID: subjectID, SubscriptionID: updated.ID, Plan: updated.Plan, ActivatedAt: now,
			}); err != nil {
			return err
		}
		return events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", subjectID,
			events.InvoiceCreated{
				OrgID: subjectID, InvoiceID: inv.ID,
				Plan: sub.Plan, AmountCents: inv.AmountCents,
				Currency: sub.Currency, DueAt: *inv.DueAt,
			})
	})
	if err != nil {
		return nil, err
	}

	// Payment link is a convenience, not load-bearing — see extendSubscription.
	_, _ = s.createPaymentLink(ctx, subjectType, subjectID, inv.ID)

	s.scheduleRenewalSequence(ctx, &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType,
		SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
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

	err = s.withOrgTx(ctx, subjectType, subjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var findErr error
		sub, findErr = s.repo.findSubscriptionBySubject(txCtx, tx, subjectType, subjectID)
		return findErr
	})
	if err != nil {
		return nil, err
	}

	switch sub.Status {
	case statusCancelled:
		return s.resumeFromCancelled(ctx, subjectID, sub)
	case statusExpired:
		return s.resumeFromExpired(ctx, sub, resumedBy)
	default:
		return nil, ErrSubscriptionNotResumable
	}
}

// resumeFromCancelled reactivates a cancelled subscription with a fresh
// billing period starting now — except a subscription cancelled while still
// within its original trial window, which resumes back into "trialing", not
// "active": the trial hasn't actually expired, and marking it "active"
// would imply a paying customer even though no invoice was ever created.
// cancelSubscription only ever touches status, never trial_end, so
// sub.TrialEnd is a reliable check here.
func (s *service) resumeFromCancelled(
	ctx context.Context,
	subjectID string,
	sub *subscriptionRecord,
) (*subscriptionRecord, error) {
	now := time.Now()
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
	err := s.withOrgTx(ctx, subjectTypeOrganization, subjectID, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		updated, err = s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, newStatus)
		if err != nil {
			return err
		}
		if isStillTrialing {
			if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd); err != nil {
				return err
			}
		} else {
			if err := s.repo.clearTrialEndAndUpdatePeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd); err != nil {
				return err
			}
		}
		if _, err := s.repo.insertHistory(
			ctx, s.querier(ctx), sub.ID, "resume",
			&sub.Plan, &sub.Plan, 0, sub.Currency,
			changedBySystem, nil,
		); err != nil {
			return err
		}
		return events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, events.RoutingKeySubscriptionResumed, "billing", subjectID,
			events.SubscriptionResumed{OrgID: subjectID, SubscriptionID: sub.ID, Plan: sub.Plan, ResumedAt: now})
	})
	if err != nil {
		return nil, err
	}

	renewalSub := &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType,
		SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
	}
	if isStillTrialing {
		renewalSub.TrialEnd = sub.TrialEnd
	}
	s.scheduleRenewalSequence(ctx, renewalSub)

	return updated, nil
}

// resumeFromExpired invoices an expired subscription's next period at its
// current plan (addons/coupons included via composeInvoiceAmount, same as
// any other invoice) without changing its status yet — the subscription
// stays "expired" until handleWebhook (service_webhook.go) confirms this
// invoice paid, at which point it reactivates. Only creating the invoice
// here (rather than reactivating immediately) means an owner who starts but
// never completes this payment never silently regains access.
func (s *service) resumeFromExpired(ctx context.Context, sub *subscriptionRecord, resumedBy string) (*subscriptionRecord, error) {
	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, fmt.Errorf("billing.resumeFromExpired: %w: current plan", ErrUnknownPlan)
	}
	var composed int64
	var addonLines []lineItemSpec
	var couponCode string
	var discountCents int64
	err = s.withOrgTx(ctx, subjectTypeOrganization, sub.SubjectID, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var compErr error
		composed, addonLines, couponCode, discountCents, compErr = s.composeInvoiceAmount(
			txCtx, tx, sub.ID, planInfo, sub.Currency, sub.Cycle)
		return compErr
	})
	if err != nil {
		return nil, err
	}
	taxRate := 0
	if s.taxReader != nil {
		taxRate, _ = s.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(composed, taxRate)

	var inv *invoiceRecord
	err = s.withOrgTx(ctx, subjectTypeOrganization, sub.SubjectID, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		var err error
		// Stays "subscription", not "activation": the webhook dispatches
		// this invoice's payment by subscription status
		// (sub.Status == statusExpired, checked before the kind-based
		// cases), so its kind never reaches the generic renewal case
		// either way — safe regardless of which value it carries.
		inv, err = s.repo.insertInvoice(ctx, s.querier(ctx), sub.SubjectID,
			sub.ID, composed, taxRate, tax, sub.Currency, "subscription", false, nil)
		if err != nil {
			return err
		}
		if err := s.insertPlanLineItem(ctx, inv, planInfo); err != nil {
			return err
		}
		if err := s.applyInvoiceCharges(
			ctx, s.querier(ctx), sub.ID, inv.ID, sub.Currency,
			addonLines, couponCode, discountCents,
		); err != nil {
			return err
		}
		if _, err := s.repo.insertHistory(
			ctx, s.querier(ctx), sub.ID, "resume", &sub.Plan,
			&sub.Plan, composed, sub.Currency, resumedBy, nil,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Payment link is a convenience, not load-bearing — see extendSubscription.
	_, _ = s.createPaymentLink(ctx, subjectTypeOrganization, sub.SubjectID, inv.ID)

	return sub, nil
}

// scheduleRenewalSequence enqueues delayed messages for the renewal flow:
// remind (7 days before a normal period end, 2 days before a trial end —
// trials run exactly 7 days, so a 7-day lead would resolve to ~now and
// never actually fire), 3 days before → auto-invoice, at end → expire check.
// Runs after its caller's own state-changing transaction has already
// committed (every call site calls this post-commit), so there's no
// transaction left here for an enqueue failure to roll back — errors are
// logged, not propagated, matching this function's existing "duplicate
// delayed messages are harmless" tolerance for the class of loss this can
// still leave (a rare DB-level failure on the outbox insert itself, not a
// broker outage — the outbox+relay already covers that case durably).
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
		if err := events.EnqueueDelayed(ctx, s.querier(ctx), events.ExchangeBillingDelay,
			events.DelayRoutingKeySubscriptionRemind, "billing", sub.SubjectID, checkPayload, d); err != nil {
			slog.Error("billing.scheduleRenewalSequence: enqueue remind failed", "subscription_id", sub.ID, "error", err)
		}
	}

	if d := time.Until(endTime.AddDate(0, 0, -3)); d > 0 {
		if err := events.EnqueueDelayed(ctx, s.querier(ctx), events.ExchangeBillingDelay,
			events.DelayRoutingKeySubscriptionAutoInvoice, "billing", sub.SubjectID, checkPayload, d); err != nil {
			slog.Error("billing.scheduleRenewalSequence: enqueue auto-invoice failed", "subscription_id", sub.ID, "error", err)
		}
	}

	d := time.Until(endTime)
	if d <= 0 {
		d = time.Second
	}
	if err := events.EnqueueDelayed(ctx, s.querier(ctx), events.ExchangeBillingDelay,
		events.DelayRoutingKeySubscriptionCheck, "billing", sub.SubjectID, checkPayload, d); err != nil {
		slog.Error("billing.scheduleRenewalSequence: enqueue expiry check failed", "subscription_id", sub.ID, "error", err)
	}
}
