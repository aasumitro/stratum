package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Plan struct {
	ID          string
	Name        string
	Description string
	Prices      []byte
	SortOrder   int
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Feature struct {
	ID          string
	Name        string
	Description string
	Type        string
	MetricKey   *string
	Active      bool
	CreatedAt   time.Time
}

type PlanFeature struct {
	PlanID      string
	FeatureID   string
	LimitValue  *int64
	ConfigValue []byte
}

type Coupon struct {
	Code           string
	Name           string
	DiscountType   string
	AmountCents    *int64
	PercentOff     *int
	Currency       *string
	Cadence        string
	DurationCount  *int
	ValidFrom      *time.Time
	ValidUntil     *time.Time
	MaxRedemptions *int
	RedeemedCount  int
	Metadata       []byte
	Active         bool
	CreatedAt      time.Time
}

type CouponTarget struct {
	CouponID    string
	SubjectType string
	SubjectID   string
	CreatedAt   time.Time
}

type Addon struct {
	ID          string
	Name        string
	Description string
	Prices      []byte
	Active      bool
	CreatedAt   time.Time
}

type AddonFeature struct {
	AddonID    string
	FeatureID  string
	LimitValue *int64
}

// --- Plans ---

func ListPlans(ctx context.Context, pool *pgxpool.Pool) ([]Plan, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, description, prices, sort_order, active, created_at, updated_at
		FROM billing.plans
		ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListPlans: %w", err)
	}
	defer rows.Close()

	var plans []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.Prices,
			&p.SortOrder, &p.Active, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("query.ListPlans: scan: %w", err)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func CreatePlan(ctx context.Context, pool *pgxpool.Pool,
	id, name, description string, prices []byte, sortOrder int, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.plans (id, name, description, prices, sort_order, active)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, name, description, prices, sortOrder, active)
	if err != nil {
		return fmt.Errorf("query.CreatePlan: %w", err)
	}
	return nil
}

func UpdatePlan(ctx context.Context, pool *pgxpool.Pool,
	id, name, description string, prices []byte, sortOrder int, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.plans
		SET name=$2, description=$3, prices=$4, sort_order=$5, active=$6, updated_at=now()
		WHERE id=$1
	`, id, name, description, prices, sortOrder, active)
	if err != nil {
		return fmt.Errorf("query.UpdatePlan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdatePlan: plan not found")
	}
	return nil
}

func DeletePlan(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM billing.plans
		WHERE id = $1
		  AND NOT EXISTS (SELECT 1 FROM billing.subscriptions WHERE plan = $1)
		  AND NOT EXISTS (SELECT 1 FROM billing.plan_features WHERE plan_id = $1)
	`, id)
	if err != nil {
		return fmt.Errorf("query.DeletePlan: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	// Blocked or not found — a follow-up read only, never gates the delete itself.
	var subCount, featureCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM billing.subscriptions WHERE plan = $1),
		       (SELECT COUNT(*) FROM billing.plan_features WHERE plan_id = $1)
	`, id).Scan(&subCount, &featureCount); err != nil {
		return fmt.Errorf("query.DeletePlan: %w", err)
	}
	if subCount > 0 {
		return fmt.Errorf("plan %q has %d subscription(s) referencing it — deactivate it instead of deleting", id, subCount)
	}
	if featureCount > 0 {
		return fmt.Errorf("plan %q has %d entitlement(s) — remove them first (Manage Entitlements)", id, featureCount)
	}
	return fmt.Errorf("query.DeletePlan: plan not found")
}

// --- Features ---

func ListFeatures(ctx context.Context, pool *pgxpool.Pool) ([]Feature, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, description, type, metric_key, active, created_at
		FROM billing.features
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListFeatures: %w", err)
	}
	defer rows.Close()

	var out []Feature
	for rows.Next() {
		var f Feature
		if err := rows.Scan(&f.ID, &f.Name, &f.Description, &f.Type, &f.MetricKey, &f.Active, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("query.ListFeatures: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, nil
}

func CreateFeature(ctx context.Context, pool *pgxpool.Pool,
	id, name, description, featureType string, metricKey *string, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.features (id, name, description, type, metric_key, active)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, name, description, featureType, metricKey, active)
	if err != nil {
		return fmt.Errorf("query.CreateFeature: %w", err)
	}
	return nil
}

func UpdateFeature(ctx context.Context, pool *pgxpool.Pool,
	id, name, description, featureType string, metricKey *string, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.features
		SET name=$2, description=$3, type=$4, metric_key=$5, active=$6
		WHERE id=$1
	`, id, name, description, featureType, metricKey, active)
	if err != nil {
		return fmt.Errorf("query.UpdateFeature: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdateFeature: feature not found")
	}
	return nil
}

