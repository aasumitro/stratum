package organization

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// AllowLoopbackWebhooksForTest relaxes the webhook SSRF guard (https-only,
// public-IP-only) so tests can use httptest.Server — a loopback http
// address — as a stand-in webhook receiver. Mutates package-level state;
// call once at test-package init, not mid-test.
func AllowLoopbackWebhooksForTest() {
	validateWebhookURL = func(context.Context, string) error { return nil }
	webhookHTTPClient = http.DefaultClient
}

// NewHandlerEngine returns a gin.Engine with organization handlers wired and no
// service. Auth and organization context are injected as fixed test values.
// Routes that reach the service will panic — only use for binding/validation.
// ownerID: sets ws.OwnerID and, when equal to "sub_caller", grants the caller owner role.
func NewHandlerEngine(ownerID string) *gin.Engine {
	role := contracts.RoleMember
	if ownerID == "sub_caller" {
		role = contracts.RoleOwner
	}
	return NewHandlerEngineWith(ownerID, role)
}

// NewHandlerEngineWith returns a handler engine with explicit ownerID and callerRole,
// so tests can simulate callers who are admins but not the organization owner.
func NewHandlerEngineWith(ownerID, callerRole string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_caller"})
		c.Set("organization.organization", contracts.OrganizationInfo{
			ID: "ws_01", Slug: "acme", Name: "Acme", Status: "active", OwnerID: ownerID,
		})
		c.Set("organization.role", callerRole)
		c.Next()
	})
	h := &handler{svc: nil, pool: nil, cacheInval: nil}
	ownerOnly := middleware.RequireRole(contracts.RoleOwner)
	adminUp := middleware.RequireRole(contracts.RoleOwner, contracts.RoleAdmin)
	// organization CRUD
	e.POST("/organizations", h.createOrganization)
	e.GET("/organizations", h.listOrganizations)
	e.GET("/organizations/ws_01", h.getOrganization)
	e.PATCH("/organizations/ws_01", ownerOnly, h.updateOrganization)
	e.DELETE("/organizations/ws_01", ownerOnly, h.deleteOrganization)
	e.PATCH("/organizations/ws_01/settings", ownerOnly, h.updateSettings)
	e.POST("/organizations/ws_01/transfer", ownerOnly, h.transferOwnership)
	e.POST("/organizations/ws_01/suspend", ownerOnly, h.suspendOrganization)
	e.POST("/organizations/ws_01/unsuspend", ownerOnly, h.unsuspendOrganization)
	// members
	e.GET("/organizations/ws_01/members", h.listMembers)
	e.POST("/organizations/ws_01/members", adminUp, h.addMember)
	e.POST("/organizations/ws_01/members/import", adminUp, h.importMembers)
	e.DELETE("/organizations/ws_01/members/:authSub", adminUp, h.removeMember)
	e.PATCH("/organizations/ws_01/members/:authSub/role", adminUp, h.updateMemberRole)
	// invitations
	e.GET("/organizations/ws_01/invitations", adminUp, h.listInvitations)
	e.POST("/organizations/ws_01/invitations", adminUp, h.createInvitation)
	e.GET("/invitations/preview", h.previewInvitation)
	e.POST("/invitations/accept", h.acceptInvitation)
	e.POST("/invitations/decline", h.declineInvitation)
	e.POST("/invitations/request-new", h.requestNewInvitation)
	e.GET("/me/invitations", h.listMyInvitations)
	// invite code
	e.PATCH("/organizations/ws_01/invite-code", ownerOnly, h.toggleInviteCode)
	e.GET("/organizations/join/preview", h.previewInviteCode)
	e.POST("/organizations/join", h.joinByCode)
	// audit
	e.GET("/organizations/ws_01/audit-log", adminUp, h.auditLog)
	e.GET("/organizations/ws_01/audit-log/export", adminUp, h.exportAuditLog)
	return e
}

