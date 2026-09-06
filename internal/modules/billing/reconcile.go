package billing

import (
	"context"
	"log/slog"

	"github.com/aasumitro/stratum/internal/contracts/events"
)

// ReconcileOverdueSubscriptions is an hourly safety net for subscription
// expiry. expireIfDue's only trigger is the SubscriptionCheck scheduled at
// period/trial end; if that one message is lost or misrouted, the
// subscription stays active past its paid period with nothing else in the
// system noticing. This re-enqueues a SubscriptionCheck for every
// active/trialing subscription already past its end date, so the normal
// HandleSubscriptionCheck -> expireIfDue path runs and (idempotently)
// expires it and suspends its organization.
//
// Scoped to expiry only: it does not backfill a missed renewal reminder or
// auto-invoice — a missed reminder is cosmetic, and a missed auto-invoice
// resolves to "the subscription lapses unpaid", which this sweep then
// expires correctly. Fire-and-forget, matching the worker's other hourly
// jobs: an enqueue failure is logged, never propagated.
func (m *Module) ReconcileOverdueSubscriptions(ctx context.Context) {
	m.svc.reconcileOverdueSubscriptions(ctx)
}

func (s *service) reconcileOverdueSubscriptions(ctx context.Context) {
	overdue, err := s.repo.listOverdueSubscriptions(ctx, s.pool)
	if err != nil {
		slog.Error("billing.reconcileOverdueSubscriptions: list overdue subscriptions failed", "error", err)
		return
	}
	for _, sub := range overdue {
		// Enqueued with the subscription's current end date so the consumer's
		// stale-check guard still discards it if the period just moved. Each
		// Enqueue is its own implicitly-committed insert on the pool (no
		// ambient transaction here) — one failure doesn't hold up the rest.
		if err := s.enqueueEvent(ctx, events.RoutingKeySubscriptionCheck, sub.SubjectID, events.SubscriptionCheck{
			SubscriptionID: sub.ID,
			SubjectType:    sub.SubjectType,
			SubjectID:      sub.SubjectID,
			ExpectedEnd:    sub.ExpectedEnd,
			IsTrial:        sub.IsTrial,
		}); err != nil {
			slog.Error("billing.reconcileOverdueSubscriptions: enqueue SubscriptionCheck failed",
				"subscription_id", sub.ID, "error", err)
		}
	}
}
