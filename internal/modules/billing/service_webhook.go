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

// webhookOutcome collects the events/scheduling a webhook triggers while its
// DB effects run inside processWebhook's transaction. The events themselves
// are enqueued inside that same transaction (enqueueWebhookOutcome, called
// from within it) — outcome only still carries scheduleRenewal forward,
// since scheduleRenewalSequence deliberately runs after commit (see its own
// doc comment).
type webhookOutcome struct {
	paid            *events.InvoicePaid
	resumed         *events.SubscriptionResumed
	extended        *events.SubscriptionExtended
	failed          *events.InvoiceFailed
	scheduleRenewal *subscriptionRecord
}

// enqueueWebhookOutcome writes an outbox row for every event outcome
// carries, using tx — the same transaction as processWebhook's DB effects —
// so they commit atomically with the webhook state they describe instead of
// publishing after the fact. outcome may be nil (a duplicate delivery
// short-circuited before handleWebhook ran).
func enqueueWebhookOutcome(ctx context.Context, tx db.Querier, outcome *webhookOutcome) error {
	if outcome == nil {
		return nil
	}
	if outcome.paid != nil {
		if err := events.Enqueue(ctx, tx, events.ExchangeBilling, events.RoutingKeyInvoicePaid,
			"billing", outcome.paid.OrgID, *outcome.paid); err != nil {
			return err
		}
	}
	if outcome.resumed != nil {
		if err := events.Enqueue(ctx, tx, events.ExchangeBilling, events.RoutingKeySubscriptionResumed,
			"billing", outcome.resumed.OrgID, *outcome.resumed); err != nil {
			return err
		}
	}
	if outcome.extended != nil {
		if err := events.Enqueue(ctx, tx, events.ExchangeBilling, events.RoutingKeySubscriptionExtended,
			"billing", outcome.extended.OrgID, *outcome.extended); err != nil {
			return err
		}
	}
	if outcome.failed != nil {
		if err := events.Enqueue(ctx, tx, events.ExchangeBilling, events.RoutingKeyInvoiceFailed,
			"billing", outcome.failed.OrgID, *outcome.failed); err != nil {
			return err
		}
		if err := events.EnqueueDelayed(ctx, tx, events.ExchangeBilling, events.RoutingKeySubscriptionPaymentRemind,
			"billing", outcome.failed.OrgID, *outcome.failed, 3*24*time.Hour); err != nil {
			return err
		}
		if err := events.EnqueueDelayed(ctx, tx, events.ExchangeBilling, events.RoutingKeySubscriptionPaymentFinal,
			"billing", outcome.failed.OrgID, *outcome.failed, 7*24*time.Hour); err != nil {
			return err
		}
	}
	return nil
}

// processWebhook marks (provider, eventID) processed and applies the
// callback's effects in one transaction. A failure anywhere in handleWebhook
// rolls back the idempotency marker along with every write it made, so a
// transient error is retried by the provider on redelivery instead of being
// silently dropped (the marker would otherwise already be committed,
// causing the retry to be ACK'd as a duplicate without ever re-running).
// eventID may be empty (test fixtures without a provider event ID) — the
// idempotency marker is skipped, but the DB effects are still atomic.
// invoiceID, when non-empty, resolves the payment link by invoice instead
// of externalID — Stripe's payment_intent.* event types carry a
// PaymentIntent (pi_...) as their data object, a different ID namespace
// than the Checkout Session ID (cs_...) stored as external_id, so
// external_id can never match for those events (see handleStripeWebhook).
func (s *service) processWebhook(ctx context.Context, provider, eventID, externalID, invoiceID, normalizedStatus string) error {
	var outcome *webhookOutcome
	err := db.WithTx(ctx, s.webhookPool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		if eventID != "" {
			inserted, err := s.repo.markWebhookProcessed(txCtx, tx, provider, eventID)
			if err != nil {
				return fmt.Errorf("billing.processWebhook: %w", err)
			}
			if !inserted {
				return nil // duplicate delivery — no-op, ACK without re-running side effects
			}
		}
		var err error
		outcome, err = s.handleWebhook(txCtx, externalID, invoiceID, normalizedStatus)
		if err != nil {
			return err
		}
		return enqueueWebhookOutcome(txCtx, tx, outcome)
	})
	if err != nil {
		return err
	}
	if outcome == nil {
		return nil
	}
	if outcome.scheduleRenewal != nil {
		s.scheduleRenewalSequence(ctx, outcome.scheduleRenewal)
	}
	return nil
}