func DeleteFeature(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM billing.features
		WHERE id = $1
		  AND NOT EXISTS (SELECT 1 FROM billing.plan_features WHERE feature_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM billing.addon_features WHERE feature_id = $1)
	`, id)
	if err != nil {
		return fmt.Errorf("query.DeleteFeature: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var planCount, addonCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM billing.plan_features WHERE feature_id = $1),
		       (SELECT COUNT(*) FROM billing.addon_features WHERE feature_id = $1)
	`, id).Scan(&planCount, &addonCount); err != nil {
		return fmt.Errorf("query.DeleteFeature: %w", err)
	}
	if planCount > 0 || addonCount > 0 {
		return fmt.Errorf("feature %q is used by %d plan(s) and %d addon(s) — remove it from those entitlements first", id, planCount, addonCount)
	}
	return fmt.Errorf("query.DeleteFeature: feature not found")
}

// --- Plan Features (entitlement management) ---

func ListPlanFeatures(ctx context.Context, pool *pgxpool.Pool, planID string) ([]PlanFeature, error) {
	rows, err := pool.Query(ctx, `
		SELECT plan_id, feature_id, limit_value, config_value
		FROM billing.plan_features
		WHERE plan_id = $1
		ORDER BY feature_id
	`, planID)
	if err != nil {
		return nil, fmt.Errorf("query.ListPlanFeatures: %w", err)
	}
	defer rows.Close()

	var out []PlanFeature
	for rows.Next() {
		var pf PlanFeature
		if err := rows.Scan(&pf.PlanID, &pf.FeatureID, &pf.LimitValue, &pf.ConfigValue); err != nil {
			return nil, fmt.Errorf("query.ListPlanFeatures: scan: %w", err)
		}
		out = append(out, pf)
	}
	return out, nil
}

func UpsertPlanFeature(ctx context.Context, pool *pgxpool.Pool,
	planID, featureID string, limitValue *int64, configValue []byte,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.plan_features (plan_id, feature_id, limit_value, config_value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (plan_id, feature_id) DO UPDATE
		SET limit_value = $3, config_value = $4
	`, planID, featureID, limitValue, configValue)
	if err != nil {
		return fmt.Errorf("query.UpsertPlanFeature: %w", err)
	}
	return nil
}

func DeletePlanFeature(ctx context.Context, pool *pgxpool.Pool, planID, featureID string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM billing.plan_features WHERE plan_id = $1 AND feature_id = $2
	`, planID, featureID)
	if err != nil {
		return fmt.Errorf("query.DeletePlanFeature: %w", err)
	}
	return nil
}

// --- Coupons ---

