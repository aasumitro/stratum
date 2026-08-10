package reference

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/apperr"
)

type service struct {
	repo    *repository
	pool    *pgxpool.Pool
	catalog contracts.CatalogReader // optional; nil until SetCatalogReader wires the billing module
}

func (s *service) listCountries(ctx context.Context) ([]countryRecord, error) {
	countries, err := s.repo.listCountries(ctx, s.pool)
	if err != nil {
		return nil, apperr.Internal("COUNTRIES_FETCH_FAILED", "failed to list countries", err)
	}
	return countries, nil
}

func (s *service) listCurrencies(ctx context.Context) ([]currencyRecord, error) {
	currencies, err := s.repo.listCurrencies(ctx, s.pool)
	if err != nil {
		return nil, apperr.Internal("CURRENCIES_FETCH_FAILED", "failed to list currencies", err)
	}
	return currencies, nil
}

// scopedPrices trims a catalog price map down to the single currency an
// org from countryCode is actually billed in (contracts.ResolveCurrency) —
// every caller now passes a server-resolved (GeoIP, never client-supplied)
// countryCode, and always gets back exactly one currency; there is no
// longer a "give me everything" mode (see handler.go's resolveCountryCode).
func scopedPrices(prices map[string]contracts.PlanPrices, countryCode string) map[string]contracts.PlanPrices {
	currency := contracts.ResolveCurrency(countryCode)
	amount, ok := prices[currency]
	if !ok {
		amount, ok = prices[contracts.CurrencyUSD]
		currency = contracts.CurrencyUSD
	}
	if !ok {
		return map[string]contracts.PlanPrices{}
	}
	return map[string]contracts.PlanPrices{currency: amount}
}

// listPlans/listFeatures/listAddons serve GET /references/plans|features|addons
// by delegating to the billing module, the catalog's schema owner.
func (s *service) listPlans(ctx context.Context, countryCode string) ([]contracts.PlanInfo, error) {
	if s.catalog == nil {
		return nil, apperr.Internal("PLANS_FETCH_FAILED", "failed to list plans",
			errors.New("reference.listPlans: catalog reader not wired"))
	}
	plans, err := s.catalog.ListPlans(ctx)
	if err != nil {
		return nil, apperr.Internal("PLANS_FETCH_FAILED", "failed to list plans", err)
	}
	for i := range plans {
		plans[i].Prices = scopedPrices(plans[i].Prices, countryCode)
	}
	return plans, nil
}

func (s *service) listFeatures(ctx context.Context) ([]contracts.FeatureInfo, error) {
	if s.catalog == nil {
		return nil, apperr.Internal("FEATURES_FETCH_FAILED", "failed to list features",
			errors.New("reference.listFeatures: catalog reader not wired"))
	}
	features, err := s.catalog.ListFeatures(ctx)
	if err != nil {
		return nil, apperr.Internal("FEATURES_FETCH_FAILED", "failed to list features", err)
	}
	return features, nil
}

func (s *service) listAddons(ctx context.Context, countryCode string) ([]contracts.AddonInfo, error) {
	if s.catalog == nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons",
			errors.New("reference.listAddons: catalog reader not wired"))
	}
	addons, err := s.catalog.ListAddons(ctx)
	if err != nil {
		return nil, apperr.Internal("ADDONS_FETCH_FAILED", "failed to list addons", err)
	}
	for i := range addons {
		addons[i].Prices = scopedPrices(addons[i].Prices, countryCode)
	}
	return addons, nil
}

func (s *service) getCountryTaxRate(ctx context.Context, code string) (int, error) {
	return s.repo.getCountryTaxRate(ctx, s.pool, code)
}
