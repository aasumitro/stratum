package billing

import (
	"context"
	"log/slog"

	"github.com/aasumitro/stratum/internal/contracts/events"
)

// ReconcileOverdueSubscriptions is an hourly safety net for the two one-shot
// messages a period boundary depends on.
//
// The end of a subscription: expireIfDue's only trigger is the
// SubscriptionCheck scheduled at period/trial end; if that one message is lost
// or misrouted, the subscription stays live past its paid period with nothing
// else in the system noticing. This re-enqueues a SubscriptionCheck for every
// active/trialing/past_due subscription already past its end date, so the
// normal HandleSubscriptionCheck -> expireIfDue path runs and (idempotently)
// ends it and suspends its organization.
//
// A scheduled downgrade or add-on decrease: applying it at its boundary rides
// on one apply-scheduled message. Once that message dead-letters, only the next
// renewal payment would queue it again, a full period later, and until then
// the schedule stays locked: the owner keeps the higher plan at the lower price
// and cannot change, extend or undo anything. This re-enqueues the apply for
// every schedule that is due (listDueSchedules); applyScheduledAtBoundary
// re-checks it under the row lock, so a duplicate does nothing. Each re-send
// doubles the wait before the next (markScheduleResent: 1h, 2h, ... at most a
// day), so a schedule that fails on every attempt dead-letters at most once a
// day after its first day rather than every hour, and still applies on its own
// within about a day of the cause being fixed. Writing, clearing, applying or
// moving the schedule resets the wait. The dead-letter queue gauge surfaces it
// either way.
//
// It does not backfill a missed renewal reminder or auto-invoice: a missed
// reminder is cosmetic, and a missed auto-invoice resolves to "the
// subscription lapses unpaid", which this sweep then expires correctly.
// Fire-and-forget, matching the worker's other hourly jobs: an enqueue failure
// is logged, never propagated.
func (m *Module) ReconcileOverdueSubscriptions(ctx context.Context) {
	m.svc.reconcileOverdueSubscriptions(ctx)
}

func (s *service) reconcileOverdueSubscriptions(ctx context.Context) {
	overdue, err := s.repo.listOverdueSubscriptions(ctx, s.pool)
	if err != nil {
		slog.Error("billing.reconcileOverdueSubscriptions: list overdue subscriptions failed", "error", err)
	}
	for _, sub := range overdue {
		// expireIfDue re-checks the live end under its row lock, so a message
		// for a period that has just moved on is a no-op. Each Enqueue is its
		// own implicitly-committed insert on the pool (no ambient transaction
		// here) — one failure doesn't hold up the rest.
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

	due, err := s.repo.listDueSchedules(ctx, s.pool)
	if err != nil {
		slog.Error("billing.reconcileOverdueSubscriptions: list due schedules failed", "error", err)
		return
	}
	for _, sub := range due {
		if err := s.enqueueApplyScheduled(ctx, &sub); err != nil {
			slog.Error("billing.reconcileOverdueSubscriptions: enqueue apply-scheduled failed",
				"subscription_id", sub.ID, "error", err)
			continue
		}
		// Unrecorded, the next tick re-sends one hour later as before.
		if err := s.repo.markScheduleResent(ctx, s.pool, sub.ID, *sub.ScheduledEffectiveAt); err != nil {
			slog.Error("billing.reconcileOverdueSubscriptions: record apply-scheduled re-send failed",
				"subscription_id", sub.ID, "error", err)
		}
	}
}
