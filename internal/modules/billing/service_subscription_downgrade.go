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

func (s *service) downgradeTargetLimits(ctx context.Context, sub *subscriptionRecord, newPlanInfo *contracts.PlanInfo) (int, int64) {
	memberLimit := -1
	if v, ok := newPlanInfo.Limits["members"]; ok {
		memberLimit = v
	}
	storageLimit := int64(-1)
	if v, ok := newPlanInfo.Limits["storage"]; ok {
		storageLimit = int64(v)
	}

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
