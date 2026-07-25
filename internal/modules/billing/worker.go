package billing

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
)

// Worker consumes domain events and drives billing side-effects.
type Worker struct {
	svc *service
}

// HandleOrganizationDeleted cancels the active subscription when an organization is deleted.
func (w *Worker) HandleOrganizationDeleted(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationDeleted](body)
	if err != nil {
		return err
	}
	return w.svc.cancelOnDeletion(ctx, evt.OrganizationID)
}

// HandleOrganizationCreated provisions a subscription for the plan and cycle
// the creator chose at organization-creation time (evt.Plan, evt.Cycle) — both
// are required at the API boundary (organization.createOrganizationRequest),
// so no defaulting happens here. An empty value reaching this far (a
// malformed or pre-contract event) fails loudly on insert instead: plan is
// FK-constrained to billing.plans(id) and cycle is CHECK-constrained to
// ('monthly', 'yearly') — see db/migrations/000004_billing.up.sql.
func (w *Worker) HandleOrganizationCreated(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationCreated](body)
	if err != nil {
		return err
	}
	_, err = w.svc.provisionSubscription(ctx, subjectTypeOrganization, evt.OrganizationID,
		evt.Plan, evt.Cycle, evt.CreatedBy, evt.CountryCode, evt.Addons, evt.CouponCode)
	return err
}

// HandleSubscriptionCheck processes a delayed expiry check. If the
// subscription is still active/trialing past its end date, mark it expired.
func (w *Worker) HandleSubscriptionCheck(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return err
	}
	return w.svc.expireIfDue(ctx, check.SubscriptionID)
}

// staleSubscriptionCheck reports whether a delayed SubscriptionCheck message
// still matches the subscription's current end date. scheduleRenewalSequence
// is called every time period_end/trial_end changes (extend, reactivate,
// resume, plan change), but the previously-scheduled delayed message for the
// old end date is never cancelled — this platform's events.PublishDelayed has
// no cancellation primitive. A message whose ExpectedEnd no longer matches
// the subscription's current end date is one of those leftovers: a fresh
// message for the new end date was already published by whatever changed it,
// so this one should no-op rather than act on a date that's no longer real.
func staleSubscriptionCheck(sub *subscriptionRecord, check events.SubscriptionCheck) bool {
	currentEnd := sub.PeriodEnd
	if check.IsTrial {
		currentEnd = sub.TrialEnd
	}
	return currentEnd == nil || !currentEnd.Equal(check.ExpectedEnd)
}

// HandleSubscriptionRemind re-publishes as a normal event so notification can consume it.
func (w *Worker) HandleSubscriptionRemind(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return err
	}
	sub, err := w.svc.repo.findSubscriptionByID(ctx, w.svc.pool, check.SubscriptionID)
	if err != nil || (sub.Status != statusActive && sub.Status != statusTrialing) {
		return nil
	}
	if staleSubscriptionCheck(sub, check) {
		slog.Info("skipping stale subscription reminder", "subscription_id", sub.ID, "expected_end", check.ExpectedEnd)
		return nil
	}
	events.Publish(ctx, w.svc.pub, events.ExchangeBilling, events.RoutingKeySubscriptionRemind, "billing", sub.SubjectID,
		events.SubscriptionCheck{
			SubscriptionID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID,
			Plan: sub.Plan, ExpectedEnd: check.ExpectedEnd, IsTrial: sub.TrialEnd != nil,
		})
	return nil
}

// HandleSubscriptionAutoInvoice generates a renewal invoice 3 days before expiry.
func (w *Worker) HandleSubscriptionAutoInvoice(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return err
	}
	sub, err := w.svc.repo.findSubscriptionByID(ctx, w.svc.pool, check.SubscriptionID)
	if err != nil || (sub.Status != statusActive && sub.Status != statusTrialing) {
		return nil
	}
	if staleSubscriptionCheck(sub, check) {
		slog.Info("skipping stale auto-invoice", "subscription_id", sub.ID, "expected_end", check.ExpectedEnd)
		return nil
	}

	pending, err := w.svc.repo.findPendingInvoiceBySubscription(ctx, w.svc.pool, sub.ID)
	switch {
	case err == nil:
		// A pending invoice already exists — most likely a prior delivery of
		// this event created it but failed before creating its payment link
		// (the link is a separate write after the invoice's). Regenerate the
		// link instead of leaving the invoice permanently unpayable; if it
		// already has one, there's nothing left to do.
		if _, linkErr := w.svc.repo.findActivePaymentLinkByInvoice(ctx, w.svc.pool, pending.ID); linkErr == nil {
			return nil
		}
		_, err = w.svc.createPaymentLink(ctx, "", "", pending.ID)
		return err
	case errors.Is(err, pgx.ErrNoRows):
		// no pending invoice yet — create one below
	default:
		return err
	}

	planInfo, err := w.svc.planCatalog(ctx, sub.Plan)
	if err != nil {
		return err
	}

	composed, addonLines, couponCode, discountCents := w.svc.composeInvoiceAmount(
		ctx, w.svc.pool, sub.ID, planInfo, sub.Currency, sub.Cycle)
	if composed <= 0 {
		// Zero-priced plan (e.g. "custom", contact-us only, never self-service
		// selectable) with no addons — insertInvoice's amount_cents CHECK
		// requires > 0, and there's nothing to bill anyway. Same guard the
		// other three composeInvoiceAmount call sites already apply.
		return nil
	}
	taxRate := 0
	if w.svc.taxReader != nil {
		taxRate, _ = w.svc.taxReader.GetCountryTaxRate(ctx, countryFromCurrency(sub.Currency))
	}
	tax := calculateTax(composed, taxRate)
	inv, err := w.svc.repo.insertInvoice(ctx, w.svc.pool, sub.SubjectID, sub.ID, composed, taxRate, tax, sub.Currency, "subscription", false)
	if err != nil {
		return err
	}
	_ = w.svc.insertPlanLineItem(ctx, inv, planInfo)
	w.svc.applyInvoiceCharges(ctx, w.svc.pool, sub.ID, inv.ID, sub.Currency, addonLines, couponCode, discountCents)

	_, err = w.svc.createPaymentLink(ctx, "", "", inv.ID)
	if err != nil {
		return err
	}

	events.Publish(ctx, w.svc.pub, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", sub.SubjectID,
		events.InvoiceCreated{
			OrgID: sub.SubjectID, InvoiceID: inv.ID,
			Plan: sub.Plan, AmountCents: inv.AmountCents, Currency: inv.Currency, DueAt: *inv.DueAt,
			FromTrial: sub.Status == statusTrialing,
		})
	return nil
}
