package billing

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// NewHandlerEngine wires billing handlers with no service and fixed context.
// Only use for binding/validation tests — service calls will panic.
func NewHandlerEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_owner"})
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: "ws_01", Slug: "acme", Name: "Acme", Status: "active", OwnerID: "sub_owner",
		})
		c.Next()
	})
	h := &handler{svc: nil}
	e.PATCH("/billing/plan", h.changePlan)
	e.POST("/billing/cancel", h.cancelSubscription)
	e.POST("/billing/invoices/:invoiceID/pay", h.createPaymentLink)
	e.POST("/billing/usage", h.recordUsage)
	e.POST("/webhooks/stripe", h.handleStripeWebhook)
	e.POST("/webhooks/xendit", h.handleXenditWebhook)
	return e
}

// NewHandlerEngineWithCaller wires billing handlers with a caller that differs
// from the organization owner — use for ownership-check tests.
func NewHandlerEngineWithCaller(callerSub, ownerSub string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	role := contracts.RoleMember
	if callerSub == ownerSub {
		role = contracts.RoleOwner
	}
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: callerSub})
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: "ws_01", Slug: "acme", Name: "Acme", Status: "active", OwnerID: ownerSub,
		})
		c.Set("organization.role", role)
		c.Next()
	})
	h := &handler{svc: nil}
	ownerOnly := middleware.RequireRole(contracts.RoleOwner)
	// GETs are member-visible; only mutations are owner-only — mirrors
	// module.go's Register exactly so RBAC tests exercise the real wiring.
	e.GET("/billing", h.getSubscription)
	e.GET("/billing/history", h.listHistory)
	e.GET("/billing/invoices", h.listInvoices)
	e.GET("/billing/payments", h.listPayments)
	e.GET("/billing/invoices/:invoiceID/pdf", h.invoicePDF)
	e.GET("/billing/payment-links", h.listPaymentLinks)
	e.GET("/billing/usage", h.getUsage)
	e.GET("/billing/features", h.listFeatures)
	e.GET("/billing/addons", h.listAddons)
	e.GET("/billing/preview", h.previewInvoice)
	e.GET("/billing/coupons", h.listEligibleCoupons)

	e.PATCH("/billing/plan", ownerOnly, h.changePlan)
	e.POST("/billing/cancel", ownerOnly, h.cancelSubscription)
	e.POST("/billing/resume", ownerOnly, h.resumeSubscription)
	e.POST("/billing/extend", ownerOnly, h.extendSubscription)
	e.POST("/billing/activate", ownerOnly, h.activateTrialNow)
	e.POST("/billing/invoices/:invoiceID/pay", ownerOnly, h.createPaymentLink)
	e.POST("/billing/invoices/:invoiceID/pay/regenerate", ownerOnly, h.regeneratePaymentLink)
	e.POST("/billing/usage", ownerOnly, h.recordUsage)
	e.POST("/billing/coupons/redeem", ownerOnly, h.redeemCoupon)
	e.POST("/billing/addons", ownerOnly, h.attachAddon)
	e.DELETE("/billing/addons/:addonID", ownerOnly, h.detachAddon)
	return e
}

// NewWebhookEngine creates a handler engine with explicit webhook secrets
// for testing signature verification paths.
func NewWebhookEngine(stripeSecret, xenditToken string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	h := &handler{svc: nil, stripeSecret: stripeSecret, xenditToken: xenditToken}
	e.POST("/webhooks/stripe", h.handleStripeWebhook)
	e.POST("/webhooks/xendit", h.handleXenditWebhook)
	return e
}

// referenceStub is the tax-rate concern billing consumes from outside its
// own schema (catalog data is billing's own schema — read via s.repo, never
// through an injected reader). Kept as its own named type since existing
// fakes (e.g. stubRefReader in integration_test.go) implement it alongside
// unrelated interfaces.
type referenceStub interface {
	contracts.CountryTaxReader
}

// NewModuleForTest creates a Module with a real pool and noop publisher. A
// nil ref leaves the tax rate unwired — tax defaults to 0 (see service.go's
// optional-dependency comments).
func NewModuleForTest(pool *pgxpool.Pool, ref referenceStub) *Module {
	return New(pool, messaging.NoopPublisher{}, ProviderConfig{}, ref, nil)
}

// NewWebhookModuleEngine creates a gin.Engine with both billing routes (authed)
// and public webhook routes (/webhooks/stripe, /webhooks/xendit), backed by a
// real DB. ProviderConfig is empty so webhook secrets are bypassed (dev mode).
func NewWebhookModuleEngine(pool *pgxpool.Pool, authSub, organizationID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := NewModuleForTest(pool, nil)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: organizationID, Slug: "test-ws", Name: "Test WS",
			Status: "active", OwnerID: authSub,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})
	mod.RegisterWebhooks(e.Group("/webhooks"))
	return e
}

// NewModuleEngine creates a full gin.Engine backed by a real DB module.
// Pass an optional referenceStub as a 4th argument to enable plan-dependent
// routes (changePlan, resumeSubscription from expired state).
func NewModuleEngine(pool *pgxpool.Pool, authSub, organizationID string, ref ...referenceStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var r referenceStub
	if len(ref) > 0 {
		r = ref[0]
	}
	mod := NewModuleForTest(pool, r)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: organizationID, Slug: "test-ws", Name: "Test WS",
			Status: "active", OwnerID: authSub,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW, RLS: noopMW, MFA: noopMW})
	return e
}

// NewModuleEngineWithRealRLS is NewModuleEngine but wires the real
// per-request RLS transaction middleware instead of a no-op — needed for
// tests that exercise behavior which only holds under an actual
// transaction (e.g. row locks held for a request's duration), since every
// other test engine in this package fakes RLS away.
func NewModuleEngineWithRealRLS(pool *pgxpool.Pool, authSub, organizationID string, ref ...referenceStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var r referenceStub
	if len(ref) > 0 {
		r = ref[0]
	}
	mod := NewModuleForTest(pool, r)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: organizationID, Slug: "test-ws", Name: "Test WS",
			Status: "active", OwnerID: authSub,
		})
		c.Set("organization.role", contracts.RoleOwner)
		c.Next()
	}
	noopMW := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{
		Auth: authMW, RateLimit: noopMW, Org: orgMW, Idempotency: noopMW,
		RLS: middleware.NewRLSTxMiddleware(pool), MFA: noopMW,
	})
	return e
}
