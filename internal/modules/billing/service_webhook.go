package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
		if err := events.EnqueueDelayed(ctx, tx, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionPaymentRemind,
			"billing", outcome.failed.OrgID, *outcome.failed, 3*24*time.Hour); err != nil {
			return err
		}
		if err := events.EnqueueDelayed(ctx, tx, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionPaymentFinal,
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
			// Already paid by an earlier, now-committed delivery — nothing left to do.
			return outcome, nil
		}

		// Both calls run under s.querier(ctx) — the same DB transaction as
		// every other write in this branch. Postgres aborts an entire
		// transaction on the first failed statement, so a swallowed error here
		// wouldn't actually let the transaction proceed. Propagate instead.
		pl, err := s.repo.findPaymentLinkByExternalID(ctx, s.querier(ctx), externalID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find payment link: %w", err)
		}
		if pl != nil {
			if err := s.repo.insertPayment(
				ctx, s.querier(ctx), link.invoiceID, pl.AmountCents,
				pl.Currency, pl.Provider, pl.ExternalID, now,
			); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: insert payment: %w", err)
			}
		}

		paidEvt := events.InvoicePaid{OrgID: link.subjectID, InvoiceID: link.invoiceID, PaidAt: now}
		if pl != nil {
			paidEvt.AmountCents = int(pl.AmountCents)
			paidEvt.Currency = pl.Currency
		}
		outcome.paid = &paidEvt

		inv, err := s.repo.findInvoiceByID(ctx, s.querier(ctx), link.invoiceID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find invoice: %w", err)
		}

		// Reactivate expired subscriptions on successful payment. Below this
		// point every write applies what the customer just paid for — a
		// failure must fail (and roll back) the whole transaction rather
		// than be swallowed, or the invoice ends up marked paid with the
		// subscription silently never actually reactivated/extended.
		sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), link.subscriptionID)
		if err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: find subscription: %w", err)
		}
		switch {
		case sub.Status == statusExpired:
			if err := s.applyExpiredReactivation(ctx, outcome, link, sub, now); err != nil {
				return nil, err
			}
		case sub.Status == statusActive && inv.Kind == "extension":
			if err := s.applyExtensionPayment(ctx, outcome, link, inv, sub); err != nil {
				return nil, err
			}
		// Three invoice kinds reach this switch: "activation" (a
		// subscription's very first invoice, or a skip-trial activation —
		// its period was already fully set at creation/activation time, so
		// paying it is correctly a no-op here — falls through to no case),
		// "extension" (handled by its own case above), and "subscription"
		// (the routine renewal invoice HandleSubscriptionAutoInvoice creates
		// 3 days before period_end — the only kind this case should roll
		// forward). Without this case matching a genuine renewal,
		// period_end never advances: expireIfDue only checks whether "now"
		// is past the *existing* period_end, with no awareness that a
		// renewal was paid, so an on-time payer got suspended on schedule
		// anyway.
		case (sub.Status == statusActive || sub.Status == statusTrialing || sub.Status == statusPastDue) &&
			inv.Kind == "subscription":
			if err := s.applyRenewalPayment(ctx, outcome, inv, sub); err != nil {
				return nil, err
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
			if err := s.applyAddonIncreasePayment(ctx, inv, sub); err != nil {
				return nil, err
			}
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
		if sub.Status == statusActive && (inv.Kind == "subscription" || inv.Kind == "activation") {
			if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusPastDue); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: mark subscription past due: %w", err)
			}
		}
	}

	return outcome, nil
}

// applyExpiredReactivation reactivates a subscription that lapsed (expired)
// and has now been paid: flips status back to active, starts a fresh period
// from now, and records the resume. Mutates outcome in place — see
// handleWebhook's own comment on why a failure here must fail (and roll
// back) the whole transaction rather than be swallowed.
func (s *service) applyExpiredReactivation(
	ctx context.Context, outcome *webhookOutcome, link *paymentLinkSubject, sub *subscriptionRecord, now time.Time,
) error {
	periodEnd := now.AddDate(0, 1, 0)
	if sub.Cycle == cycleYearly {
		periodEnd = now.AddDate(1, 0, 0)
	}
	if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: reactivate subscription: %w", err)
	}
	if err := s.repo.clearTrialEndAndUpdatePeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: update period on reactivation: %w", err)
	}
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
		&sub.Plan, &sub.Plan, 0, sub.Currency, changedBySystem, nil); err != nil {
		return fmt.Errorf("billing.applyExpiredReactivation: insert resume history: %w", err)
	}
	// Suspension state lives in the organization module (a separate
	// schema/service, not this transaction) — a failure here is logged, not
	// rolled back; the paid reactivation itself already committed correctly
	// and shouldn't be undone over this.
	if s.orgSuspender != nil && sub.SubjectType == subjectTypeOrganization {
		if err := s.orgSuspender.UnsuspendOrganization(ctx, sub.SubjectID); err != nil {
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
// its cycle to yearly, if requested) and records the extension. Mutates
// outcome in place.
func (s *service) applyExtensionPayment(
	ctx context.Context, outcome *webhookOutcome, link *paymentLinkSubject, inv *invoiceRecord, sub *subscriptionRecord,
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
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "extend",
		&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedByWebhook, nil); err != nil {
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
	inv *invoiceRecord, sub *subscriptionRecord,
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
		if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: reactivate past-due subscription: %w", err)
		}
	default:
		if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
			return fmt.Errorf("billing.applyRenewalPayment: roll over subscription period: %w", err)
		}
	}
	if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "renew",
		&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedByWebhook, nil); err != nil {
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
func (s *service) applyAddonIncreasePayment(ctx context.Context, inv *invoiceRecord, sub *subscriptionRecord) error {
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
		nil, nil, inv.AmountCents, inv.Currency, changedByWebhook, metadata); err != nil {
		return fmt.Errorf("billing.applyAddonIncreasePayment: insert addon_change history: %w", err)
	}
	return nil
}
