package billing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// webhookOutcome collects the events/scheduling a webhook triggers while its
// DB effects run inside processWebhook's transaction. Publishing is deferred
// until after that transaction commits — publishing before commit would
// announce state that might still roll back.
type webhookOutcome struct {
	paid            *events.InvoicePaid
	resumed         *events.SubscriptionResumed
	extended        *events.SubscriptionExtended
	failed          *events.InvoiceFailed
	scheduleRenewal *subscriptionRecord
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
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
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
		return err
	})
	if err != nil {
		return err
	}
	if outcome == nil {
		return nil
	}

	if outcome.paid != nil {
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoicePaid, "billing", outcome.paid.OrgID, *outcome.paid)
	}
	if outcome.resumed != nil {
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeySubscriptionResumed, "billing", outcome.resumed.OrgID, *outcome.resumed)
	}
	if outcome.extended != nil {
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeySubscriptionExtended, "billing", outcome.extended.OrgID, *outcome.extended)
	}
	if outcome.failed != nil {
		events.Publish(ctx, s.pub, events.ExchangeBilling, events.RoutingKeyInvoiceFailed, "billing", outcome.failed.OrgID, *outcome.failed)
		events.PublishDelayed(ctx, s.pub, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionPaymentRemind, "billing", outcome.failed.OrgID, *outcome.failed, 3*24*time.Hour)
		events.PublishDelayed(ctx, s.pub, events.ExchangeBillingDelay, events.DelayRoutingKeySubscriptionPaymentFinal, "billing", outcome.failed.OrgID, *outcome.failed, 7*24*time.Hour)
	}
	if outcome.scheduleRenewal != nil {
		s.scheduleRenewalSequence(ctx, outcome.scheduleRenewal)
	}
	return nil
}

