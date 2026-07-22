package billing

import (
	"context"
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
func (s *service) processWebhook(ctx context.Context, provider, eventID, externalID, normalizedStatus string) error {
	var outcome *webhookOutcome
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		txCtx := db.WithQuerier(ctx, tx)
		if eventID != "" {
			inserted, err := s.repo.markWebhookProcessed(txCtx, tx, provider, eventID)
			if err != nil {
				return err
			}
			if !inserted {
				return nil // duplicate delivery — no-op, ACK without re-running side effects
			}
		}
		var err error
		outcome, err = s.handleWebhook(txCtx, externalID, normalizedStatus)
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
// transaction has committed.
func (s *service) handleWebhook(ctx context.Context, externalID, normalizedStatus string) (*webhookOutcome, error) {
	link, err := s.repo.findPaymentLinkWithSubjectByExternalID(ctx, s.querier(ctx), externalID)
	if err != nil {
		return nil, err
	}
	if link.status == normalizedStatus {
		return nil, nil
	}

	if err := s.repo.updatePaymentLinkStatus(ctx, s.querier(ctx), link.linkID, normalizedStatus); err != nil {
		return nil, err
	}

	outcome := &webhookOutcome{}

	if normalizedStatus == statusPaid {
		now := time.Now()
		if err := s.repo.markInvoicePaid(ctx, s.querier(ctx), link.invoiceID, now); err != nil {
			return nil, err
		}

		pl, _ := s.repo.findPaymentLinkByExternalID(ctx, s.querier(ctx), externalID)
		if pl != nil {
			_ = s.repo.insertPayment(ctx, s.querier(ctx), link.invoiceID, pl.AmountCents, pl.Currency, pl.Provider, pl.ExternalID, now)
		}

		paidEvt := events.InvoicePaid{OrgID: link.subjectID, InvoiceID: link.invoiceID, PaidAt: now}
		if pl != nil {
			paidEvt.AmountCents = int(pl.AmountCents)
			paidEvt.Currency = pl.Currency
		}
		outcome.paid = &paidEvt

		// Reactivate expired subscriptions on successful payment
		sub, err := s.repo.findSubscriptionByID(ctx, s.querier(ctx), link.subscriptionID)
		if err == nil && sub.Status == statusExpired {
			periodEnd := now.AddDate(0, 1, 0)
			if sub.Cycle == cycleYearly {
				periodEnd = now.AddDate(1, 0, 0)
			}
			_, _ = s.repo.updateSubscriptionStatus(ctx, s.querier(ctx), sub.ID, statusActive)
			_ = s.repo.updateSubscriptionPeriod(ctx, s.querier(ctx), sub.ID, now, periodEnd)
			_ = s.repo.insertHistory(ctx, s.querier(ctx), sub.ID, "resume",
				&sub.Plan, &sub.Plan, 0, sub.Currency, changedByWebhook, nil)
			if s.orgSuspender != nil && sub.SubjectType == "organization" {
				_ = s.orgSuspender.UnsuspendOrganization(ctx, sub.SubjectID)
			}
			outcome.resumed = &events.SubscriptionResumed{OrgID: link.subjectID, SubscriptionID: sub.ID, Plan: sub.Plan, ResumedAt: now}
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