// applyMarkedPaid applies an operator's "mark paid" from Studio as a
// confirmed payment: the same effects a provider webhook has for the
// invoice (applyInvoicePayment), minus the billing.payments row, since no
// provider took the money. Only a pending invoice moves; one already
// settled (paid, void, failed) or gone is logged and acknowledged, since
// redelivering it would change nothing.
func (s *service) applyMarkedPaid(ctx context.Context, invoiceID string) error {
	var outcome *webhookOutcome
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		inv, err := s.repo.findInvoiceByID(txCtx, tx, invoiceID)
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("billing.applyMarkedPaid: invoice not found", "invoice_id", invoiceID)
			return nil
		}
		if err != nil {
			return fmt.Errorf("billing.applyMarkedPaid: find invoice: %w", err)
		}
		if err := s.repo.lockSubscriptionForUpdate(txCtx, tx, inv.SubscriptionID); err != nil {
			return fmt.Errorf("billing.applyMarkedPaid: lock subscription: %w", err)
		}
		now := time.Now()
		applied, err := s.repo.markInvoicePaid(txCtx, tx, invoiceID, now)
		if err != nil {
			return fmt.Errorf("billing.applyMarkedPaid: mark invoice paid: %w", err)
		}
		if !applied {
			slog.Info("billing.applyMarkedPaid: invoice no longer pending, nothing to apply", "invoice_id", invoiceID)
			return nil
		}
		sub, err := s.repo.findSubscriptionByID(txCtx, tx, inv.SubscriptionID)
		if err != nil {
			return fmt.Errorf("billing.applyMarkedPaid: find subscription: %w", err)
		}
		outcome = &webhookOutcome{}
		link := &paymentLinkSubject{invoiceID: invoiceID, subscriptionID: sub.ID, subjectType: sub.SubjectType, subjectID: sub.SubjectID}
		if err := s.applyInvoicePayment(txCtx, outcome, link, nil, changedByOperator, now); err != nil {
			return err
		}
		return enqueueWebhookOutcome(txCtx, tx, outcome)
	})
	if err != nil {
		return err
	}
	if outcome != nil && outcome.scheduleRenewal != nil {
		s.scheduleRenewalSequence(ctx, outcome.scheduleRenewal)
	}
	return nil
}

// findPaymentLinkWithSubject resolves a payment link by invoice (when
// invoiceID is set) or external ID otherwise — see processWebhook's comment
// on why invoiceID takes priority. Called twice by handleWebhook: once
// before the subscription lock (to learn which subscription to lock) and
// once after (to re-read the authoritative, post-lock status).
func (s *service) findPaymentLinkWithSubject(ctx context.Context, externalID, invoiceID string) (*paymentLinkSubject, error) {
	if invoiceID != "" {
		return s.repo.findPaymentLinkWithSubjectByInvoiceID(ctx, s.querier(ctx), invoiceID)
	}
	return s.repo.findPaymentLinkWithSubjectByExternalID(ctx, s.querier(ctx), externalID)
}

