package billing

import (
	"testing"

	"github.com/aasumitro/stratum/internal/contracts"
)

func TestScopeAddonPrice(t *testing.T) {
	both := func() *attachedAddonRecord {
		return &attachedAddonRecord{
			AddonID: "extra-seat",
			Prices: map[string]contracts.PlanPrices{
				"USD": {Monthly: 500, Yearly: 5000},
				"IDR": {Monthly: 75000, Yearly: 750000},
			},
		}
	}

	t.Run("IDR subscription sees IDR only", func(t *testing.T) {
		got := scopeAddonPrice(both(), "IDR")
		if len(got.Prices) != 1 {
			t.Fatalf("want exactly 1 currency, got %d: %v", len(got.Prices), got.Prices)
		}
		if _, ok := got.Prices["IDR"]; !ok {
			t.Errorf("want IDR key, got %v", got.Prices)
		}
	})

	t.Run("USD subscription sees USD only", func(t *testing.T) {
		got := scopeAddonPrice(both(), "USD")
		if len(got.Prices) != 1 {
			t.Fatalf("want exactly 1 currency, got %d: %v", len(got.Prices), got.Prices)
		}
		if _, ok := got.Prices["USD"]; !ok {
			t.Errorf("want USD key, got %v", got.Prices)
		}
	})

	t.Run("missing currency falls back to USD", func(t *testing.T) {
		a := &attachedAddonRecord{Prices: map[string]contracts.PlanPrices{"USD": {Monthly: 500}}}
		got := scopeAddonPrice(a, "IDR")
		if _, ok := got.Prices["USD"]; !ok || len(got.Prices) != 1 {
			t.Errorf("want USD-only fallback, got %v", got.Prices)
		}
	})

	t.Run("no USD fallback available leaves an empty map, never the full one", func(t *testing.T) {
		a := &attachedAddonRecord{Prices: map[string]contracts.PlanPrices{"EUR": {Monthly: 500}}}
		got := scopeAddonPrice(a, "IDR")
		if len(got.Prices) != 0 {
			t.Errorf("want empty prices, got %v", got.Prices)
		}
	})
}
