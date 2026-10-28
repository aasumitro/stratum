package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// Worker consumes domain events and drives billing side-effects.
type Worker struct {
	svc *service
}

// HandleOrganizationDeleted cancels the active subscription when an organization is deleted.
func (w *Worker) HandleOrganizationDeleted(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.OrganizationDeleted](body)
	if err != nil {
		return fmt.Errorf("billing.HandleOrganizationDeleted: decode: %w", err)
	}
	if err := w.svc.cancelOnDeletion(ctx, evt.OrganizationID); err != nil {
		return fmt.Errorf("billing.HandleOrganizationDeleted: %w", err)
	}
	return nil
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
		return fmt.Errorf("billing.HandleOrganizationCreated: decode: %w", err)
	}
	_, err = w.svc.provisionSubscription(ctx, subjectTypeOrganization, evt.OrganizationID,
		evt.Plan, evt.Cycle, evt.CreatedBy, evt.CountryCode, evt.Addons, evt.CouponCode)
	if err != nil {
		return fmt.Errorf("billing.HandleOrganizationCreated: %w", err)
	}
	return nil
}

// HandleSubscriptionCheck processes a delayed end-of-period check: a
// subscription past its end date is ended by expireIfDue — cancelled when a
// cancellation is scheduled, expired otherwise.
func (w *Worker) HandleSubscriptionCheck(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionCheck: decode: %w", err)
	}
	if err := w.svc.expireIfDue(ctx, check.SubscriptionID); err != nil {
		return fmt.Errorf("billing.HandleSubscriptionCheck: %w", err)
	}
	return nil
}

// staleSubscriptionCheck reports whether a delayed SubscriptionCheck message
// still matches the subscription's current end date. scheduleRenewalSequence
// is called every time period_end/trial_end changes (extend, reactivate,
// resume, plan change), but the previously-scheduled delayed message for the
// old end date is never cancelled — this platform's delayed-message path
// (events.EnqueueDelayed) has no cancellation primitive. A message whose
// ExpectedEnd no longer matches the subscription's current end date is one
// of those leftovers: a fresh message for the new end date was already
// enqueued by whatever changed it, so this one should no-op rather than act
// on a date that's no longer real. The comparison runs at microsecond
// precision: Postgres stores timestamptz in microseconds, while an end
// computed from time.Now() keeps nanoseconds on clocks that have them, so
// the stored and the scheduled value of the same instant can differ below
// one microsecond.
func staleSubscriptionCheck(sub *subscriptionRecord, check events.SubscriptionCheck) bool {
	currentEnd := sub.PeriodEnd
	if check.IsTrial {
		currentEnd = sub.TrialEnd
	}
	return currentEnd == nil ||
		!currentEnd.Truncate(time.Microsecond).Equal(check.ExpectedEnd.Truncate(time.Microsecond))
}

// HandleSubscriptionRemind checks a scheduled reminder against the live
// subscription and, if it still applies, publishes it under
// RoutingKeySubscriptionExpiringSoon for notification. It never publishes
// its own input key: this handler's queue binds that key, so doing so would
// loop.
func (w *Worker) HandleSubscriptionRemind(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionRemind: decode: %w", err)
	}
	sub, err := w.svc.repo.findSubscriptionByID(ctx, w.svc.pool, check.SubscriptionID)
	if err != nil || (sub.Status != statusActive && sub.Status != statusTrialing) {
		return nil
	}
	if staleSubscriptionCheck(sub, check) {
		slog.Info("skipping stale subscription reminder",
			"subscription_id", sub.ID, "expected_end", check.ExpectedEnd)
		return nil
	}
	return w.svc.enqueueEvent(ctx, events.RoutingKeySubscriptionExpiringSoon, sub.SubjectID,
		events.SubscriptionCheck{
			SubscriptionID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID,
			Plan: sub.Plan, ExpectedEnd: check.ExpectedEnd, IsTrial: sub.TrialEnd != nil,
		})
}