// handleWebhook applies a provider callback's DB effects using ctx's
// transaction-scoped querier, returning the side effects to run once that
// transaction has committed. invoiceID, when non-empty, resolves the
// payment link by invoice instead of externalID — see processWebhook.
func (s *service) handleWebhook(ctx context.Context, externalID, invoiceID, normalizedStatus string) (*webhookOutcome, error) {
	link, err := s.findPaymentLinkWithSubject(ctx, externalID, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing.handleWebhook: find payment link with subject: %w", err)
	}

	// Lock the subscription before trusting link.status. This read of link
	// only exists to learn which subscription to lock — a concurrent
	// delivery for the same subscription may commit its own status update
	// while this call waits for the lock, so link's pre-lock snapshot can
	// be stale by the time the lock is granted. Re-fetch after acquiring it
	// (Postgres's default READ COMMITTED isolation means this new read sees
	// any such concurrent commit) so the status compared below is
	// authoritative. This covers every normalizedStatus branch, not just
	// paid, since the same read-check-write race exists for failed/expired
	// too — two distinct event types reporting the same outcome, each with
	// its own event ID and so not caught by the eventID-based check in
	// processWebhook, must serialize here instead of both reading the same
	// pre-update status and both applying its effects.
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), link.subscriptionID); err != nil {
		return nil, fmt.Errorf("billing.handleWebhook: lock subscription: %w", err)
	}
	link, err = s.findPaymentLinkWithSubject(ctx, externalID, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing.handleWebhook: find payment link with subject (post-lock): %w", err)
	}

	if link.status == normalizedStatus {
		return nil, nil
	}

	if err := s.repo.updatePaymentLinkStatus(ctx, s.querier(ctx), link.linkID, normalizedStatus); err != nil {
		return nil, fmt.Errorf("billing.handleWebhook: update payment link status: %w", err)
	}

	outcome := &webhookOutcome{}

	if normalizedStatus == statusPaid {
		now := time.Now()

		applied, err := s.repo.markInvoicePaid(ctx, s.querier(ctx), link.invoiceID, now)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: mark invoice paid: %w", err)
		}
		if !applied {
			// markInvoicePaid only moves a 'pending' invoice, and the post-lock
			// link-status check above already absorbed a redelivery for this
			// link — so a payment arrived for an invoice that is no longer
			// pending. The recoverable case: a period-backing invoice of a
			// subscription that has ended (see recoverLatePayment, which logs
			// every other case).
			recovered, err := s.recoverLatePayment(ctx, link.invoiceID, link.subscriptionID, now)
			if err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: recover late payment: %w", err)
			}
			if !recovered {
				return outcome, nil
			}
			// The invoice is now 'paid': fall through so the shared paid path
			// below records the payment, emits InvoicePaid, and reactivates the
			// ended subscription.
		}

		// Runs under s.querier(ctx) — the same DB transaction as every other
		// write in this branch. Postgres aborts an entire transaction on the
		// first failed statement, so a swallowed error here wouldn't actually
		// let the transaction proceed. Propagate instead.
		pl, err := s.repo.findPaymentLinkByExternalID(ctx, s.querier(ctx), externalID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find payment link: %w", err)
		}
		if err := s.applyInvoicePayment(ctx, outcome, link, pl, changedByWebhook, now); err != nil {
			return nil, err
		}
	}

	if normalizedStatus == statusFailed || normalizedStatus == statusExpired {
		outcome.failed = &events.InvoiceFailed{OrgID: link.subjectID, InvoiceID: link.invoiceID, FailedAt: time.Now()}
	}

	// Mark subscription past_due on a confirmed payment failure so callers can
	// gate access without waiting for the subscription-check to fire at
	// period_end. Only a failed payment for a period-backing invoice
	// ("subscription" renewal or "activation") means the subscription's own
	// period is unpaid — a failed "extension" or "addon_increase" payment is
	// additive, the current period is still covered, so past-dueing it would
	// wrongly mark a current subscription for termination at period_end.
	if normalizedStatus == statusFailed {
		sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), link.subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find subscription for past-due check: %w", err)
		}
		inv, err := s.repo.findInvoiceByID(ctx, s.querier(ctx), link.invoiceID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find invoice for past-due check: %w", err)
		}
		if sub.Status == statusActive && isPeriodBacking(inv.Kind) {
			if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusPastDue); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: mark subscription past due: %w", err)
			}
		}
	}

	return outcome, nil
}