func ListCoupons(ctx context.Context, pool *pgxpool.Pool) ([]Coupon, error) {
	rows, err := pool.Query(ctx, `
		SELECT code, name, discount_type, amount_cents, percent_off, currency,
		       cadence, duration_count, valid_from, valid_until, max_redemptions,
		       redeemed_count, metadata, active, created_at
		FROM billing.coupons
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListCoupons: %w", err)
	}
	defer rows.Close()

	var out []Coupon
	for rows.Next() {
		var c Coupon
		if err := rows.Scan(
			&c.Code, &c.Name, &c.DiscountType, &c.AmountCents, &c.PercentOff, &c.Currency,
			&c.Cadence, &c.DurationCount, &c.ValidFrom, &c.ValidUntil, &c.MaxRedemptions,
			&c.RedeemedCount, &c.Metadata, &c.Active, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("query.ListCoupons: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func CreateCoupon(ctx context.Context, pool *pgxpool.Pool,
	code, name, discountType string, amountCents *int64, percentOff *int, currency *string,
	cadence string, durationCount *int, validFrom, validUntil *time.Time, maxRedemptions *int,
	metadata []byte, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.coupons
			(code, name, discount_type, amount_cents, percent_off, currency,
			 cadence, duration_count, valid_from, valid_until, max_redemptions, metadata, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, code, name, discountType, amountCents, percentOff, currency,
		cadence, durationCount, validFrom, validUntil, maxRedemptions, metadata, active)
	if err != nil {
		return fmt.Errorf("query.CreateCoupon: %w", err)
	}
	return nil
}

func UpdateCoupon(ctx context.Context, pool *pgxpool.Pool,
	code, name, discountType string, amountCents *int64, percentOff *int, currency *string,
	cadence string, durationCount *int, validFrom, validUntil *time.Time, maxRedemptions *int,
	metadata []byte, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.coupons
		SET name=$2, discount_type=$3, amount_cents=$4, percent_off=$5, currency=$6,
		    cadence=$7, duration_count=$8, valid_from=$9, valid_until=$10,
		    max_redemptions=$11, metadata=$12, active=$13
		WHERE code=$1
	`, code, name, discountType, amountCents, percentOff, currency,
		cadence, durationCount, validFrom, validUntil, maxRedemptions, metadata, active)
	if err != nil {
		return fmt.Errorf("query.UpdateCoupon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdateCoupon: coupon not found")
	}
	return nil
}

func DeleteCoupon(ctx context.Context, pool *pgxpool.Pool, code string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM billing.coupons WHERE code = $1 AND redeemed_count = 0
	`, code)
	if err != nil {
		return fmt.Errorf("query.DeleteCoupon: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var redeemedCount int
	if err := pool.QueryRow(ctx, `
		SELECT redeemed_count FROM billing.coupons WHERE code = $1
	`, code).Scan(&redeemedCount); err != nil {
		return fmt.Errorf("query.DeleteCoupon: coupon not found")
	}
	return fmt.Errorf("coupon %q has been redeemed %d time(s) — deactivate it instead of deleting", code, redeemedCount)
}

// --- Coupon Targets ---

func ListCouponTargets(ctx context.Context, pool *pgxpool.Pool, couponID string) ([]CouponTarget, error) {
	rows, err := pool.Query(ctx, `
		SELECT coupon_id, subject_type, subject_id, created_at
		FROM billing.coupon_targets
		WHERE coupon_id = $1
		ORDER BY created_at
	`, couponID)
	if err != nil {
		return nil, fmt.Errorf("query.ListCouponTargets: %w", err)
	}
	defer rows.Close()

	var out []CouponTarget
	for rows.Next() {
		var t CouponTarget
		if err := rows.Scan(&t.CouponID, &t.SubjectType, &t.SubjectID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("query.ListCouponTargets: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, nil
}

func AddCouponTarget(ctx context.Context, pool *pgxpool.Pool, couponID, subjectType, subjectID string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.coupon_targets (coupon_id, subject_type, subject_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, couponID, subjectType, subjectID)
	if err != nil {
		return fmt.Errorf("query.AddCouponTarget: %w", err)
	}
	return nil
}

func RemoveCouponTarget(ctx context.Context, pool *pgxpool.Pool, couponID, subjectType, subjectID string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM billing.coupon_targets WHERE coupon_id = $1 AND subject_type = $2 AND subject_id = $3
	`, couponID, subjectType, subjectID)
	if err != nil {
		return fmt.Errorf("query.RemoveCouponTarget: %w", err)
	}
	return nil
}