// handleWebhook applies a provider callback's DB effects using ctx's
// transaction-scoped querier, returning the side effects to run once that
// transaction has committed. invoiceID, when non-empty, resolves the
// payment link by invoice instead of externalID — see processWebhook.
func (s *service) handleWebhook(ctx context.Context, externalID, invoiceID, normalizedStatus string) (*webhookOutcome, error) {
	var link *paymentLinkSubject
	var err error
	if invoiceID != "" {
		link, err = s.repo.findPaymentLinkWithSubjectByInvoiceID(ctx, s.querier(ctx), invoiceID)
	} else {
		link, err = s.repo.findPaymentLinkWithSubjectByExternalID(ctx, s.querier(ctx), externalID)
	}
	if err != nil {
		return nil, fmt.Errorf("billing.handleWebhook: find payment link with subject: %w", err)
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

		// Lock before the invoice's own paid/pending check so two concurrent
		// deliveries for the same invoice (a provider retry, or two distinct
		// event types reporting the same payment, each with its own event ID
		// and so not caught by the eventID-based check above) serialize here
		// instead of both reading "pending" and both applying its effects.
		if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), link.subscriptionID); err != nil {
			return nil, fmt.Errorf("billing.handleWebhook: lock subscription: %w", err)
		}

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
			if err := s.repo.insertPayment(ctx, s.querier(ctx), link.invoiceID, pl.AmountCents, pl.Currency, pl.Provider, pl.ExternalID, now); err != nil {
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
			periodEnd := now.AddDate(0, 1, 0)
			if sub.Cycle == cycleYearly {
				periodEnd = now.AddDate(1, 0, 0)
			}
			if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: reactivate subscription: %w", err)
			}
			if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: update period on reactivation: %w", err)
			}
			if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
				&sub.Plan, &sub.Plan, 0, sub.Currency, changedBySystem, nil); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: insert resume history: %w", err)
			}
			// Suspension state lives in the organization module (a separate
			// schema/service, not this transaction) — a failure here is
			// logged, not rolled back; the paid reactivation itself already
			// committed correctly and shouldn't be undone over this.
			if s.orgSuspender != nil && sub.SubjectType == subjectTypeOrganization {
				if err := s.orgSuspender.UnsuspendOrganization(ctx, sub.SubjectID); err != nil {
					slog.Error("UnsuspendOrganization failed", "organization_id", sub.SubjectID, "error", err)
				}
			}
			outcome.resumed = &events.SubscriptionResumed{OrgID: link.subjectID, SubscriptionID: sub.ID, Plan: sub.Plan, ResumedAt: now}
			outcome.scheduleRenewal = &subscriptionRecord{
				ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
			}
		case sub.Status == statusActive && inv.Kind == "extension":
			lineItems, err := s.repo.listLineItems(ctx, s.querier(ctx), inv.ID)
			if err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: list extension invoice line items: %w", err)
			}
			// Summed across every line item's quantity, not read off a single
			// fixed index — a tiered extension purchase splits into up to two
			// line items (a 12-month-block line and a monthly-remainder line,
			// see insertExtensionLineItems in service_subscription_billing.go),
			// each carrying its own share of the total months in its Quantity.
			months := 0
			for _, li := range lineItems {
				months += li.Quantity
			}
			if months == 0 {
				months = 1
			}
			newPeriodEnd := sub.PeriodEnd.AddDate(0, months, 0)
			if inv.SwitchToAnnual {
				if err := s.repo.updateSubscriptionCycleAndPeriod(ctx, s.querier(ctx), sub.ID, cycleYearly, *sub.PeriodStart, newPeriodEnd); err != nil {
					return nil, fmt.Errorf("billing.handleWebhook: apply extension period and cycle: %w", err)
				}
			} else if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, newPeriodEnd); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: apply extension period: %w", err)
			}
			if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "extend",
				&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedByWebhook, nil); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: insert extend history: %w", err)
			}

			outcome.extended = &events.SubscriptionExtended{
				OrgID: link.subjectID, SubscriptionID: sub.ID, Plan: sub.Plan,
				Months: months, NewPeriodEnd: newPeriodEnd,
			}
			outcome.scheduleRenewal = &subscriptionRecord{
				ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &newPeriodEnd,
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
			// Extends from the current period_end, not from now — the
			// invoice is paid up to 3 days early, and starting the new
			// period at payment time would shave those days off the
			// customer's paid term.
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
					return nil, fmt.Errorf("billing.handleWebhook: convert trial to active on renewal: %w", err)
				}
			case statusPastDue:
				if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
					return nil, fmt.Errorf("billing.handleWebhook: roll over past-due subscription period: %w", err)
				}
				if _, err := s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive); err != nil {
					return nil, fmt.Errorf("billing.handleWebhook: reactivate past-due subscription: %w", err)
				}
			default:
				if err := s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, *sub.PeriodStart, periodEnd); err != nil {
					return nil, fmt.Errorf("billing.handleWebhook: roll over subscription period: %w", err)
				}
			}
			if _, err := s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "renew",
				&sub.Plan, &sub.Plan, inv.AmountCents, inv.Currency, changedByWebhook, nil); err != nil {
				return nil, fmt.Errorf("billing.handleWebhook: insert renew history: %w", err)
			}
			outcome.scheduleRenewal = &subscriptionRecord{
				ID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID, PeriodEnd: &periodEnd,
			}
		}
	}

	if normalizedStatus == statusFailed || normalizedStatus == statusExpired {
		outcome.failed = &events.InvoiceFailed{OrgID: link.subjectID, InvoiceID: link.invoiceID, FailedAt: time.Now()}
	}

	// Mark subscription past_due on a confirmed payment failure so callers can
	// gate access without waiting for the subscription-check to fire at period_end.
	if normalizedStatus == statusFailed {
		sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), link.subscriptionID)
		if err == nil && sub.Status == statusActive {
			_, _ = s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusPastDue)
		}
	}

	return outcome, nil
}