// HandleSubscriptionPaymentRemind checks a scheduled day-3 dunning row
// against the live invoice and, if it still applies, publishes it under
// RoutingKeyInvoicePaymentReminder for notification.
func (w *Worker) HandleSubscriptionPaymentRemind(ctx context.Context, body []byte) error {
	return w.forwardDunningNotice(ctx, body, events.RoutingKeyInvoicePaymentReminder)
}

// HandleSubscriptionPaymentFinal is HandleSubscriptionPaymentRemind for the
// day-7 final notice, published under RoutingKeyInvoicePaymentFinalNotice.
func (w *Worker) HandleSubscriptionPaymentFinal(ctx context.Context, body []byte) error {
	return w.forwardDunningNotice(ctx, body, events.RoutingKeyInvoicePaymentFinalNotice)
}

// forwardDunningNotice publishes a scheduled dunning notice (payload
// events.InvoiceFailed) under publishKey only while it is still true: the
// invoice is still pending, it backs the subscription's own period (a failed
// extension or addon-increase payment leaves the current period paid), and
// the subscription has not ended. A renewal invoice is created three days
// before period_end, so by day 3 its subscription has usually either been
// paid or expired — expireIfDue then marks the invoice failed (no longer
// payable) and SubscriptionExpired has already told the owner. It never
// publishes its own input keys: its queues bind those, so doing so would loop.
func (w *Worker) forwardDunningNotice(ctx context.Context, body []byte, publishKey string) error {
	evt, err := events.Decode[events.InvoiceFailed](body)
	if err != nil {
		return fmt.Errorf("billing.forwardDunningNotice: decode: %w", err)
	}
	inv, err := w.svc.repo.findInvoiceByID(ctx, w.svc.pool, evt.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("billing.forwardDunningNotice: find invoice: %w", err)
	}
	sub, err := w.svc.repo.findSubscriptionByID(ctx, w.svc.pool, inv.SubscriptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("billing.forwardDunningNotice: find subscription: %w", err)
	}
	live := sub.Status == statusActive || sub.Status == statusTrialing || sub.Status == statusPastDue
	if inv.Status != statusPending || !isPeriodBacking(inv.Kind) || !live {
		slog.Info("skipping dunning notice that no longer applies",
			"invoice_id", inv.ID, "invoice_status", inv.Status, "invoice_kind", inv.Kind,
			"subscription_status", sub.Status, "routing_key", publishKey)
		return nil
	}
	return w.svc.enqueueEvent(ctx, publishKey, evt.OrgID, evt)
}