// --- Addons ---

func ListAddons(ctx context.Context, pool *pgxpool.Pool) ([]Addon, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, description, prices, active, created_at
		FROM billing.addons
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListAddons: %w", err)
	}
	defer rows.Close()

	var out []Addon
	for rows.Next() {
		var a Addon
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.Prices, &a.Active, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("query.ListAddons: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, nil
}

func CreateAddon(ctx context.Context, pool *pgxpool.Pool,
	id, name, description string, prices []byte, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.addons (id, name, description, prices, active)
		VALUES ($1, $2, $3, $4, $5)
	`, id, name, description, prices, active)
	if err != nil {
		return fmt.Errorf("query.CreateAddon: %w", err)
	}
	return nil
}

func UpdateAddon(ctx context.Context, pool *pgxpool.Pool,
	id, name, description string, prices []byte, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE billing.addons
		SET name=$2, description=$3, prices=$4, active=$5
		WHERE id=$1
	`, id, name, description, prices, active)
	if err != nil {
		return fmt.Errorf("query.UpdateAddon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdateAddon: addon not found")
	}
	return nil
}

func DeleteAddon(ctx context.Context, pool *pgxpool.Pool, id string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM billing.addons
		WHERE id = $1
		  AND NOT EXISTS (SELECT 1 FROM billing.subscription_addons WHERE addon_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM billing.addon_features WHERE addon_id = $1)
	`, id)
	if err != nil {
		return fmt.Errorf("query.DeleteAddon: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var subCount, featureCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM billing.subscription_addons WHERE addon_id = $1),
		       (SELECT COUNT(*) FROM billing.addon_features WHERE addon_id = $1)
	`, id).Scan(&subCount, &featureCount); err != nil {
		return fmt.Errorf("query.DeleteAddon: %w", err)
	}
	if subCount > 0 {
		return fmt.Errorf("addon %q is attached to %d subscription(s) — detach it first", id, subCount)
	}
	if featureCount > 0 {
		return fmt.Errorf("addon %q has %d entitlement(s) — remove them first (Manage Entitlements)", id, featureCount)
	}
	return fmt.Errorf("query.DeleteAddon: addon not found")
}

// --- Addon Features (entitlement management) ---

func ListAddonFeatures(ctx context.Context, pool *pgxpool.Pool, addonID string) ([]AddonFeature, error) {
	rows, err := pool.Query(ctx, `
		SELECT addon_id, feature_id, limit_value
		FROM billing.addon_features
		WHERE addon_id = $1
		ORDER BY feature_id
	`, addonID)
	if err != nil {
		return nil, fmt.Errorf("query.ListAddonFeatures: %w", err)
	}
	defer rows.Close()

	var out []AddonFeature
	for rows.Next() {
		var af AddonFeature
		if err := rows.Scan(&af.AddonID, &af.FeatureID, &af.LimitValue); err != nil {
			return nil, fmt.Errorf("query.ListAddonFeatures: scan: %w", err)
		}
		out = append(out, af)
	}
	return out, nil
}

func UpsertAddonFeature(ctx context.Context, pool *pgxpool.Pool, addonID, featureID string, limitValue *int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO billing.addon_features (addon_id, feature_id, limit_value)
		VALUES ($1, $2, $3)
		ON CONFLICT (addon_id, feature_id) DO UPDATE
		SET limit_value = $3
	`, addonID, featureID, limitValue)
	if err != nil {
		return fmt.Errorf("query.UpsertAddonFeature: %w", err)
	}
	return nil
}

func DeleteAddonFeature(ctx context.Context, pool *pgxpool.Pool, addonID, featureID string) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM billing.addon_features WHERE addon_id = $1 AND feature_id = $2
	`, addonID, featureID)
	if err != nil {
		return fmt.Errorf("query.DeleteAddonFeature: %w", err)
	}
	return nil
}