// NewModuleForTest creates a Module with a real pool and noop publisher.
func NewModuleForTest(pool *pgxpool.Pool) *Module {
	return New(pool, messaging.NoopPublisher{})
}

// NewModuleEngine creates a full gin.Engine backed by a real DB module.
func NewModuleEngine(pool *pgxpool.Pool, authSub string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithPublisher is NewModuleEngine but wires the given
// publisher instead of a no-op, and also returns the Module — use for tests
// that need to capture published events (e.g. WebhookRetryRequested) and
// feed them into a Worker handler directly, simulating what the real
// RabbitMQ consumer would do without needing a broker in this test binary.
func NewModuleEngineWithPublisher(pool *pgxpool.Pool, authSub string, pub messaging.EventPublisher) (*gin.Engine, *Module) {
	gin.SetMode(gin.TestMode)
	mod := New(pool, pub)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e, mod
}

// NewModuleEngineWithEmail is NewModuleEngine plus verified JWT "email"/
// "user_metadata.email_verified" raw claims — use for invitation-accept/
// preview and GET /me/invitations tests, which read claims.Raw the same
// way a real Supabase JWT would (Supabase nests email_verified under
// user_metadata, it never sets a top-level claim). Simulates a caller who
// owns and has verified email.
func NewModuleEngineWithEmail(pool *pgxpool.Pool, authSub, email string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": email, "user_metadata": map[string]any{"email_verified": true}}})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithUnverifiedEmail is NewModuleEngineWithEmail but the
// nested "user_metadata.email_verified" claim is false — use to test that
// invitation accept/preview reject a caller who hasn't verified the email
// their token carries, even when it matches the invitation.
func NewModuleEngineWithUnverifiedEmail(pool *pgxpool.Pool, authSub, email string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": email, "user_metadata": map[string]any{"email_verified": false}}})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithWriter creates a full gin.Engine backed by a real DB module
// with a billing writer wired in — use for usage-recording integration tests.
func NewModuleEngineWithWriter(pool *pgxpool.Pool, authSub string, bw contracts.BillingWriter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	mod.SetBillingWriter(bw)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithWriterAndEmail is NewModuleEngineWithWriter plus a
// verified JWT "email"/"user_metadata.email_verified" claim — use for
// invitation-accept tests that also need a billing writer wired in.
func NewModuleEngineWithWriterAndEmail(pool *pgxpool.Pool, authSub, email string, bw contracts.BillingWriter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	mod.SetBillingWriter(bw)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": email, "user_metadata": map[string]any{"email_verified": true}}})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithUserReaderAndEmail is NewModuleEngineWithUserReader plus
// a verified JWT "email"/"user_metadata.email_verified" claim — use for
// invitation-preview/accept tests that need both a resolvable inviter
// profile (name/email) and a caller identity matching the invitation.
func NewModuleEngineWithUserReaderAndEmail(pool *pgxpool.Pool, authSub, email string, ur contracts.UserReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	mod.SetUserReader(ur)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": email, "user_metadata": map[string]any{"email_verified": true}}})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithCatalogReader creates a full gin.Engine backed by a
// real DB module with a catalog reader wired in — use for plan-validation
// integration tests on organization creation.
func NewModuleEngineWithCatalogReader(pool *pgxpool.Pool, authSub string, cr contracts.CatalogReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	mod.SetCatalogReader(cr)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}

// NewModuleEngineWithUserReader creates a full gin.Engine backed by a real
// DB module with a user reader wired in — use for integration tests on the
// batch profile-enrichment path (GET .../members).
func NewModuleEngineWithUserReader(pool *pgxpool.Pool, authSub string, ur contracts.UserReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mod := New(pool, messaging.NoopPublisher{})
	mod.SetUserReader(ur)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub})
		c.Next()
	}
	orgMW := middleware.NewOrganizationMiddleware(mod)
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, Org: orgMW, MFA: noopGate})
	return e
}
