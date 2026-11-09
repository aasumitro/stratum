package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// A scheduled plan downgrade or add-on decrease takes effect at the period
// end it was scheduled against (scheduled_effective_at). The renewal for the
// next period is priced from it (futurePricing), and it is applied at that
// boundary once the renewal is paid, or when the subscription expires there.

const (
	amendmentAlreadyPaidCode = "AMENDMENT_ALREADY_PAID"
	amendmentAlreadyPaidMsg  = "a scheduled change is already paid for and takes effect at the end of the current period; change it after that"
)

// scheduleLocked reports whether sub's schedule was already paid for: the
// renewal priced from it moved period_end past the boundary. The owner paid
// the scheduled price for the coming period, so the schedule can no longer
// change until it applies.
func scheduleLocked(sub *subscriptionRecord) bool {
	return sub.ScheduledEffectiveAt != nil && sub.PeriodEnd != nil && sub.PeriodEnd.After(*sub.ScheduledEffectiveAt)
}

func errAmendmentAlreadyPaid() error {
	return apperr.Validation(amendmentAlreadyPaidCode, amendmentAlreadyPaidMsg)
}

// afterScheduleChange runs, in the caller's transaction under the subscription
// lock, after a schedule was written or cleared. It keeps the effective date in
// line with what is scheduled, queues applying it at that date, and reissues a
// pending renewal, which was priced from the schedule as it stood.
func (s *service) afterScheduleChange(ctx context.Context, sub *subscriptionRecord) error {
	q := s.querier(ctx)
	effective, err := s.repo.syncScheduledEffectiveAt(ctx, q, sub.ID)
	if err != nil {
		return err
	}
	sub.ScheduledEffectiveAt = effective
	if err := s.enqueueApplyScheduled(ctx, sub); err != nil {
		return err
	}
	fresh, err := s.repo.findSubscriptionByID(ctx, q, sub.ID)
	if err != nil {
		return fmt.Errorf("billing.afterScheduleChange: %w", err)
	}
	return s.reissuePendingRenewal(ctx, fresh)
}

// enqueueApplyScheduled queues applying sub's schedule at its effective date,
// or in a second once that has passed. Delivery checks the live state, so a
// message left over from an undone or moved schedule does nothing.
func (s *service) enqueueApplyScheduled(ctx context.Context, sub *subscriptionRecord) error {
	if sub.ScheduledEffectiveAt == nil {
		return nil
	}
	check := events.SubscriptionCheck{
		SubscriptionID: sub.ID, SubjectType: sub.SubjectType, SubjectID: sub.SubjectID,
		ExpectedEnd: *sub.ScheduledEffectiveAt,
	}
	delay := max(time.Until(*sub.ScheduledEffectiveAt), time.Second)
	if err := events.EnqueueDelayed(ctx, s.querier(ctx), events.ExchangeBilling,
		events.RoutingKeySubscriptionApplyScheduled, "billing", sub.SubjectID, check, delay); err != nil {
		return fmt.Errorf("billing.enqueueApplyScheduled: %w", err)
	}
	return nil
}

// reissuePendingRenewal voids a pending renewal and issues it again, priced from
// what is scheduled now. The amount changed and the old pay link is dead, so the
// owner is told.
func (s *service) reissuePendingRenewal(ctx context.Context, sub *subscriptionRecord) error {
	pending, err := s.repo.pendingInvoiceKinds(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return fmt.Errorf("billing.reissuePendingRenewal: %w", err)
	}
	if !pending[invoiceKindSubscription] {
		return nil
	}
	if err := s.voidSuperseded(ctx, sub, invoiceKindSubscription); err != nil {
		return err
	}
	return s.issueRenewalAndNotify(ctx, sub)
}

// issueRenewalAndNotify issues sub's renewal (priced by issuePeriodInvoice) and
// tells the owner, when there is something to pay.
func (s *service) issueRenewalAndNotify(ctx context.Context, sub *subscriptionRecord) error {
	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return fmt.Errorf("billing.issueRenewalAndNotify: %w", err)
	}
	inv, err := s.issuePeriodInvoice(ctx, sub, planInfo, invoiceKindSubscription)
	if err != nil || inv == nil {
		return err
	}
	plan := sub.Plan
	if sub.ScheduledPlan != nil {
		plan = *sub.ScheduledPlan
	}
	return events.Enqueue(ctx, s.querier(ctx), events.ExchangeBilling, events.RoutingKeyInvoiceCreated, "billing", sub.SubjectID,
		events.InvoiceCreated{
			OrgID: sub.SubjectID, InvoiceID: inv.ID,
			Plan: plan, AmountCents: inv.AmountCents, Currency: inv.Currency, DueAt: *inv.DueAt,
			FromTrial: sub.Status == statusTrialing,
		})
}

// scheduleDue reports whether sub's schedule applies now, for the boundary
// check names: the renewal priced from it is paid (period_end moved past the
// boundary), or the subscription expired there. Too early returns false with
// early set, so the caller can queue it again.
func scheduleDue(sub *subscriptionRecord, check events.SubscriptionCheck, now time.Time) (due, early bool) {
	if sub.ScheduledEffectiveAt == nil ||
		!sub.ScheduledEffectiveAt.Truncate(time.Microsecond).Equal(check.ExpectedEnd.Truncate(time.Microsecond)) {
		return false, false
	}
	switch sub.Status {
	case statusActive, statusPastDue:
		if !scheduleLocked(sub) {
			// Unpaid at the boundary: the expiry ends the subscription and
			// queues this again. Paid late: the payment queues it again.
			return false, false
		}
	case statusExpired:
	default:
		return false, false
	}
	if now.Before(*sub.ScheduledEffectiveAt) {
		return false, true
	}
	return true, false
}

