package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts/events"
	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// recordUsage may be called either under an ambient transaction (the HTTP
// route, wrapped by the organization RLS middleware) or from a background
// goroutine with a stripped context and no org identity set anywhere
// (organization.syncMemberUsage's fire-and-forget member-count sync). Only
// the latter needs its own transaction — reusing an already-active one would
// nest db.WithTx and double-apply SetOrgContext for no reason. db.HasQuerier
// distinguishes the two: true inside the RLS middleware's transaction, false
// for a bare background context, so recordUsageTx always runs against a
// Querier with org context already set, whichever branch got it there.
func (s *service) recordUsage(ctx context.Context, organizationID, metric string, value int64) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("USAGE_RECORD_FAILED", "failed to record usage", err)
		}
	}()

	if db.HasQuerier(ctx) {
		return s.recordUsageTx(ctx, organizationID, metric, value)
	}
	return s.withOrgTx(ctx, subjectTypeOrganization, organizationID, func(tx db.Querier) error {
		return s.recordUsageTx(db.WithQuerier(ctx, tx), organizationID, metric, value)
	})
}

// Bare repo-call returns below are deliberate — same funnel-into-one-
// deferred-apperr-classification tradeoff as redeemCoupon (service_coupon.go).
func (s *service) recordUsageTx(ctx context.Context, organizationID, metric string, value int64) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return err
	}
	periodStart := sub.CreatedAt
	periodEnd := sub.CreatedAt.AddDate(0, 1, 0)
	if sub.PeriodStart != nil {
		periodStart = *sub.PeriodStart
	}
	if sub.PeriodEnd != nil {
		periodEnd = *sub.PeriodEnd
	}

	var previous int64
	if usage, uErr := s.repo.getCurrentUsage(ctx, s.querier(ctx), organizationID, metric); uErr == nil {
		previous = usage.Value
	}

	if err := s.repo.upsertUsage(ctx, s.querier(ctx), organizationID, metric, value, periodStart, periodEnd); err != nil {
		return err
	}

	return s.maybeWarnUsageLimit(ctx, organizationID, metric, previous, value)
}

// maybeWarnUsageLimit enqueues UsageLimitWarning the moment usage crosses
// 90% of the effective limit (plan + any attached addon delta), checked
// against previous so it fires exactly once per crossing rather than on
// every subsequent recordUsage call once already over the threshold. A
// lookup failure here still shouldn't fail the usage write that already
// succeeded (same fail-open convention as the rest of this gate) — but once
// a crossing is confirmed, the enqueue's own error must propagate per
// events.Enqueue's contract.
func (s *service) maybeWarnUsageLimit(ctx context.Context, organizationID, metric string, previous, current int64) error {
	_, limit, err := s.checkUsageLimit(ctx, organizationID, metric)
	if err != nil || limit < 0 {
		return nil
	}
	if !crossedUsageThreshold(previous, current, limit) {
		return nil
	}
	return s.enqueueEvent(ctx, events.RoutingKeyUsageLimitWarning, organizationID,
		events.UsageLimitWarning{OrgID: organizationID, Metric: metric, Current: current, Limit: limit})
}

// crossedUsageThreshold reports whether this usage write newly crossed 90%
// of limit — true only when previous was below the threshold and current
// is at or above it, so a warning fires exactly once per crossing rather
// than on every subsequent write once already over the line. limit < 0
// (unlimited) is the caller's responsibility to filter out beforehand.
func crossedUsageThreshold(previous, current int64, limit int) bool {
	threshold := int64(limit) * 9 / 10
	return previous < threshold && current >= threshold
}

func (s *service) getUsage(ctx context.Context, _, subjectID string) ([]usageRecord, error) {
	usage, err := s.repo.listCurrentUsage(ctx, s.querier(ctx), subjectID)
	if err != nil {
		return nil, apperr.Internal("USAGE_FETCH_FAILED", "failed to get usage", err)
	}
	return usage, nil
}

// checkUsageLimit may be called either under an ambient transaction (a
// billing-route caller, already RLS-wrapped) or with no querier in context
// at all (organization's invitation/member paths) — same split recordUsage
// already uses (above) for identical reasons: billing.subscriptions has
// FORCE ROW LEVEL SECURITY, so a bare-pool read with no app.organization_id
// set is silently filtered to zero rows.
func (s *service) checkUsageLimit(ctx context.Context, organizationID, metric string) (current int64, limit int, err error) {
	if db.HasQuerier(ctx) {
		return s.checkUsageLimitTx(ctx, organizationID, metric)
	}
	err = s.withOrgTx(ctx, subjectTypeOrganization, organizationID, func(tx db.Querier) error {
		var txErr error
		current, limit, txErr = s.checkUsageLimitTx(db.WithQuerier(ctx, tx), organizationID, metric)
		return txErr
	})
	return current, limit, err
}

