package reference

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/geoip"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

// Module owns static/semi-static lookup data: countries, currencies, and
// country tax rates. Implements contracts.CountryTaxReader so the billing
// module can resolve a country's tax rate without importing this package
// directly. The plan/feature/addon catalog lives in billing (the schema
// owner) — this module only serves those HTTP routes, delegating to an
// injected contracts.CatalogReader (see SetCatalogReader).
type Module struct {
	svc             *service
	countryResolver *geoip.Resolver
}

func New(pool *pgxpool.Pool) *Module {
	return &Module{svc: &service{repo: &repository{}, pool: pool}}
}

// SetCatalogReader wires the catalog data source (plans/features/addons)
// after construction — always the billing module in production, since it
// owns the billing schema. Called from main.go after the billing module is
// created, mirroring the existing nil-safe Set* convention.
func (m *Module) SetCatalogReader(r contracts.CatalogReader) {
	m.svc.catalog = r
}

// SetCountryResolver wires the GeoIP resolver after construction — the
// trusted source GET /references/plans|addons resolves a caller's currency
// from, replacing the client-supplied country_code query param they used to
// accept. Nil-safe: unwired, listPlans/listAddons fall back to "US" the
// same way createOrganization does (see organization.Module.SetCountryResolver).
func (m *Module) SetCountryResolver(r *geoip.Resolver) {
	m.countryResolver = r
}

// GetCountryTaxRate implements contracts.CountryTaxReader.
func (m *Module) GetCountryTaxRate(ctx context.Context, countryCode string) (int, error) {
	return m.svc.getCountryTaxRate(ctx, countryCode)
}

// Register mounts reference routes onto r.
//
//	GET /references/countries
//	GET /references/currencies
//	GET /references/plans
//	GET /references/features
//	GET /references/addons
func (m *Module) Register(r *gin.RouterGroup, deps httpserver.RouteDeps) {
	h := &handler{svc: m.svc, countryResolver: m.countryResolver}

	ref := r.Group("/references")
	ref.Use(deps.Auth, deps.RateLimit)
	{
		ref.GET("/countries", h.listCountries)
		ref.GET("/currencies", h.listCurrencies)
		ref.GET("/plans", h.listPlans)
		ref.GET("/features", h.listFeatures)
		ref.GET("/addons", h.listAddons)
	}
}