// applyInvoicePayment applies what a confirmed payment of link.invoiceID
// buys, once that invoice is 'paid': the payment row when the money came
// through a provider link (pl), InvoicePaid, and the subscription effect for
// the invoice's kind and the subscription's status. Shared by the provider
// webhook and an operator's mark-paid (applyMarkedPaid, pl nil); paidBy is
// the history actor for what the payment applies. Every
// write here applies what the customer paid for — a failure must fail (and
// roll back) the whole transaction rather than be swallowed, or the invoice
// ends up marked paid with the subscription silently never actually
// reactivated/extended. Mutates outcome in place.
func (s *service) applyInvoicePayment(
	ctx context.Context, outcome *webhookOutcome, link *paymentLinkSubject, pl *paymentLinkRecord,
	paidBy string, now time.Time,
) error {
	inv, err := s.repo.findInvoiceByID(ctx, s.querier(ctx), link.invoiceID)
	if err != nil {
		return fmt.Errorf("billing.applyInvoicePayment: find invoice: %w", err)
	}
	paidEvt := events.InvoicePaid{
		OrgID: link.subjectID, InvoiceID: link.invoiceID,
		AmountCents: int(inv.AmountCents), Currency: inv.Currency, PaidAt: now,
	}
	if pl != nil {
		if err := s.repo.insertPayment(
			ctx, s.querier(ctx), link.invoiceID, pl.AmountCents,
			pl.Currency, pl.Provider, pl.ExternalID, now,
		); err != nil {
			return fmt.Errorf("billing.applyInvoicePayment: insert payment: %w", err)
		}
		paidEvt.AmountCents = int(pl.AmountCents)
		paidEvt.Currency = pl.Currency
	}
	outcome.paid = &paidEvt
	// The invoice is settled: any other checkout still open for it (an
	// operator's mark-paid leaves the customer's link open) must stop taking
	// money, here and at the provider. The link that just paid is already
	// 'paid', so it isn't touched.
	closed, err := s.repo.expireOpenPaymentLinks(ctx, s.querier(ctx), link.invoiceID)
	if err != nil {
		return fmt.Errorf("billing.applyInvoicePayment: %w", err)
	}
	if err := enqueueCheckoutExpiry(ctx, s.querier(ctx), link.subjectID, closed); err != nil {
		return fmt.Errorf("billing.applyInvoicePayment: %w", err)
	}

	sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), link.subscriptionID)
	if err != nil {
		return fmt.Errorf("billing.applyInvoicePayment: find subscription: %w", err)
	}
	switch {
	case sub.Status == statusExpired:
		return s.applyExpiredReactivation(ctx, outcome, link, sub, paidBy, now)
	// A paid Resume invoice — or a late payment recoverLatePayment let
	// through — on a subscription whose cancellation already took effect:
	// same fresh-period reactivation as an expired one.
	case sub.Status == statusCancelled && isPeriodBacking(inv.Kind):
		return s.applyExpiredReactivation(ctx, outcome, link, sub, paidBy, now)
	// An extension can only be requested while active, but a renewal
	// that fails before it is paid leaves the subscription past_due —
	// the months were still paid for.
	case (sub.Status == statusActive || sub.Status == statusPastDue) && inv.Kind == "extension":
		return s.applyExtensionPayment(ctx, outcome, link, inv, sub, paidBy)
	// Three invoice kinds reach this switch: "activation" (a
	// subscription's very first invoice, or a skip-trial activation —
	// its period was already fully set at creation/activation time, so
	// paying it on an active subscription is correctly a no-op here;
	// a past_due one has its own case below), "extension" (handled by
	// its own case above), and "subscription"
	// (the routine renewal invoice HandleSubscriptionAutoInvoice creates
	// 3 days before period_end — the only kind this case should roll
	// forward). Without this case matching a genuine renewal,
	// period_end never advances: expireIfDue only checks whether "now"
	// is past the *existing* period_end, with no awareness that a
	// renewal was paid, so an on-time payer got suspended on schedule
	// anyway.
	case (sub.Status == statusActive || sub.Status == statusTrialing || sub.Status == statusPastDue) &&
		inv.Kind == invoiceKindSubscription:
		return s.applyRenewalPayment(ctx, outcome, inv, sub, paidBy)
	// The activation payment failed earlier (past_due) and has now
	// arrived: the period it backs was set at activation, so only the
	// status moves. The renewal auto-invoice skipped this subscription
	// while it was past_due, so queue it again — a payment landing
	// after end−3d would otherwise never get a renewal invoice.
	case sub.Status == statusPastDue && inv.Kind == invoiceKindActivation:
		if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
			return fmt.Errorf("billing.applyInvoicePayment: reactivate past-due activation: %w", err)
		}
		if err := s.enqueueAutoInvoice(ctx, sub); err != nil {
			return fmt.Errorf("billing.applyInvoicePayment: enqueue renewal auto-invoice: %w", err)
		}
	// An addon-increase invoice paid: fold the addon row's pending
	// quantity into its live quantity and clear both pending columns —
	// the mirror-image of applyScheduledAddonQuantityChange for the
	// increase (pay-first) direction instead of the decrease
	// (schedule-first) one. Nothing about the subscription's own
	// period/status changes here; this only ever affects one addon row.
	// No new event beyond InvoicePaid (outcome.paid, set above) — the
	// frontend already invalidates the addons query off other addon
	// mutations and wires the same invalidation to InvoicePaid.
	case inv.Kind == "addon_increase":
		return s.applyAddonIncreasePayment(ctx, inv, sub, paidBy)
	}
	return nil
}