// checkUsageLimitTx is checkUsageLimit's body, unchanged.
func (s *service) checkUsageLimitTx(ctx context.Context, organizationID, metric string) (current int64, limit int, err error) {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return 0, 0, fmt.Errorf("billing.checkUsageLimit: %w", err)
	}
	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return 0, 0, fmt.Errorf("billing.checkUsageLimit: %w", err)
	}
	limit = -1
	if v, ok := planInfo.Limits[metric]; ok {
		limit = v
	}
	// Attached addons grant additive limit deltas on top of the plan (e.g.
	// "+5 members") — only relevant for a finite limit; an already-unlimited
	// plan has nothing to add to. Fail open on lookup error, same convention
	// as the rest of this gate: a Redis/Postgres blip shouldn't block usage.
	if limit >= 0 {
		if deltas, dErr := s.repo.addonLimitDeltas(ctx, s.querier(ctx), sub.ID); dErr == nil {
			limit += deltas[metric]
		}
	}

	usage, err := s.repo.getCurrentUsage(ctx, s.querier(ctx), organizationID, metric)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, limit, nil
		}
		return 0, limit, fmt.Errorf("billing.checkUsageLimit: %w", err)
	}
	return usage.Value, limit, nil
}

// checkFeatureAccess is plan-only, not addon-aware — today's seeded addons
// only ever grant additive numeric limits, never a presence-only
// boolean/static entitlement, so there's nothing for an addon to unlock
// here the way checkUsageLimit folds in addon limit deltas. Revisit if a
// future addon is meant to grant a boolean/static feature.
//
// May be called either under an ambient transaction (a billing-route
// caller, already RLS-wrapped) or with no querier in context at all
// (organization's webhook path) — same split recordUsage/checkUsageLimit
// already use, for identical reasons.
func (s *service) checkFeatureAccess(ctx context.Context, organizationID, feature string) error {
	if db.HasQuerier(ctx) {
		return s.checkFeatureAccessTx(ctx, organizationID, feature)
	}
	return s.withOrgTx(ctx, subjectTypeOrganization, organizationID, func(tx db.Querier) error {
		return s.checkFeatureAccessTx(db.WithQuerier(ctx, tx), organizationID, feature)
	})
}

// checkFeatureAccessTx is checkFeatureAccess's body, unchanged.
func (s *service) checkFeatureAccessTx(ctx context.Context, organizationID, feature string) error {
	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return fmt.Errorf("billing.checkFeatureAccess: %w", err)
	}
	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return fmt.Errorf("billing.checkFeatureAccess: %w", err)
	}
	if slices.Contains(planInfo.Features, feature) {
		return nil
	}
	return ErrFeatureNotAvailable
}

// entitlementRecord is the fully resolved per-feature view for an
// organization: catalog metadata (name/type) plus whatever combination of
// limit/current/remaining/config_value applies, given the organization's
// plan and any addons attached to its subscription. Only features the plan
// actually grants are included — billing.features is a shared catalog and
// most plans only use a subset of it.
type entitlementRecord struct {
	FeatureID   string          `json:"feature_id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Limit       int             `json:"limit,omitempty"`        // metered only; -1 = unlimited; PlanLimit + AddonDelta
	PlanLimit   int             `json:"plan_limit,omitempty"`   // metered only; the plan's own limit before addons
	AddonDelta  int             `json:"addon_delta,omitempty"`  // metered only; sum of attached-addon deltas ("6/8 · 3 plan + 5 addon")
	Current     int64           `json:"current,omitempty"`      // metered only
	Remaining   int64           `json:"remaining,omitempty"`    // metered only; -1 = unlimited
	ConfigValue json.RawMessage `json:"config_value,omitempty"` // config only
}

// Bare repo-call returns below are deliberate — same funnel-into-one-
// deferred-apperr-classification tradeoff as redeemCoupon (service_coupon.go).
func (s *service) resolveEntitlements(ctx context.Context, organizationID string) (out []entitlementRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		out = nil
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("NOT_FOUND", "subscription not found", err)
			return
		}
		err = apperr.Internal("FEATURES_FETCH_FAILED", "failed to get features", err)
	}()

	sub, err := s.repo.findSubscriptionBySubject(ctx, s.querier(ctx), subjectTypeOrganization, organizationID)
	if err != nil {
		return nil, err
	}
	planInfo, err := s.planCatalog(ctx, sub.Plan)
	if err != nil {
		return nil, err
	}
	features, err := s.featureCatalog(ctx)
	if err != nil {
		return nil, err
	}

	// Fail open on addon lookup — same convention as checkUsageLimit.
	deltas, _ := s.repo.addonLimitDeltas(ctx, s.querier(ctx), sub.ID)

	out = make([]entitlementRecord, 0, len(features))
	for _, f := range features {
		rec := entitlementRecord{FeatureID: f.ID, Name: f.Name, Type: f.Type}

		switch f.Type {
		case "metered":
			planLimit, ok := planInfo.Limits[f.ID]
			if !ok {
				continue // plan doesn't grant this metered feature at all
			}
			limit := planLimit
			delta := deltas[f.ID]
			if limit >= 0 {
				limit += delta
			}
			var current int64
			if f.MetricKey != "" {
				if usage, uErr := s.repo.getCurrentUsage(ctx, s.querier(ctx), organizationID, f.MetricKey); uErr == nil {
					current = usage.Value
				}
			}
			rec.Limit = limit
			rec.PlanLimit = planLimit
			rec.AddonDelta = delta
			rec.Current = current
			rec.Remaining = -1
			if limit >= 0 {
				rec.Remaining = int64(limit) - current
			}

		case "config":
			raw, ok := planInfo.ConfigValues[f.ID]
			if !ok {
				continue // plan doesn't grant this config feature
			}
			rec.ConfigValue = raw

		default: // "boolean", "static" — presence-only
			if !slices.Contains(planInfo.Features, f.ID) {
				continue
			}
		}

		out = append(out, rec)
	}
	return out, nil
}
