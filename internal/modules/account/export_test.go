package account

import (
	"github.com/gin-gonic/gin"
	jwtgo "github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/storage"
)

// NewHandlerEngine returns a gin.Engine with profile handlers wired and no
// service. Only use for binding/validation tests — routes that reach the
// service will panic.
func NewHandlerEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: "sub_owner"})
		c.Next()
	})
	h := &handler{svc: nil}
	e.POST("/me", h.upsertProfile)
	e.PATCH("/me", h.updateProfile)
	e.PATCH("/me/preferences", h.updatePreferences)
	e.POST("/me/password/changed", h.recordPasswordChanged)
	return e
}

// NewModuleForTest creates a Module with a real pool and noop publisher.
func NewModuleForTest(pool *pgxpool.Pool) *Module {
	return New(pool, messaging.NoopPublisher{}, "", "", nil, nil, "")
}

// NewModuleForTestWithStore is NewModuleForTest but also wires a storage
// client — needed to exercise executeDeleteAccount's avatar-blob cleanup
// step against a fake/failing storage backend.
func NewModuleForTestWithStore(pool *pgxpool.Pool, store *storage.Client) *Module {
	return New(pool, messaging.NoopPublisher{}, "", "", nil, store, "")
}

// NewModuleForTestWithAdminAndRedis is NewModuleForTest but also wires a
// Supabase Admin URL/service-role key and a real Redis namespace — needed
// to exercise RecordLoginEvent's MFA-sync side effect, which is gated
// behind both (recordLoginEvent early-returns with a nil revokedNS, and
// syncMFAStatusOnLogin early-returns with no admin creds configured).
func NewModuleForTestWithAdminAndRedis(pool *pgxpool.Pool, adminURL, serviceRoleKey string, redis *goredis.Client) *Module {
	return New(pool, messaging.NoopPublisher{}, adminURL, serviceRoleKey, cache.NewNamespace(redis, "account_test"), nil, "")
}

// NewModuleEngine creates a full gin.Engine backed by a real DB module,
// with a real organization.Module wired as the organization reader — tests
// exercising DELETE /me (owned-organization gate) or POST /me/export
// (GDPR data-export) need a real cross-module read against whatever they
// seed into organization.organizations, not a hand-rolled stub. Carries a
// verified email claim derived from authSub, matching what a real Supabase
// JWT would have, so POST /me succeeds — use NewModuleEngineWithEmail instead when a
// test asserts the resulting email's exact value.
func NewModuleEngine(pool *pgxpool.Pool, authSub string) *gin.Engine {
	return NewModuleEngineWithEmail(pool, authSub, authSub+"@test.local")
}

// NewModuleEngineWithEmail is NewModuleEngine with an explicit email claim
// — use when a test asserts the profile's resulting email value.
func NewModuleEngineWithEmail(pool *pgxpool.Pool, authSub, email string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": email, "email_verified": true}})
		c.Next()
	}
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	mod := New(pool, messaging.NoopPublisher{}, "", "", nil, nil, "")
	// Duplicated rather than shared with organization's own unexported
	// testSecretEncryptionKey — this package (account) cannot see it, and
	// the actual value is irrelevant here since no test in this file
	// exercises webhook encrypt/decrypt.
	const testWebhookEncryptionKey = "test-webhook-secret-encryption-key-0000"
	mod.SetOrganizationReader(organization.New(pool, messaging.NoopPublisher{}, testWebhookEncryptionKey))
	mod.Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, MFA: noopGate})
	return e
}

// NewModuleEngineWithAdmin creates a full gin.Engine backed by a real DB
// module, wired with the given Supabase Admin URL/service-role key — use for
// tests that need to hit a fake Supabase server (e.g. MFA status sync).
// Carries a derived verified email claim, same reasoning as NewModuleEngine.
func NewModuleEngineWithAdmin(pool *pgxpool.Pool, authSub, adminURL, serviceRoleKey string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: authSub, Raw: jwtgo.MapClaims{"email": authSub + "@test.local", "email_verified": true}})
		c.Next()
	}
	noopGate := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	New(pool, messaging.NoopPublisher{}, adminURL, serviceRoleKey, nil, nil, "").
		Register(api, httpserver.RouteDeps{Auth: authMW, RateLimit: noopGate, MFA: noopGate})
	return e
}

// NewWebhookModuleEngine creates a gin.Engine with only the public webhook
// routes mounted (no /me routes, no auth middleware) — mirrors how
// internal/app/api.go wires RegisterWebhooks onto a separate router group.
func NewWebhookModuleEngine(pool *pgxpool.Pool, webhookSecret string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	webhooks := e.Group("/webhooks")
	New(pool, messaging.NoopPublisher{}, "", "", nil, nil, webhookSecret).RegisterWebhooks(webhooks)
	return e
}