// recoverLatePayment handles the recoverable case of a 'paid' webhook for
// an invoice that is no longer 'pending': a period-backing invoice
// ('subscription' or 'activation') closed without payment — 'failed' by
// expireIfDue when the subscription ended, or 'void' when a cancel request
// or a plan change superseded it — whose payment completed at the gateway
// anyway: paid before the close with the webhook delivered after it, or
// paid on a hosted checkout the provider still had open. When the
// subscription has ended ('expired', or 'cancelled' once its cancellation
// took effect) it flips the invoice to 'paid' and reports true, so
// handleWebhook's normal paid path treats the money like a Resume payment:
// a fresh period from now (applyExpiredReactivation). Every other shape
// reports false after a Warn log, since money arrived that nothing applies
// automatically: on a live subscription the closed invoice's replacement is
// already pending, an additive invoice (extension, addon increase) bought
// nothing an ended subscription can still use, and a deleted
// organization's subscription stays ended.
func (s *service) recoverLatePayment(ctx context.Context, invoiceID, subscriptionID string, now time.Time) (bool, error) {
	inv, err := s.repo.findInvoiceByID(ctx, s.querier(ctx), invoiceID)
	if err != nil {
		return false, fmt.Errorf("billing.recoverLatePayment: find invoice: %w", err)
	}
	sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), subscriptionID)
	if err != nil {
		return false, fmt.Errorf("billing.recoverLatePayment: find subscription: %w", err)
	}
	closed := inv.Status == statusFailed || inv.Status == statusVoid
	ended := sub.Status == statusExpired || sub.Status == statusCancelled
	recoverable := closed && ended && isPeriodBacking(inv.Kind)
	if recoverable {
		deleted, err := s.subjectDeleted(ctx, sub)
		if err != nil {
			return false, fmt.Errorf("billing.recoverLatePayment: %w", err)
		}
		recoverable = !deleted
	}
	if !recoverable {
		slog.Warn("billing.recoverLatePayment: paid webhook for a non-pending invoice not applied",
			"invoice_id", inv.ID, "invoice_status", inv.Status, "invoice_kind", inv.Kind,
			"subscription_id", sub.ID, "subscription_status", sub.Status)
		return false, nil
	}
	return s.repo.markClosedInvoicePaid(ctx, s.querier(ctx), invoiceID, now)
}

// subjectDeleted reports whether sub's organization was deleted — soft
// deleted, or already purged — so a late payment never brings its
// subscription back. Without an organization reader wired it reports false.
func (s *service) subjectDeleted(ctx context.Context, sub *subscriptionRecord) (bool, error) {
	if s.orgReader == nil || sub.SubjectType != subjectTypeOrganization {
		return false, nil
	}
	info, err := s.orgReader.GetOrganizationByID(ctx, sub.SubjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("billing.subjectDeleted: %w", err)
	}
	return info.Status == organizationStatusDeleted, nil
}

