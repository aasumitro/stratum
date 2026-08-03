package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/apperr"
)

// downgradeSubscription processes a plan downgrade, enforcing deterministic
// overage resolution to keep the organization within the new plan's limits.
// The returned OverageResolution is what the caller (the HTTP handler) surfaces
// to the frontend so the success screen can show exactly what was removed and
// whether each item was owner-selected or auto-selected — it's the same value
// already persisted to subscription_history.metadata below, just also handed
// back on the response instead of only being visible via the audit trail.
func (s *service) downgradeSubscription(
	ctx context.Context, subjectType, subjectID, plan, cycle, changedBy string,
	removeMemberIDs []string, removeFileIDs []string,
) (*subscriptionRecord, contracts.OverageResolution, error) {
	if subjectType != subjectTypeOrganization {
		return nil, contracts.OverageResolution{}, apperr.Validation("DOWNGRADE_UNSUPPORTED", "downgrade is only supported for organizations")
	}

	newPlanInfo, err := s.planCatalog(ctx, plan)
	if err != nil {
		return nil, contracts.OverageResolution{}, ErrUnknownPlan
	}

	sub, err := s.getSubscription(ctx, subjectType, subjectID)
	if err != nil {
		return nil, contracts.OverageResolution{}, err
	}

	// The HTTP layer (RLS middleware) wraps this entire method in a transaction.
	// Locking the row serializes concurrent downgrade requests for this org.
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, contracts.OverageResolution{}, fmt.Errorf("billing.downgradeSubscription: %w", err)
	}

	// Validate it is actually a downgrade
	alreadyOnTarget := sub.Plan == plan && sub.Cycle == cycle
	if !alreadyOnTarget {
		oldPlanInfo, err := s.planCatalog(ctx, sub.Plan)
		if err != nil {
			return nil, contracts.OverageResolution{}, fmt.Errorf("billing.downgradeSubscription: %w: current plan", ErrUnknownPlan)
		}
		if newPlanInfo.SortOrder >= oldPlanInfo.SortOrder {
			return nil, contracts.OverageResolution{}, apperr.Validation("INVALID_DOWNGRADE", "target plan is not a downgrade")
		}
	}

	// Trialing has no paid period to protect, so a downgrade applies
	// immediately. Every other status defers to renewal instead:
	// scheduled_plan/cycle are written here, but the live plan/cycle (and
	// any overage they'd force) stay untouched until the renewal worker
	// applies the schedule — never reduce entitlement mid-period.
	if sub.Status != statusTrialing {
		if sub.ScheduledCancelAt != nil {
			return nil, contracts.OverageResolution{}, apperr.Validation(
				"CANCELLATION_SCHEDULED", "subscription is scheduled to cancel; undo that first")
		}
		if err := s.repo.schedulePlanDowngrade(ctx, s.querier(ctx), sub.ID, plan, cycle); err != nil {
			return nil, contracts.OverageResolution{}, fmt.Errorf("billing.downgradeSubscription: %w", err)
		}
		metadata, _ := json.Marshal(struct {
			ToPlan  string `json:"to_plan"`
			ToCycle string `json:"to_cycle"`
		}{ToPlan: plan, ToCycle: cycle})
		phase := historyPhaseScheduled
		if _, err := s.repo.insertHistoryWithPhase(ctx, s.querier(ctx), sub.ID, actionDowngrade,
			&sub.Plan, &plan, 0, sub.Currency, changedBy, metadata, &phase, sub.PeriodEnd); err != nil {
			return nil, contracts.OverageResolution{}, fmt.Errorf("billing.downgradeSubscription: %w", err)
		}
		// Re-fetch so the caller sees the schedule it just wrote, not the
		// pre-write snapshot taken before the lock.
		updated, err := s.getSubscription(ctx, subjectType, subjectID)
		if err != nil {
			return nil, contracts.OverageResolution{}, err
		}
		return updated, contracts.OverageResolution{}, nil
	}

	var historyID string
	if !alreadyOnTarget {
		historyID, sub, err = s.changePlanWithMetadata(ctx, subjectType, subjectID, plan, cycle, changedBy, nil)
		if err != nil {
			return nil, contracts.OverageResolution{}, err
		}
	}

	var res contracts.OverageResolution
	var metadata []byte
	if s.orgCommander != nil {
		memberLimit, storageLimit := s.downgradeTargetLimits(ctx, sub, newPlanInfo)
		// Execute overage resolution in the organization module. This executes
		// via its own autonomous transaction but won't deadlock with our row lock.
		res, err = s.orgCommander.ResolveDowngradeOverage(ctx, subjectID,
			removeMemberIDs, memberLimit,
			removeFileIDs, storageLimit, false)
		if err != nil {
			return nil, contracts.OverageResolution{}, fmt.Errorf("billing.downgradeSubscription: resolve overage: %w", err)
		}
		metadata, _ = json.Marshal(res)
	}

	if historyID != "" && metadata != nil {
		if err := s.repo.updateHistoryMetadata(ctx, s.querier(ctx), historyID, metadata); err != nil {
			slog.Error("downgradeSubscription: failed to update history metadata",
				"history_id", historyID, "organization_id", subjectID, "error", err)
		}
	}

	return sub, res, nil
}

