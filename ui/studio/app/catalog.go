package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Plan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prices      string `json:"prices"`
	SortOrder   int    `json:"sort_order"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type PlanInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prices      string `json:"prices"`
	SortOrder   int    `json:"sort_order"`
	Active      bool   `json:"active"`
}

type Feature struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	MetricKey   string `json:"metric_key"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
}

type FeatureInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	MetricKey   string `json:"metric_key"`
	Active      bool   `json:"active"`
}

type PlanFeature struct {
	PlanID      string `json:"plan_id"`
	FeatureID   string `json:"feature_id"`
	LimitValue  *int64 `json:"limit_value"`
	ConfigValue string `json:"config_value"`
}

type PlanFeatureInput struct {
	FeatureID   string `json:"feature_id"`
	LimitValue  *int64 `json:"limit_value"`
	ConfigValue string `json:"config_value"`
}

type Coupon struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	DiscountType   string `json:"discount_type"`
	AmountCents    *int64 `json:"amount_cents"`
	PercentOff     *int   `json:"percent_off"`
	Currency       string `json:"currency"`
	Cadence        string `json:"cadence"`
	DurationCount  *int   `json:"duration_count"`
	ValidFrom      string `json:"valid_from"`
	ValidUntil     string `json:"valid_until"`
	MaxRedemptions *int   `json:"max_redemptions"`
	RedeemedCount  int    `json:"redeemed_count"`
	Metadata       string `json:"metadata"`
	Active         bool   `json:"active"`
	CreatedAt      string `json:"created_at"`
}

type CouponInput struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	DiscountType   string `json:"discount_type"`
	AmountCents    *int64 `json:"amount_cents"`
	PercentOff     *int   `json:"percent_off"`
	Currency       string `json:"currency"`
	Cadence        string `json:"cadence"`
	DurationCount  *int   `json:"duration_count"`
	ValidFrom      string `json:"valid_from"`
	ValidUntil     string `json:"valid_until"`
	MaxRedemptions *int   `json:"max_redemptions"`
	Metadata       string `json:"metadata"`
	Active         bool   `json:"active"`
}