// applyExpiredReactivation reactivates a subscription that ended (expired,
// or cancelled once its cancellation took effect) and has now been paid:
// flips status back to active, starts a fresh period from now, clears any
// cancellation schedule, and records the resume. Mutates outcome in place —
// see handleWebhook's own comment on why a failure here must fail (and roll
// back) the whole transaction rather than be swallowed.
func (s *service) applyExpiredReactivation(
	ctx context.Context, outcome *webhookOutcome, link *paymentLinkSubject, sub *subscriptionRecord,
	changedBy string, now time.Time,
) error {
	periodEnd := now.AddDate(0, 1, 0)
	if sub.Cycle == cycleYearly {
		periodEnd = now.AddDate(1, 0, 0)
	}
	if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: reactivate subscription: %w", err)
	}
	if err := s.repo.clearTrialEndAndUpdatePeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: update period on reactivation: %w", err)
	}
	// A reactivated subscription renews normally: a cancellation schedule
	// left on it, whatever path cancelled it, would skip its next renewal
	// and cancel it again.
	if err := s.repo.clearScheduledCancellation(ctx, s.querier(ctx), sub.ID); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: clear cancellation schedule: %w", err)
	}
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
		&sub.Plan, &sub.Plan, 0, sub.Currency, changedBy, nil); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: insert resume history: %w", err)
	}
	// Suspension state lives in the organization module (a separate
	// schema/service, not this transaction) — a failure here is logged, not
	// rolled back; the paid reactivation itself already committed correctly
	// and shouldn't be undone over this. Only billing's own suspension
	// lifts: an owner's pause or a Studio operator's suspension stays.
	if s.orgSuspender != nil && sub.SubjectType == subjectTypeOrganization {
		if err := s.orgSuspender.UnsuspendOrganization(ctx, sub.SubjectID, billingSuspensionReasons); err != nil {
			slog.Error("UnsuspendOrganization failed", "organization_id", sub.SubjectID, "error", err)
		}
	}
	outcome.resumed = &events.SubscriptionResumed{OrgID: link.subjectID, SubscriptionID: sub.ID, Plan: sub.Plan, ResumedAt: now}
	outcome.scheduleRenewal = &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
	}
	return nil
}

// applyExtensionPayment applies a paid extension invoice: rolls the
// subscription's period forward by the invoice's total months (and converts
// its cycle to yearly, if requested) and records the extension. A past_due
// subscription returns to active: the failed renewal backed the period that
// started at the old period_end, which the extension now covers, so nothing
// is overdue — the renewal invoice stays pending and the auto-invoice for
// the new end offers it again. Mutates outcome in place.
func (s *service) applyExtensionPayment(
	ctx context.Context, outcome *webhookOutcome, link *paymentLinkSubject, inv *invoiceRecord, sub *subscriptionRecord,
	changedBy string,
) error {
	// inv.ExtensionMonths is the source of truth, set once by
	// extendSubscription at invoice creation — not re-derived by summing
	// every line item's Quantity: once addon lines (also quantified in
	// months, see insertExtensionLineItems) started sharing this invoice
	// with the plan's own block/remainder lines, summing every line item
	// would double-count months across them. Falls back to the old
	// summing behavior only for an invoice created before this field
	// existed (extension_months NULL, added in migration 000004).
	var months int
	if inv.ExtensionMonths != nil {
		months = *inv.ExtensionMonths
	} else {
		lineItems, err := s.repo.listLineItems(ctx, s.querier(ctx), inv.ID)
		if err != nil {
			return fmt.Errorf("billing.applyExtensionPayment: list extension invoice line items: %w", err)
		}
		for _, li := range lineItems {
			months += li.Quantity
		}
	}
	if months == 0 {
		months = 1
	}
	newPeriodEnd := sub.PeriodEnd.AddDate(0, months, 0)
	if inv.SwitchToAnnual {
		if err := s.repo.updateSubscriptionCycleAndPeriod(ctx, s.querier(ctx), sub.ID, cycleYearly, *sub.PeriodStart, newPeriodEnd); err != nil {
			return fmt.Errorf("billing.applyExtensionPayment: apply extension period and cycle: %w", err)
		}
	} else if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, newPeriodEnd); err != nil {
		return fmt.Errorf("billing.applyExtensionPayment: apply extension period: %w", err)
	}
	if sub.Status == statusPastDue {
		if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
			return fmt.Errorf("billing.applyExtensionPayment: reactivate past-due subscription: %w", err)
		}
	}
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "extend",
		&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedBy, nil); err != nil {
		return fmt.Errorf("billing.applyExtensionPayment: insert extend history: %w", err)
	}

	outcome.extended = &events.SubscriptionExtended{
		OrgID: link.subjectID, SubscriptionID: sub.ID, Plan: sub.Plan,
		Months: months, NewPeriodEnd: newPeriodEnd,
	}
	outcome.scheduleRenewal = &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType,
		SubjectID: sub.SubjectID, PeriodEnd: &newPeriodEnd,
	}
	return nil
}