// undoScheduledPlanDowngrade clears a scheduled plan downgrade before it
// ever takes effect. No history row is written — the "scheduled" row
// simply never gets a matching "applied" one, which is itself the record
// that it was undone, visible by its absence at renewal.
func (s *service) undoScheduledPlanDowngrade(
	ctx context.Context, subjectType, subjectID string,
) (*subscriptionRecord, error) {
	sub, err := s.getSubscription(ctx, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.lockSubscriptionForUpdate(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, fmt.Errorf("billing.undoScheduledPlanDowngrade: %w", err)
	}
	if sub.ScheduledPlan == nil {
		return nil, apperr.Validation("NO_SCHEDULED_DOWNGRADE", "no scheduled plan downgrade to undo")
	}
	if err := s.repo.clearScheduledPlanDowngrade(ctx, s.querier(ctx), sub.ID); err != nil {
		return nil, fmt.Errorf("billing.undoScheduledPlanDowngrade: %w", err)
	}
	return s.getSubscription(ctx, subjectType, subjectID)
}

// planLimits extracts a plan's raw member/storage limits (-1 meaning
// unlimited) — the base every per-subscription limit calculation starts
// from before adding any addon-derived delta on top.
func planLimits(planInfo *contracts.PlanInfo) (memberLimit int, storageLimit int64) {
	memberLimit = -1
	if v, ok := planInfo.Limits["members"]; ok {
		memberLimit = v
	}
	storageLimit = -1
	if v, ok := planInfo.Limits["storage"]; ok {
		storageLimit = int64(v)
	}
	return memberLimit, storageLimit
}

func (s *service) downgradeTargetLimits(ctx context.Context, sub *subscriptionRecord, newPlanInfo *contracts.PlanInfo) (int, int64) {
	memberLimit, storageLimit := planLimits(newPlanInfo)

	if memberLimit >= 0 || storageLimit >= 0 {
		if deltas, dErr := s.repo.addonLimitDeltas(ctx, s.querier(ctx), sub.ID); dErr == nil {
			if memberLimit >= 0 {
				memberLimit += deltas["members"]
			}
			if storageLimit >= 0 {
				storageLimit += int64(deltas["storage"])
			}
		}
	}
	return memberLimit, storageLimit
}

// futureOverage is what resolveFutureOveragePreview computes: a
// subscription's member/storage limits once every currently-scheduled
// amendment applies, alongside its current usage — enough for a caller to
// decide whether applying those amendments would put the organization over
// either limit.
type futureOverage struct {
	MemberLimit    int
	StorageLimit   int64
	CurrentMembers int64
	CurrentStorage int64
}

// resolveFutureOveragePreview computes sub's future member/storage limits —
// its scheduled_plan if set, else its current plan, plus every attached
// addon's scheduled_quantity if set, else its live quantity — against its
// current usage. It never calls OrganizationCommander.ResolveDowngradeOverage
// itself; the caller decides what to do once it knows whether the future
// state would be over either limit. Shared by the renewal worker (applies
// the scheduled amendments for real) and the billing preview endpoint
// (read-only), so this future-state math exists in exactly one place.
func (s *service) resolveFutureOveragePreview(ctx context.Context, sub *subscriptionRecord) (*futureOverage, error) {
	futurePlan := sub.Plan
	if sub.ScheduledPlan != nil {
		futurePlan = *sub.ScheduledPlan
	}
	planInfo, err := s.planCatalog(ctx, futurePlan)
	if err != nil {
		return nil, fmt.Errorf("billing.resolveFutureOveragePreview: %w", err)
	}
	memberLimit, storageLimit := planLimits(planInfo)

	scheduledAddons, err := s.repo.listScheduledAddonChanges(ctx, s.querier(ctx), sub.ID)
	if err != nil {
		return nil, fmt.Errorf("billing.resolveFutureOveragePreview: %w", err)
	}
	overrides := make(map[string]int, len(scheduledAddons))
	for _, a := range scheduledAddons {
		overrides[a.AddonID] = a.ScheduledQuantity
	}
	futureDeltas, err := s.repo.futureAddonLimitDeltas(ctx, s.querier(ctx), sub.ID, overrides)
	if err != nil {
		return nil, fmt.Errorf("billing.resolveFutureOveragePreview: %w", err)
	}
	if memberLimit >= 0 {
		memberLimit += futureDeltas["members"]
	}
	if storageLimit >= 0 {
		storageLimit += int64(futureDeltas["storage"])
	}

	usages, err := s.repo.listCurrentUsage(ctx, s.querier(ctx), sub.SubjectID)
	if err != nil {
		return nil, fmt.Errorf("billing.resolveFutureOveragePreview: %w", err)
	}
	var currentMembers, currentStorage int64
	for _, u := range usages {
		switch u.Metric {
		case "members":
			currentMembers = u.Value
		case "storage_bytes":
			currentStorage = u.Value
		}
	}
	return &futureOverage{
		MemberLimit: memberLimit, StorageLimit: storageLimit,
		CurrentMembers: currentMembers, CurrentStorage: currentStorage,
	}, nil
}