type CouponTarget struct {
	CouponID    string `json:"coupon_id"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	CreatedAt   string `json:"created_at"`
}

type Addon struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prices      string `json:"prices"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"created_at"`
}

type AddonInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prices      string `json:"prices"`
	Active      bool   `json:"active"`
}

type AddonFeature struct {
	AddonID    string `json:"addon_id"`
	FeatureID  string `json:"feature_id"`
	LimitValue *int64 `json:"limit_value"`
}

type AddonFeatureInput struct {
	FeatureID  string `json:"feature_id"`
	LimitValue *int64 `json:"limit_value"`
}

var validFeatureTypes = map[string]bool{"metered": true, "boolean": true, "static": true, "config": true}
var validDiscountTypes = map[string]bool{"fixed": true, "percent": true}
var validCadences = map[string]bool{"once": true, "repeated": true, "forever": true}
var validSubjectTypes = map[string]bool{"organization": true, "user": true}

type CatalogService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewCatalogService(projects *ProjectService, pool *connect.PostgresPool) *CatalogService {
	return &CatalogService{projects: projects, pool: pool}
}

// --- Plans ---

func (s *CatalogService) ListPlans(projectID string) ([]Plan, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListPlans", 15*time.Second, query.ListPlans)
	if err != nil {
		return nil, err
	}

	out := make([]Plan, len(raw))
	for i, p := range raw {
		out[i] = Plan{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Prices:      string(p.Prices),
			SortOrder:   p.SortOrder,
			Active:      p.Active,
			CreatedAt:   p.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   p.UpdatedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

func (s *CatalogService) CreatePlan(projectID string, input PlanInput) error {
	if input.ID == "" {
		return fmt.Errorf("CatalogService.CreatePlan: id is required")
	}
	if input.Name == "" {
		return fmt.Errorf("CatalogService.CreatePlan: name is required")
	}
	if !json.Valid([]byte(input.Prices)) {
		return fmt.Errorf("CatalogService.CreatePlan: prices is not valid JSON")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.CreatePlan", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreatePlan(ctx, db, input.ID, input.Name, input.Description, []byte(input.Prices), input.SortOrder, input.Active)
		})
}

func (s *CatalogService) UpdatePlan(projectID, planID string, input PlanInput) error {
	if input.Name == "" {
		return fmt.Errorf("CatalogService.UpdatePlan: name is required")
	}
	if !json.Valid([]byte(input.Prices)) {
		return fmt.Errorf("CatalogService.UpdatePlan: prices is not valid JSON")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpdatePlan", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdatePlan(ctx, db, planID, input.Name, input.Description, []byte(input.Prices), input.SortOrder, input.Active)
		})
}

func (s *CatalogService) DeletePlan(projectID, planID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeletePlan", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeletePlan(ctx, db, planID)
		})
}

// --- Features ---

func (s *CatalogService) ListFeatures(projectID string) ([]Feature, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListFeatures", 15*time.Second, query.ListFeatures)
	if err != nil {
		return nil, err
	}

	out := make([]Feature, len(raw))
	for i, f := range raw {
		metricKey := ""
		if f.MetricKey != nil {
			metricKey = *f.MetricKey
		}
		out[i] = Feature{
			ID:          f.ID,
			Name:        f.Name,
			Description: f.Description,
			Type:        f.Type,
			MetricKey:   metricKey,
			Active:      f.Active,
			CreatedAt:   f.CreatedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

func validateFeatureInput(input FeatureInput) error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !validFeatureTypes[input.Type] {
		return fmt.Errorf("type must be one of metered, boolean, static, config")
	}
	if input.Type == "metered" && input.MetricKey == "" {
		return fmt.Errorf("metric_key is required for metered features")
	}
	if input.Type != "metered" && input.MetricKey != "" {
		return fmt.Errorf("metric_key is only valid for metered features")
	}
	return nil
}

func (s *CatalogService) CreateFeature(projectID string, input FeatureInput) error {
	if input.ID == "" {
		return fmt.Errorf("CatalogService.CreateFeature: id is required")
	}
	if err := validateFeatureInput(input); err != nil {
		return fmt.Errorf("CatalogService.CreateFeature: %w", err)
	}

	var metricKey *string
	if input.MetricKey != "" {
		metricKey = &input.MetricKey
	}
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.CreateFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreateFeature(ctx, db, input.ID, input.Name, input.Description, input.Type, metricKey, input.Active)
		})
}

func (s *CatalogService) UpdateFeature(projectID, featureID string, input FeatureInput) error {
	if err := validateFeatureInput(input); err != nil {
		return fmt.Errorf("CatalogService.UpdateFeature: %w", err)
	}

	var metricKey *string
	if input.MetricKey != "" {
		metricKey = &input.MetricKey
	}
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpdateFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdateFeature(ctx, db, featureID, input.Name, input.Description, input.Type, metricKey, input.Active)
		})
}

func (s *CatalogService) DeleteFeature(projectID, featureID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeleteFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteFeature(ctx, db, featureID)
		})
}

// --- Plan Features (entitlement management) ---

func (s *CatalogService) ListPlanFeatures(projectID, planID string) ([]PlanFeature, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListPlanFeatures", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.PlanFeature, error) {
			return query.ListPlanFeatures(ctx, db, planID)
		})
	if err != nil {
		return nil, err
	}

	out := make([]PlanFeature, len(raw))
	for i, pf := range raw {
		out[i] = PlanFeature{
			PlanID:      pf.PlanID,
			FeatureID:   pf.FeatureID,
			LimitValue:  pf.LimitValue,
			ConfigValue: string(pf.ConfigValue),
		}
	}
	return out, nil
}

func (s *CatalogService) UpsertPlanFeature(projectID, planID string, input PlanFeatureInput) error {
	if input.FeatureID == "" {
		return fmt.Errorf("CatalogService.UpsertPlanFeature: feature_id is required")
	}
	if input.ConfigValue != "" && !json.Valid([]byte(input.ConfigValue)) {
		return fmt.Errorf("CatalogService.UpsertPlanFeature: config_value is not valid JSON")
	}

	var configValue []byte
	if input.ConfigValue != "" {
		configValue = []byte(input.ConfigValue)
	}
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpsertPlanFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpsertPlanFeature(ctx, db, planID, input.FeatureID, input.LimitValue, configValue)
		})
}

func (s *CatalogService) DeletePlanFeature(projectID, planID, featureID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeletePlanFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeletePlanFeature(ctx, db, planID, featureID)
		})
}

// --- Coupons ---

func (s *CatalogService) ListCoupons(projectID string) ([]Coupon, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListCoupons", 15*time.Second, query.ListCoupons)
	if err != nil {
		return nil, err
	}

	out := make([]Coupon, len(raw))
	for i, c := range raw {
		out[i] = couponToDTO(c)
	}
	return out, nil
}

func couponToDTO(c query.Coupon) Coupon {
	currency := ""
	if c.Currency != nil {
		currency = *c.Currency
	}
	validFrom := ""
	if c.ValidFrom != nil {
		validFrom = c.ValidFrom.Format(time.RFC3339)
	}
	validUntil := ""
	if c.ValidUntil != nil {
		validUntil = c.ValidUntil.Format(time.RFC3339)
	}
	return Coupon{
		Code:           c.Code,
		Name:           c.Name,
		DiscountType:   c.DiscountType,
		AmountCents:    c.AmountCents,
		PercentOff:     c.PercentOff,
		Currency:       currency,
		Cadence:        c.Cadence,
		DurationCount:  c.DurationCount,
		ValidFrom:      validFrom,
		ValidUntil:     validUntil,
		MaxRedemptions: c.MaxRedemptions,
		RedeemedCount:  c.RedeemedCount,
		Metadata:       string(c.Metadata),
		Active:         c.Active,
		CreatedAt:      c.CreatedAt.Format(time.RFC3339),
	}
}

func validateCouponInput(input CouponInput) error {
	if input.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !validDiscountTypes[input.DiscountType] {
		return fmt.Errorf("discount_type must be one of fixed, percent")
	}
	if input.DiscountType == "fixed" && (input.AmountCents == nil || input.Currency == "") {
		return fmt.Errorf("amount_cents and currency are required for fixed discounts")
	}
	if input.DiscountType == "percent" && input.PercentOff == nil {
		return fmt.Errorf("percent_off is required for percent discounts")
	}
	if input.PercentOff != nil && (*input.PercentOff < 0 || *input.PercentOff > 100) {
		return fmt.Errorf("percent_off must be between 0 and 100")
	}
	if !validCadences[input.Cadence] {
		return fmt.Errorf("cadence must be one of once, repeated, forever")
	}
	if input.Cadence == "repeated" && input.DurationCount == nil {
		return fmt.Errorf("duration_count is required when cadence is repeated")
	}
	if input.Metadata != "" && !json.Valid([]byte(input.Metadata)) {
		return fmt.Errorf("metadata is not valid JSON")
	}
	return nil
}

func parseOptionalTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	return &t, nil
}

func (s *CatalogService) CreateCoupon(projectID string, input CouponInput) error {
	if input.Code == "" {
		return fmt.Errorf("CatalogService.CreateCoupon: code is required")
	}
	if err := validateCouponInput(input); err != nil {
		return fmt.Errorf("CatalogService.CreateCoupon: %w", err)
	}

	validFrom, err := parseOptionalTime(input.ValidFrom)
	if err != nil {
		return fmt.Errorf("CatalogService.CreateCoupon: %w", err)
	}
	validUntil, err := parseOptionalTime(input.ValidUntil)
	if err != nil {
		return fmt.Errorf("CatalogService.CreateCoupon: %w", err)
	}

	var currency *string
	if input.Currency != "" {
		currency = &input.Currency
	}
	metadata := []byte(input.Metadata)
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.CreateCoupon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreateCoupon(ctx, db, input.Code, input.Name, input.DiscountType,
				input.AmountCents, input.PercentOff, currency,
				input.Cadence, input.DurationCount, validFrom, validUntil, input.MaxRedemptions,
				metadata, input.Active)
		})
}

func (s *CatalogService) UpdateCoupon(projectID, code string, input CouponInput) error {
	if err := validateCouponInput(input); err != nil {
		return fmt.Errorf("CatalogService.UpdateCoupon: %w", err)
	}

	validFrom, err := parseOptionalTime(input.ValidFrom)
	if err != nil {
		return fmt.Errorf("CatalogService.UpdateCoupon: %w", err)
	}
	validUntil, err := parseOptionalTime(input.ValidUntil)
	if err != nil {
		return fmt.Errorf("CatalogService.UpdateCoupon: %w", err)
	}

	var currency *string
	if input.Currency != "" {
		currency = &input.Currency
	}
	metadata := []byte(input.Metadata)
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpdateCoupon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdateCoupon(ctx, db, code, input.Name, input.DiscountType,
				input.AmountCents, input.PercentOff, currency,
				input.Cadence, input.DurationCount, validFrom, validUntil, input.MaxRedemptions,
				metadata, input.Active)
		})
}

func (s *CatalogService) DeleteCoupon(projectID, code string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeleteCoupon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteCoupon(ctx, db, code)
		})
}

// --- Coupon Targets ---

func (s *CatalogService) ListCouponTargets(projectID, couponID string) ([]CouponTarget, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListCouponTargets", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.CouponTarget, error) {
			return query.ListCouponTargets(ctx, db, couponID)
		})
	if err != nil {
		return nil, err
	}

	out := make([]CouponTarget, len(raw))
	for i, t := range raw {
		out[i] = CouponTarget{
			CouponID:    t.CouponID,
			SubjectType: t.SubjectType,
			SubjectID:   t.SubjectID,
			CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

func (s *CatalogService) AddCouponTarget(projectID, couponID, subjectType, subjectID string) error {
	if !validSubjectTypes[subjectType] {
		return fmt.Errorf("CatalogService.AddCouponTarget: subject_type must be organization or user")
	}
	if subjectID == "" {
		return fmt.Errorf("CatalogService.AddCouponTarget: subject_id is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.AddCouponTarget", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.AddCouponTarget(ctx, db, couponID, subjectType, subjectID)
		})
}

func (s *CatalogService) RemoveCouponTarget(projectID, couponID, subjectType, subjectID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.RemoveCouponTarget", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.RemoveCouponTarget(ctx, db, couponID, subjectType, subjectID)
		})
}

// --- Addons ---

func (s *CatalogService) ListAddons(projectID string) ([]Addon, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListAddons", 15*time.Second, query.ListAddons)
	if err != nil {
		return nil, err
	}

	out := make([]Addon, len(raw))
	for i, a := range raw {
		out[i] = Addon{
			ID:          a.ID,
			Name:        a.Name,
			Description: a.Description,
			Prices:      string(a.Prices),
			Active:      a.Active,
			CreatedAt:   a.CreatedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

func (s *CatalogService) CreateAddon(projectID string, input AddonInput) error {
	if input.ID == "" {
		return fmt.Errorf("CatalogService.CreateAddon: id is required")
	}
	if input.Name == "" {
		return fmt.Errorf("CatalogService.CreateAddon: name is required")
	}
	if !json.Valid([]byte(input.Prices)) {
		return fmt.Errorf("CatalogService.CreateAddon: prices is not valid JSON")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.CreateAddon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreateAddon(ctx, db, input.ID, input.Name, input.Description, []byte(input.Prices), input.Active)
		})
}

func (s *CatalogService) UpdateAddon(projectID, addonID string, input AddonInput) error {
	if input.Name == "" {
		return fmt.Errorf("CatalogService.UpdateAddon: name is required")
	}
	if !json.Valid([]byte(input.Prices)) {
		return fmt.Errorf("CatalogService.UpdateAddon: prices is not valid JSON")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpdateAddon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdateAddon(ctx, db, addonID, input.Name, input.Description, []byte(input.Prices), input.Active)
		})
}

func (s *CatalogService) DeleteAddon(projectID, addonID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeleteAddon", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteAddon(ctx, db, addonID)
		})
}

// --- Addon Features (entitlement management) ---

func (s *CatalogService) ListAddonFeatures(projectID, addonID string) ([]AddonFeature, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "CatalogService.ListAddonFeatures", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) ([]query.AddonFeature, error) {
			return query.ListAddonFeatures(ctx, db, addonID)
		})
	if err != nil {
		return nil, err
	}

	out := make([]AddonFeature, len(raw))
	for i, af := range raw {
		out[i] = AddonFeature{AddonID: af.AddonID, FeatureID: af.FeatureID, LimitValue: af.LimitValue}
	}
	return out, nil
}

func (s *CatalogService) UpsertAddonFeature(projectID, addonID string, input AddonFeatureInput) error {
	if input.FeatureID == "" {
		return fmt.Errorf("CatalogService.UpsertAddonFeature: feature_id is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.UpsertAddonFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpsertAddonFeature(ctx, db, addonID, input.FeatureID, input.LimitValue)
		})
}

func (s *CatalogService) DeleteAddonFeature(projectID, addonID, featureID string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "CatalogService.DeleteAddonFeature", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteAddonFeature(ctx, db, addonID, featureID)
		})
}
