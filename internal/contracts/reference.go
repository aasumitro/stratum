package contracts

import (
	"context"
	"encoding/json"
	"time"
)

// PlanPrices holds the monthly and yearly price for one currency.
// Amounts are in minor units (cents for USD, full units for IDR).
type PlanPrices struct {
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// PlanInfo is the minimal projection of plan data for modules that need
// to validate or display pricing tiers without owning the references schema.
type PlanInfo struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Prices      map[string]PlanPrices `json:"prices"`
	Limits      map[string]int        `json:"limits"`
	Features    []string              `json:"features"`
	// SortOrder mirrors billing.plans.sort_order — the tier ranking used to
	// detect upgrade vs downgrade, replacing a hardcoded plan-slug map.
	SortOrder int `json:"sort_order"`
	// ConfigValues holds the raw config_value JSON for this plan's
	// type='config' features (billing.plan_features), keyed by feature ID —
	// e.g. {"api_rate_limit": {"requests_per_minute": 300}}.
	ConfigValues map[string]json.RawMessage `json:"config_values"`
	// Active/CreatedAt/UpdatedAt round out the wire shape GET /references/plans
	// has always returned; internal callers (rate-limit middleware, plan
	// validation) don't use them.
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FeatureInfo is the catalog projection of a billing.features row.
type FeatureInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"` // "metered" | "boolean" | "static" | "config"
	MetricKey   string `json:"metric_key,omitempty"`
	// Active/CreatedAt round out the wire shape GET /references/features
	// has always returned.
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// AddonInfo is the catalog projection of a billing.addons row plus its
// entitlement deltas (billing.addon_features) — limit_value here is additive
// on top of whatever the subscription's plan already grants.
type AddonInfo struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Prices      map[string]PlanPrices `json:"prices"`
	Features    map[string]int        `json:"features"` // feature_id -> limit_value delta
	// Active/CreatedAt round out the wire shape GET /references/addons
	// has always returned.
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// CurrencyUSD/CurrencyIDR are the only two currencies the catalog prices
// plans/addons in today (db/migrations/000004_billing.up.sql's seed data).
const (
	CurrencyUSD = "USD"
	CurrencyIDR = "IDR"
)

// ResolveCurrency maps a country code to the currency an organization from
// that country is actually billed in. The single source of truth for this
// rule — billing's subscription provisioning and the reference catalog's
// currency-scoped price filtering both call this instead of each keeping
// their own copy, so they can't drift on which currency an org gets billed.
func ResolveCurrency(countryCode string) string {
	if countryCode == "ID" {
		return CurrencyIDR
	}
	return CurrencyUSD
}

// Price returns the price for a given currency and cycle.
// Returns 0 if the currency or cycle is not found.
func (p *PlanInfo) Price(currency, cycle string) int64 {
	prices, ok := p.Prices[currency]
	if !ok {
		return 0
	}
	if cycle == "yearly" {
		return int64(prices.Yearly)
	}
	return int64(prices.Monthly)
}

// CatalogReader is implemented by the billing module (the catalog's
// schema owner) and consumed by modules that need plan/feature/addon data
// without owning the billing schema.
type CatalogReader interface {
	GetPlanByID(ctx context.Context, id string) (*PlanInfo, error)
	ListPlans(ctx context.Context) ([]PlanInfo, error)
	ListFeatures(ctx context.Context) ([]FeatureInfo, error)
	ListAddons(ctx context.Context) ([]AddonInfo, error)
	GetAddonByID(ctx context.Context, id string) (*AddonInfo, error)
	// ValidateCouponCode reports whether code is redeemable by authSub right
	// now, ignoring any organization-specific targeting (there is no
	// organization yet at the call site this exists for — organization
	// creation's up-front cart validation). Returns an error if the code is
	// unknown, inactive, expired, exhausted, or targeted at a different user.
	ValidateCouponCode(ctx context.Context, code, authSub string) error
}

// CountryTaxReader is implemented by the reference module and consumed by
// modules that need a country's tax rate without owning the ref schema.
type CountryTaxReader interface {
	GetCountryTaxRate(ctx context.Context, countryCode string) (int, error)
}
