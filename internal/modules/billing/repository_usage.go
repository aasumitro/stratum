package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type usageRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Metric         string    `json:"metric"`
	Value          int64     `json:"value"`
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	RecordedAt     time.Time `json:"recorded_at"`
}

func (r *repository) upsertUsage(
	ctx context.Context, q db.Querier,
	organizationID, metric string, value int64, periodStart, periodEnd time.Time,
) error {
	_, err := q.Exec(ctx, `
		INSERT INTO billing.usage (organization_id, metric, value, period_start, period_end, recorded_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (organization_id, metric, period_start, period_end)
		DO UPDATE SET value = $3, recorded_at = now()`,
		organizationID, metric, value, periodStart, periodEnd,
	)
	if err != nil {
		return fmt.Errorf("billing.upsertUsage: %w", err)
	}
	return nil
}

func (r *repository) listCurrentUsage(
	ctx context.Context, q db.Querier, organizationID string,
) ([]usageRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (metric) id, organization_id, metric, value, period_start, period_end, recorded_at
		FROM billing.usage
		WHERE organization_id = $1
		ORDER BY metric, period_end DESC`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.listCurrentUsage: %w", err)
	}
	defer rows.Close()

	var out []usageRecord
	for rows.Next() {
		var u usageRecord
		if err := rows.Scan(&u.ID, &u.OrganizationID, &u.Metric, &u.Value, &u.PeriodStart, &u.PeriodEnd, &u.RecordedAt); err != nil {
			return nil, fmt.Errorf("billing.listCurrentUsage: scan: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listCurrentUsage: %w", err)
	}
	return out, nil
}

func (r *repository) getCurrentUsage(
	ctx context.Context, q db.Querier, organizationID, metric string,
) (*usageRecord, error) {
	var u usageRecord
	err := q.QueryRow(ctx, `
		SELECT id, organization_id, metric, value, period_start, period_end, recorded_at
		FROM billing.usage
		WHERE organization_id = $1 AND metric = $2
		ORDER BY period_end DESC LIMIT 1`,
		organizationID, metric,
	).Scan(&u.ID, &u.OrganizationID, &u.Metric, &u.Value, &u.PeriodStart, &u.PeriodEnd, &u.RecordedAt)
	if err != nil {
		return nil, fmt.Errorf("billing.getCurrentUsage: %w", err)
	}
	return &u, nil
}

// addonLimitDeltas sums billing.addon_features.limit_value (× the addon's
// attached quantity) per feature for every addon currently attached to
// subscriptionID via billing.subscription_addons. These deltas are additive
// on top of whatever the subscription's plan already grants — a metered
// feature's effective limit is plan_limit + delta.
// Config-only addon_features rows (limit_value IS NULL) are excluded; the
// schema gives addons no config_value column, only numeric limits.
func (r *repository) addonLimitDeltas(ctx context.Context, q db.Querier, subscriptionID string) (map[string]int, error) {
	rows, err := q.Query(ctx, `
		SELECT af.feature_id, SUM(af.limit_value * sa.quantity)
		FROM billing.subscription_addons sa
		JOIN billing.addon_features af ON af.addon_id = sa.addon_id
		WHERE sa.subscription_id = $1 AND af.limit_value IS NOT NULL
		GROUP BY af.feature_id`, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("billing.addonLimitDeltas: %w", err)
	}
	defer rows.Close()

	deltas := map[string]int{}
	for rows.Next() {
		var featureID string
		var delta int64
		if err := rows.Scan(&featureID, &delta); err != nil {
			return nil, fmt.Errorf("billing.addonLimitDeltas: scan: %w", err)
		}
		deltas[featureID] = int(delta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.addonLimitDeltas: %w", err)
	}
	return deltas, nil
}
