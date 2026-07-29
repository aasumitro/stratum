package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
)

// --- catalog (plans, features, addons) ---
//
// billing owns billing.plans/features/addons/plan_features/addon_features —
// moved here from the reference module, which used to query these tables
// directly across a schema boundary it didn't own. None of these tables
// have RLS enabled, so every method below runs against the shared pool,
// same as the reference module used before.

type planRecord struct {
	ID           string                          `json:"id"`
	Name         string                          `json:"name"`
	Description  string                          `json:"description"`
	Prices       map[string]contracts.PlanPrices `json:"prices"`
	Limits       map[string]int                  `json:"limits"`
	Features     []string                        `json:"features"`
	SortOrder    int                             `json:"sort_order"`
	ConfigValues map[string]json.RawMessage      `json:"config_values"`
	Active       bool                            `json:"active"`
	CreatedAt    time.Time                       `json:"created_at"`
	UpdatedAt    time.Time                       `json:"updated_at"`
}

type featureRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	MetricKey   *string   `json:"metric_key,omitempty"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
}

type addonRecord struct {
	ID          string                          `json:"id"`
	Name        string                          `json:"name"`
	Description string                          `json:"description"`
	Prices      map[string]contracts.PlanPrices `json:"prices"`
	Features    map[string]int                  `json:"features"` // feature_id -> limit_value delta
	Active      bool                            `json:"active"`
	CreatedAt   time.Time                       `json:"created_at"`
}

// loadPlanFeatures reconstructs a plan's Limits/Features/ConfigValues
// projection from billing.plan_features: a row with a non-null limit_value
// is a numeric cap (Limits[feature_id] = limit_value); a row with a non-null
// config_value is a config-type entitlement (ConfigValues[feature_id] = raw
// JSON, e.g. api_rate_limit/audit_retention_days); a row with neither is a
// presence-only entitlement (boolean/static feature types) and lands in
// Features by feature ID.
func (r *repository) loadPlanFeatures(
	ctx context.Context, q db.Querier, planID string,
) (limits map[string]int, features []string, configValues map[string]json.RawMessage, err error) {
	rows, err := q.Query(ctx, `
		SELECT feature_id, limit_value, config_value
		FROM billing.plan_features
		WHERE plan_id = $1`, planID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("billing.loadPlanFeatures: %w", err)
	}

	limits = map[string]int{}
	features = []string{}
	configValues = map[string]json.RawMessage{}
	for rows.Next() {
		var featureID string
		var limitValue *int64
		var configValue []byte
		if err := rows.Scan(&featureID, &limitValue, &configValue); err != nil {
			rows.Close()
			return nil, nil, nil, fmt.Errorf("billing.loadPlanFeatures: scan: %w", err)
		}
		switch {
		case limitValue != nil:
			limits[featureID] = int(*limitValue)
		case configValue != nil:
			configValues[featureID] = configValue
		default:
			features = append(features, featureID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("billing.loadPlanFeatures: %w", err)
	}
	return limits, features, configValues, nil
}

// loadAddonFeatures returns feature_id -> limit_value delta for an addon
// (billing.addon_features has no config_value column — addons only ever
// contribute additive numeric limits, never config entitlements).
func (r *repository) loadAddonFeatures(
	ctx context.Context, q db.Querier, addonID string,
) (map[string]int, error) {
	rows, err := q.Query(ctx, `
		SELECT feature_id, limit_value
		FROM billing.addon_features
		WHERE addon_id = $1`, addonID)
	if err != nil {
		return nil, fmt.Errorf("billing.loadAddonFeatures: %w", err)
	}
	defer rows.Close()

	features := map[string]int{}
	for rows.Next() {
		var featureID string
		var limitValue *int64
		if err := rows.Scan(&featureID, &limitValue); err != nil {
			return nil, fmt.Errorf("billing.loadAddonFeatures: scan: %w", err)
		}
		if limitValue != nil {
			features[featureID] = int(*limitValue)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.loadAddonFeatures: %w", err)
	}
	return features, nil
}

// planFeatureSet is one plan's resolved Limits/Features/ConfigValues —
// the per-plan_id grouping of loadPlanFeaturesBatch's rows.
type planFeatureSet struct {
	Limits       map[string]int
	Features     []string
	ConfigValues map[string]json.RawMessage
}

// loadPlanFeaturesBatch is the batched counterpart to loadPlanFeatures —
// resolves every plan's features in one query instead of one query per
// plan (the N+1 listPlans used to have), grouped by plan_id in Go. Same
// aggregation shape as addonLimitDeltas elsewhere in this file.
func (r *repository) loadPlanFeaturesBatch(
	ctx context.Context, q db.Querier, planIDs []string,
) (map[string]planFeatureSet, error) {
	rows, err := q.Query(ctx, `
		SELECT plan_id, feature_id, limit_value, config_value
		FROM billing.plan_features
		WHERE plan_id = ANY($1)`, planIDs)
	if err != nil {
		return nil, fmt.Errorf("billing.loadPlanFeaturesBatch: %w", err)
	}
	defer rows.Close()

	out := make(map[string]planFeatureSet, len(planIDs))
	for rows.Next() {
		var planID, featureID string
		var limitValue *int64
		var configValue []byte
		if err := rows.Scan(&planID, &featureID, &limitValue, &configValue); err != nil {
			return nil, fmt.Errorf("billing.loadPlanFeaturesBatch: scan: %w", err)
		}
		set, ok := out[planID]
		if !ok {
			set = planFeatureSet{Limits: map[string]int{}, Features: []string{}, ConfigValues: map[string]json.RawMessage{}}
		}
		switch {
		case limitValue != nil:
			set.Limits[featureID] = int(*limitValue)
		case configValue != nil:
			set.ConfigValues[featureID] = configValue
		default:
			set.Features = append(set.Features, featureID)
		}
		out[planID] = set
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.loadPlanFeaturesBatch: %w", err)
	}
	return out, nil
}

// loadAddonFeaturesBatch is the batched counterpart to loadAddonFeatures —
// resolves every addon's feature deltas in one query, grouped by addon_id.
func (r *repository) loadAddonFeaturesBatch(
	ctx context.Context, q db.Querier, addonIDs []string,
) (map[string]map[string]int, error) {
	rows, err := q.Query(ctx, `
		SELECT addon_id, feature_id, limit_value
		FROM billing.addon_features
		WHERE addon_id = ANY($1)`, addonIDs)
	if err != nil {
		return nil, fmt.Errorf("billing.loadAddonFeaturesBatch: %w", err)
	}
	defer rows.Close()

	out := make(map[string]map[string]int, len(addonIDs))
	for rows.Next() {
		var addonID, featureID string
		var limitValue *int64
		if err := rows.Scan(&addonID, &featureID, &limitValue); err != nil {
			return nil, fmt.Errorf("billing.loadAddonFeaturesBatch: scan: %w", err)
		}
		if limitValue == nil {
			continue
		}
		if out[addonID] == nil {
			out[addonID] = map[string]int{}
		}
		out[addonID][featureID] = int(*limitValue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.loadAddonFeaturesBatch: %w", err)
	}
	return out, nil
}

func (r *repository) listPlans(ctx context.Context, q db.Querier) ([]planRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, description, prices, sort_order, active, created_at, updated_at
		FROM billing.plans
		WHERE active = true
		ORDER BY sort_order`)
	if err != nil {
		return nil, fmt.Errorf("billing.listPlans: %w", err)
	}

	var out []planRecord
	for rows.Next() {
		var p planRecord
		var pricesJSON []byte
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &pricesJSON,
			&p.SortOrder, &p.Active, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("billing.listPlans: scan: %w", err)
		}
		_ = json.Unmarshal(pricesJSON, &p.Prices)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listPlans: %w", err)
	}
	if len(out) == 0 {
		return out, nil
	}

	planIDs := make([]string, len(out))
	for i := range out {
		planIDs[i] = out[i].ID
	}
	featureSets, err := r.loadPlanFeaturesBatch(ctx, q, planIDs)
	if err != nil {
		return nil, fmt.Errorf("billing.listPlans: %w", err)
	}
	for i := range out {
		set, ok := featureSets[out[i].ID]
		if !ok {
			set = planFeatureSet{Limits: map[string]int{}, Features: []string{}, ConfigValues: map[string]json.RawMessage{}}
		}
		out[i].Limits = set.Limits
		out[i].Features = set.Features
		out[i].ConfigValues = set.ConfigValues
	}
	return out, nil
}

