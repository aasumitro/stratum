package reference

import (
	"context"
	"testing"

	"github.com/aasumitro/stratum/internal/contracts"
)

// stubCatalogWithBothCurrencies satisfies contracts.CatalogReader with one
// plan and one addon, each priced in both USD and IDR — enough to exercise
// scopedPrices without a cross-module import into billing.
type stubCatalogWithBothCurrencies struct{}

func (stubCatalogWithBothCurrencies) GetPlanByID(_ context.Context, _ string) (*contracts.PlanInfo, error) {
	return nil, nil
}

func (stubCatalogWithBothCurrencies) ListPlans(_ context.Context) ([]contracts.PlanInfo, error) {
	return []contracts.PlanInfo{{
		ID: "solo",
		Prices: map[string]contracts.PlanPrices{
			"USD": {Monthly: 900, Yearly: 9000},
			"IDR": {Monthly: 150000, Yearly: 1500000},
		},
	}}, nil
}

func (stubCatalogWithBothCurrencies) ListFeatures(_ context.Context) ([]contracts.FeatureInfo, error) {
	return nil, nil
}

func (stubCatalogWithBothCurrencies) ListAddons(_ context.Context) ([]contracts.AddonInfo, error) {
	return []contracts.AddonInfo{{
		ID: "extra-members",
		Prices: map[string]contracts.PlanPrices{
			"USD": {Monthly: 500, Yearly: 5000},
			"IDR": {Monthly: 75000, Yearly: 750000},
		},
	}}, nil
}

func (stubCatalogWithBothCurrencies) GetAddonByID(_ context.Context, _ string) (*contracts.AddonInfo, error) {
	return nil, nil
}

func (stubCatalogWithBothCurrencies) ValidateCouponCode(_ context.Context, _, _ string) error {
	return nil
}

func TestScopedPrices(t *testing.T) {
	prices := map[string]contracts.PlanPrices{
		"USD": {Monthly: 900, Yearly: 9000},
		"IDR": {Monthly: 150000, Yearly: 1500000},
	}

	if got := scopedPrices(prices, ""); len(got) != 2 {
		t.Errorf("empty countryCode: want unchanged 2-currency map, got %d entries", len(got))
	}

	got := scopedPrices(prices, "ID")
	if len(got) != 1 {
		t.Fatalf("ID: want exactly 1 currency, got %d", len(got))
	}
	if _, ok := got["IDR"]; !ok {
		t.Errorf("ID: want IDR key, got %v", got)
	}

	got = scopedPrices(prices, "US")
	if len(got) != 1 {
		t.Fatalf("US: want exactly 1 currency, got %d", len(got))
	}
	if _, ok := got["USD"]; !ok {
		t.Errorf("US: want USD key, got %v", got)
	}

	// Resolved currency missing from the map falls back to USD, matching
	// contracts.ResolveCurrency's own fallback semantics.
	got = scopedPrices(map[string]contracts.PlanPrices{"USD": {Monthly: 900}}, "ID")
	if _, ok := got["USD"]; !ok || len(got) != 1 {
		t.Errorf("ID with no IDR entry: want USD-only fallback, got %v", got)
	}
}

func TestListPlans_CountryCodeScopesPrices(t *testing.T) {
	svc := &service{catalog: stubCatalogWithBothCurrencies{}}

	all, err := svc.listPlans(t.Context(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all[0].Prices) != 2 {
		t.Errorf("no country_code: want both currencies, got %d", len(all[0].Prices))
	}

	scoped, err := svc.listPlans(t.Context(), "ID")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scoped[0].Prices) != 1 {
		t.Fatalf("country_code=ID: want exactly 1 currency, got %d", len(scoped[0].Prices))
	}
	if _, ok := scoped[0].Prices["IDR"]; !ok {
		t.Errorf("country_code=ID: want IDR only, got %v", scoped[0].Prices)
	}
}

func TestListAddons_CountryCodeScopesPrices(t *testing.T) {
	svc := &service{catalog: stubCatalogWithBothCurrencies{}}

	scoped, err := svc.listAddons(t.Context(), "ID")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scoped[0].Prices) != 1 {
		t.Fatalf("country_code=ID: want exactly 1 currency, got %d", len(scoped[0].Prices))
	}
	if _, ok := scoped[0].Prices["IDR"]; !ok {
		t.Errorf("country_code=ID: want IDR only, got %v", scoped[0].Prices)
	}
}
