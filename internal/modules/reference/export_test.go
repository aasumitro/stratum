package reference

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// NewHandlerEngine returns a gin.Engine with reference handlers wired and
// no real DB. Routes that reach the service will panic — only use for
// request-level validation tests.
func NewHandlerEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_test"})
		c.Next()
	})
	h := &handler{svc: nil}
	e.GET("/references/countries", h.listCountries)
	e.GET("/references/currencies", h.listCurrencies)
	e.GET("/references/plans", h.listPlans)
	e.GET("/references/features", h.listFeatures)
	e.GET("/references/addons", h.listAddons)
	return e
}

// NewModuleForTest creates a Module backed by a real pool.
func NewModuleForTest(pool *pgxpool.Pool) *Module {
	return New(pool)
}

// NewModuleEngine creates a full gin.Engine backed by a real DB module. The
// catalog routes (plans/features/addons) are backed by a real, self-wired
// billing.Module against the same pool — reference no longer owns that data
// (see billing/repository.go's "catalog" section), so exercising these HTTP
// endpoints end to end needs a real catalog source, not a hand-rolled stub.
func NewModuleEngine(pool *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool)
	catalogMod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, nil, nil)
	mod.SetCatalogReader(catalogMod)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_test"})
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW})
	return e
}