func (r *repository) findPlanByID(ctx context.Context, q db.Querier, id string) (*planRecord, error) {
	var p planRecord
	var pricesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT id, name, description, prices, sort_order, active, created_at, updated_at
		FROM billing.plans
		WHERE id = $1 AND active = true`, id,
	).Scan(
		&p.ID, &p.Name, &p.Description, &pricesJSON,
		&p.SortOrder, &p.Active, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("billing.findPlanByID: %w", err)
	}
	_ = json.Unmarshal(pricesJSON, &p.Prices)

	limits, features, configValues, err := r.loadPlanFeatures(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("billing.findPlanByID: %w", err)
	}
	p.Limits = limits
	p.Features = features
	p.ConfigValues = configValues
	return &p, nil
}

func (r *repository) listFeatures(ctx context.Context, q db.Querier) ([]featureRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, description, type, metric_key, active, created_at
		FROM billing.features
		WHERE active = true
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("billing.listFeatures: %w", err)
	}
	defer rows.Close()

	var out []featureRecord
	for rows.Next() {
		var f featureRecord
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.Type, &f.MetricKey, &f.Active, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("billing.listFeatures: scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listFeatures: %w", err)
	}
	return out, nil
}

func (r *repository) listAddons(ctx context.Context, q db.Querier) ([]addonRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, description, prices, active, created_at
		FROM billing.addons
		WHERE active = true
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("billing.listAddons: %w", err)
	}

	var out []addonRecord
	for rows.Next() {
		var a addonRecord
		var pricesJSON []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &pricesJSON, &a.Active, &a.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("billing.listAddons: scan: %w", err)
		}
		_ = json.Unmarshal(pricesJSON, &a.Prices)
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing.listAddons: %w", err)
	}
	if len(out) == 0 {
		return out, nil
	}

	addonIDs := make([]string, len(out))
	for i := range out {
		addonIDs[i] = out[i].ID
	}
	featureSets, err := r.loadAddonFeaturesBatch(ctx, q, addonIDs)
	if err != nil {
		return nil, fmt.Errorf("billing.listAddons: %w", err)
	}
	for i := range out {
		if fs, ok := featureSets[out[i].ID]; ok {
			out[i].Features = fs
		} else {
			out[i].Features = map[string]int{}
		}
	}
	return out, nil
}

func (r *repository) findAddonByID(ctx context.Context, q db.Querier, id string) (*addonRecord, error) {
	var a addonRecord
	var pricesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT id, name, description, prices, active, created_at
		FROM billing.addons
		WHERE id = $1 AND active = true`, id,
	).Scan(&a.ID, &a.Name, &a.Description, &pricesJSON, &a.Active, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("billing.findAddonByID: %w", err)
	}
	_ = json.Unmarshal(pricesJSON, &a.Prices)

	features, err := r.loadAddonFeatures(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("billing.findAddonByID: %w", err)
	}
	a.Features = features
	return &a, nil
}