// reconcileScheduledCancellation takes the row lock covering this renewal
// decision and hands the unlocked subscription back to the caller to
// compose a renewal invoice — unless a cancellation is scheduled: no
// renewal is invoiced for a period the owner has cancelled, and expireIfDue
// applies the cancellation when the current period ends. The lock is only
// held for this decision, not for the invoice/payment-link work that follows
// in the caller — that work makes a blocking call to Stripe/Xendit, and
// holding a transaction (and its pooled connection) open across an external
// HTTP round-trip is the same pool-exhaustion risk this module's billingPay
// route group already avoids for the interactive pay-invoice endpoint
// (module.go).
//
// Returns (nil, nil) when there is nothing for the caller to do: the
// subscription wasn't found, isn't active/trialing, the check is stale, or
// a cancellation is scheduled. Returns (sub, nil) when the caller should
// proceed to compose an invoice for sub. Returns (nil, err) on failure, in
// which case the transaction rolled back and nothing changed — a later
// redelivery of the same event retries from the same state.
func (w *Worker) reconcileScheduledCancellation(
	ctx context.Context, check events.SubscriptionCheck,
) (*subscriptionRecord, error) {
	var sub *subscriptionRecord
	err := db.WithTx(ctx, w.svc.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		q := w.svc.querier(txCtx)

		if err := w.svc.repo.lockSubscriptionForUpdate(txCtx, q, check.SubscriptionID); err != nil {
			return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: %w", err)
		}
		found, err := w.svc.repo.findSubscriptionByID(txCtx, q, check.SubscriptionID)
		if err != nil || (found.Status != statusActive && found.Status != statusTrialing) {
			return nil
		}
		if staleSubscriptionCheck(found, check) {
			slog.Info("skipping stale auto-invoice", "subscription_id", found.ID, "expected_end", check.ExpectedEnd)
			return nil
		}
		if found.ScheduledCancelAt != nil {
			slog.Info("skipping renewal invoice: cancellation scheduled", "subscription_id", found.ID)
			return nil
		}
		sub = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// reconcileScheduledOverage applies sub's scheduled plan downgrade and/or
// addon quantity decreases, resolving any resulting member overage exactly
// once against the fully combined future state before either applies —
// never two separate overage checks for one renewal. Like
// reconcileScheduledCancellation, this takes its own row lock in a short
// transaction and commits before the caller composes a renewal invoice, so
// composeInvoiceAmount's blocking payment-provider call never runs inside a
// held transaction. Re-reads the subscription and its scheduled addons
// fresh under the lock rather than trusting the caller's copy, so a
// concurrent redelivery that already applied the schedule is a safe no-op
// here rather than a stale double-apply.
func (w *Worker) reconcileScheduledOverage(ctx context.Context, subscriptionID string) (*subscriptionRecord, error) {
	var result *subscriptionRecord
	err := db.WithTx(ctx, w.svc.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		q := w.svc.querier(txCtx)

		if err := w.svc.repo.lockSubscriptionForUpdate(txCtx, q, subscriptionID); err != nil {
			return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
		}
		sub, err := w.svc.repo.findSubscriptionByID(txCtx, q, subscriptionID)
		if err != nil {
			return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
		}
		scheduledAddons, err := w.svc.repo.listScheduledAddonChanges(txCtx, q, subscriptionID)
		if err != nil {
			return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
		}
		hasScheduledPlan := sub.ScheduledPlan != nil
		if !hasScheduledPlan && len(scheduledAddons) == 0 {
			result = sub
			return nil
		}

		if w.svc.orgCommander != nil {
			overage, err := w.svc.resolveFutureOveragePreview(txCtx, sub)
			if err != nil {
				return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
			}
			if overage.MemberLimit >= 0 && overage.CurrentMembers > int64(overage.MemberLimit) {
				if _, err := w.svc.orgCommander.ResolveDowngradeOverage(txCtx, sub.SubjectID,
					nil, overage.MemberLimit, false); err != nil {
					return fmt.Errorf("billing.reconcileScheduledOverage: resolve overage: %w", err)
				}
			}
		}

		if hasScheduledPlan {
			if _, err := w.svc.repo.applyScheduledPlanDowngrade(txCtx, q, sub.ID); err != nil {
				return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
			}
			phase := historyPhaseApplied
			appliedAt := time.Now()
			if _, err := w.svc.repo.insertHistoryWithCycle(txCtx, q, sub.ID, actionDowngrade,
				&sub.Plan, sub.ScheduledPlan, 0, sub.Currency, changedBySystem, nil, &phase, &appliedAt,
				&sub.Cycle, sub.ScheduledCycle); err != nil {
				return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
			}
		}
		for _, a := range scheduledAddons {
			if a.ScheduledQuantity == 0 {
				if err := w.svc.repo.deleteSubscriptionAddon(txCtx, q, sub.ID, a.AddonID); err != nil {
					return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
				}
			} else if err := w.svc.repo.applyScheduledAddonQuantityChange(txCtx, q, sub.ID, a.AddonID, a.ScheduledQuantity); err != nil {
				return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
			}
			addonMetadata, _ := json.Marshal(addonChangeMetadata{
				AddonID: a.AddonID, FromQuantity: a.LiveQuantity, ToQuantity: a.ScheduledQuantity,
			})
			addonPhase := historyPhaseApplied
			addonAppliedAt := time.Now()
			if _, err := w.svc.repo.insertHistoryWithPhase(txCtx, q, sub.ID, "addon_change",
				nil, nil, 0, sub.Currency, changedBySystem, addonMetadata, &addonPhase, &addonAppliedAt); err != nil {
				return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
			}
		}

		result, err = w.svc.repo.findSubscriptionByID(txCtx, q, sub.ID)
		if err != nil {
			return fmt.Errorf("billing.reconcileScheduledOverage: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// HandlePaymentLinkExpire closes a locally expired payment link's hosted
// checkout at its provider (events.RoutingKeyPaymentLinkExpire).
func (w *Worker) HandlePaymentLinkExpire(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.PaymentLinkExpire](body)
	if err != nil {
		return fmt.Errorf("billing.HandlePaymentLinkExpire: decode: %w", err)
	}
	return w.svc.expireCheckout(ctx, evt.Provider, evt.ExternalID)
}

// HandleInvoiceMarkPaid applies an operator's "mark paid" from Studio
// (events.RoutingKeyInvoiceMarkPaid) as a confirmed payment.
func (w *Worker) HandleInvoiceMarkPaid(ctx context.Context, body []byte) error {
	evt, err := events.Decode[events.InvoiceMarkPaid](body)
	if err != nil {
		return fmt.Errorf("billing.HandleInvoiceMarkPaid: decode: %w", err)
	}
	return w.svc.applyMarkedPaid(ctx, evt.InvoiceID)
}

// HandleSubscriptionAutoInvoice generates a renewal invoice 3 days before expiry.
func (w *Worker) HandleSubscriptionAutoInvoice(ctx context.Context, body []byte) error {
	check, err := events.Decode[events.SubscriptionCheck](body)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: decode: %w", err)
	}

	sub, err := w.reconcileScheduledCancellation(ctx, check)
	if err != nil || sub == nil {
		// err nil + sub nil covers every early-return case (not found, wrong
		// status, stale check, cancellation scheduled) — either way there's
		// no invoice to compose.
		return err
	}

	sub, err = w.reconcileScheduledOverage(ctx, sub.ID)
	if err != nil {
		return err
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
		if _, err := w.svc.createPaymentLink(ctx, "", "", pending.ID); err != nil {
			return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: regenerate payment link: %w", err)
		}
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		// no pending invoice yet — create one below
	default:
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: find pending invoice: %w", err)
	}

	planInfo, err := w.svc.planCatalog(ctx, sub.Plan)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: %w", err)
	}

	composed, addonLines, couponCode, discountCents, err := w.svc.composeInvoiceAmount(
		ctx, w.svc.pool, sub.ID, planInfo, sub.Currency, sub.Cycle)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: compose invoice amount: %w", err)
	}
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

	// DB-local writes (invoice + line item + charges) wrapped in one
	// transaction so a mid-sequence failure can't leave an orphaned invoice
	// with a wrong/missing line-item breakdown — same convention every other
	// invoice-creation site in this module already uses (provisionSubscription,
	// extendSubscription, activateTrialNow, resumeSubscription). The payment
	// link (blocking Stripe/Xendit HTTP call) and event publish stay outside,
	// after this transaction commits.
	var inv *invoiceRecord
	err = db.WithTx(ctx, w.svc.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		var err error
		inv, err = w.svc.repo.insertInvoice(txCtx, tx, sub.SubjectID, sub.ID,
			composed, taxRate, tax, sub.Currency, "subscription", false, nil)
		if err != nil {
			return err
		}
		if err := w.svc.insertPlanLineItem(txCtx, inv, planInfo); err != nil {
			return err
		}
		if err := w.svc.applyInvoiceCharges(
			txCtx, tx, sub.ID, inv.ID, sub.Currency,
			addonLines, couponCode, discountCents,
		); err != nil {
			return err
		}
		// Enqueued inside this transaction — commits before the payment-link
		// HTTP call below starts, matching every other billingPay-group site:
		// the durability guarantee only needs to cover the DB write and the
		// outbox row, not a successful provider response too.
		return events.Enqueue(txCtx, tx, events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", sub.SubjectID,
			events.InvoiceCreated{
				OrgID: sub.SubjectID, InvoiceID: inv.ID,
				Plan: sub.Plan, AmountCents: inv.AmountCents, Currency: inv.Currency, DueAt: *inv.DueAt,
				FromTrial: sub.Status == statusTrialing,
			})
	})
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: insert invoice: %w", err)
	}

	_, err = w.svc.createPaymentLink(ctx, "", "", inv.ID)
	if err != nil {
		return fmt.Errorf("billing.HandleSubscriptionAutoInvoice: create payment link: %w", err)
	}
	return nil
}