// applyRenewalPayment applies a paid routine renewal invoice: rolls the
// subscription's period forward one cycle from its current period_end
// (never from now — the invoice is paid up to 3 days early, and starting
// the new period at payment time would shave those days off the customer's
// paid term), converting a trialing or past_due subscription back to active
// in the same move. Mutates outcome in place.
func (s *service) applyRenewalPayment(
	ctx context.Context, outcome *webhookOutcome,
	inv *invoiceRecord, sub *subscriptionRecord, changedBy string,
) error {
	periodEnd := sub.PeriodEnd.AddDate(0, 1, 0)
	if sub.Cycle == cycleYearly {
		periodEnd = sub.PeriodEnd.AddDate(1, 0, 0)
	}
	switch sub.Status {
	case statusTrialing:
		// Single write: converts the trial to active AND clears
		// trial_end — expireIfDue's trial_end check would otherwise
		// still fire at the original (now past) trial_end date and
		// undo this renewal on its very next run.
		if _, err := s.repo.activateTrialImmediately(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: convert trial to active on renewal: %w", err)
		}
	case statusPastDue:
		if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: roll over past-due subscription period: %w", err)
		}
		if err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: reactivate past-due subscription: %w", err)
		}
	default:
		if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: roll over subscription period: %w", err)
		}
	}
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "renew",
		&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedBy, nil); err != nil {
		return fmt.Errorf("billing.applyRenewalPayment: insert renew history: %w", err)
	}
	outcome.scheduleRenewal = &subscriptionRecord{
		ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
	}
	return nil
}

// applyAddonIncreasePayment applies a paid addon-increase invoice: folds the
// addon row's pending quantity into its live quantity and clears both
// pending columns. No outcome field to set beyond InvoicePaid (already
// assembled by the caller) — the frontend already invalidates the addons
// query off other addon mutations and wires the same invalidation to
// InvoicePaid.
func (s *service) applyAddonIncreasePayment(ctx context.Context, inv *invoiceRecord, sub *subscriptionRecord, changedBy string) error {
	addon, err := s.repo.findAddonByPendingInvoice(ctx, s.querier(ctx), inv.ID)
	if err != nil {
		return fmt.Errorf("billing.applyAddonIncreasePayment: find addon by pending invoice: %w", err)
	}
	if err := s.repo.applyPendingAddonIncrease(ctx, s.querier(ctx), addon.SubscriptionID, addon.AddonID); err != nil {
		return fmt.Errorf("billing.applyAddonIncreasePayment: apply pending addon increase: %w", err)
	}
	metadata, _ := json.Marshal(addonChangeMetadata{
		AddonID: addon.AddonID, FromQuantity: addon.Quantity, ToQuantity: addon.PendingQuantity,
	})
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "addon_change",
		nil, nil, inv.AmountCents, inv.Currency, changedBy, metadata); err != nil {
		return fmt.Errorf("billing.applyAddonIncreasePayment: insert addon_change history: %w", err)
	}
	return nil
}