// applyScheduledAtBoundary applies sub's schedule once it is due. Member
// overage is resolved first, by the organization module in a transaction of its
// own, never while billing holds one. A failure after it retries, and resolving
// again removes nobody once the count fits.
func (s *service) applyScheduledAtBoundary(ctx context.Context, check events.SubscriptionCheck) error {
	var due bool
	var subjectID string
	memberLimit, over := -1, false
	err := db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		sub, err := s.lockedSubscription(ctx, check.SubscriptionID)
		if err != nil || sub == nil {
			return err
		}
		var early bool
		if due, early = scheduleDue(sub, check, time.Now()); early {
			return s.enqueueApplyScheduled(ctx, sub)
		}
		if !due || s.orgCommander == nil {
			return nil
		}
		overage, err := s.resolveFutureOveragePreview(ctx, sub)
		if err != nil {
			return err
		}
		subjectID = sub.SubjectID
		memberLimit = overage.MemberLimit
		over = overage.MemberLimit >= 0 && overage.CurrentMembers > int64(overage.MemberLimit)
		return nil
	})
	if err != nil || !due {
		return err
	}
	if over {
		if _, err := s.orgCommander.ResolveDowngradeOverage(ctx, subjectID, nil, memberLimit, false); err != nil {
			return fmt.Errorf("billing.applyScheduledAtBoundary: resolve overage: %w", err)
		}
	}
	return db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		ctx := db.WithQuerier(ctx, tx)
		sub, err := s.lockedSubscription(ctx, check.SubscriptionID)
		if err != nil || sub == nil {
			return err
		}
		if due, _ := scheduleDue(sub, check, time.Now()); !due {
			return nil
		}
		return s.applySchedule(ctx, sub)
	})
}

// lockedSubscription locks and reads a subscription in ctx's transaction; nil
// when it no longer exists.
func (s *service) lockedSubscription(ctx context.Context, subscriptionID string) (*subscriptionRecord, error) {
	q := s.querier(ctx)
	if err := s.repo.lockSubscriptionForUpdate(ctx, q, subscriptionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sub, err := s.repo.findSubscriptionByID(ctx, q, subscriptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sub, err
}

// applySchedule moves the scheduled plan, cycle and add-on quantities into the
// live ones, with a history row each, and clears the schedule. Runs in the
// caller's transaction under the subscription lock.
func (s *service) applySchedule(ctx context.Context, sub *subscriptionRecord) error {
	q := s.querier(ctx)
	scheduledAddons, err := s.repo.listScheduledAddonChanges(ctx, q, sub.ID)
	if err != nil {
		return fmt.Errorf("billing.applySchedule: %w", err)
	}
	now := time.Now()
	if sub.ScheduledPlan != nil {
		if _, err := s.repo.applyScheduledPlanDowngrade(ctx, q, sub.ID); err != nil {
			return fmt.Errorf("billing.applySchedule: %w", err)
		}
		phase := historyPhaseApplied
		if _, err := s.repo.insertHistoryWithCycle(ctx, q, sub.ID, actionDowngrade,
			&sub.Plan, sub.ScheduledPlan, 0, sub.Currency, changedBySystem, nil, &phase, &now,
			&sub.Cycle, sub.ScheduledCycle); err != nil {
			return fmt.Errorf("billing.applySchedule: %w", err)
		}
	}
	for _, a := range scheduledAddons {
		if a.ScheduledQuantity == 0 {
			if err := s.repo.deleteSubscriptionAddon(ctx, q, sub.ID, a.AddonID); err != nil {
				return fmt.Errorf("billing.applySchedule: %w", err)
			}
		} else if err := s.repo.applyScheduledAddonQuantityChange(ctx, q, sub.ID, a.AddonID, a.ScheduledQuantity); err != nil {
			return fmt.Errorf("billing.applySchedule: %w", err)
		}
		metadata, _ := json.Marshal(addonChangeMetadata{
			AddonID: a.AddonID, FromQuantity: a.LiveQuantity, ToQuantity: a.ScheduledQuantity,
		})
		phase := historyPhaseApplied
		if _, err := s.repo.insertHistoryWithPhase(ctx, q, sub.ID, "addon_change",
			nil, nil, 0, sub.Currency, changedBySystem, metadata, &phase, &now); err != nil {
			return fmt.Errorf("billing.applySchedule: %w", err)
		}
	}
	_, err = s.repo.syncScheduledEffectiveAt(ctx, q, sub.ID)
	return err
}

// followPeriodEnd moves a waiting schedule's boundary to newEnd when period_end
// moves for a reason that was not priced from the schedule (an extension, the
// cycle an activation pays for, a prorated plan change). A schedule already
// paid for keeps its boundary.
func (s *service) followPeriodEnd(ctx context.Context, sub *subscriptionRecord, newEnd time.Time) error {
	if sub.ScheduledEffectiveAt == nil || scheduleLocked(sub) || sub.ScheduledEffectiveAt.Equal(newEnd) {
		return nil
	}
	if err := s.repo.moveScheduledEffectiveAt(ctx, s.querier(ctx), sub.ID, newEnd); err != nil {
		return err
	}
	moved := *sub
	moved.ScheduledEffectiveAt = &newEnd
	return s.enqueueApplyScheduled(ctx, &moved)
}
