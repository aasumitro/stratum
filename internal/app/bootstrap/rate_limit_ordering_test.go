package bootstrap_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

// TestNonMember_RateLimitHeaderNeverLeaksOrgTier guards each module's
// route-group middleware order: Org must run before RateLimit on every
// :organizationID-scoped group. If RateLimit ran first, a non-member's
// request against a billing- or organization-scoped route would consume
// the target org's plan rate limit and stamp X-RateLimit-Limit on the
// response before the 403 — leaking that org's plan tier to a caller who
// was never a member.
//
// This wires the real Module.Register (both billing and organization),
// the real middleware.NewOrganizationMiddleware, and the real
// middleware.NewRateLimitMiddleware — the exact three things api_router.go
// composes in production — rather than a stand-in, so a future reordering
// regression in either module's Register trips this test. It deliberately
// skips the rest of NewAPIRouter's wiring (CORS, audit, geoip, live auth)
// since none of that participates in the property under test.
func TestNonMember_RateLimitHeaderNeverLeaksOrgTier(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL required")
	}

	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	redisClient, err := cache.NewClient(t.Context(), config.RedisConfig{URL: redisURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	const ownerSub = "owner_rl_hdr_test"
	const outsiderSub = "outsider_rl_hdr_test"
	// Duplicated rather than shared with organization's own unexported
	// testSecretEncryptionKey — this package cannot see it, and the actual
	// value is irrelevant here since no test in this file exercises
	// webhook encrypt/decrypt.
	const testWebhookEncryptionKey = "test-webhook-secret-encryption-key-0000"

	// Real modules, built from their exported constructors only — this
	// package (bootstrap) is the one place both are meant to be wired
	// together, matching NewAPIModules in production.
	orgMod := organization.New(pool, messaging.NoopPublisher{}, testWebhookEncryptionKey)
	billingMod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{},
		nil, nil, nil)

	gin.SetMode(gin.TestMode)
	e := gin.New()
	// Subject comes from a header instead of a fixed claim so the same
	// engine can create the org as its owner, then probe it as an
	// outsider — RateLimit only needs to be real for the outsider calls,
	// but wiring it identically for both keeps this one engine instead of
	// two copies of the same registration.
	authMW := func(c *gin.Context) {
		c.Set("auth.claims", middleware.Claims{Subject: c.GetHeader("X-Test-Subject")})
		c.Next()
	}
	noop := func(c *gin.Context) { c.Next() }
	deps := httpserver.RouteDeps{
		Auth: authMW,
		Org:  middleware.NewOrganizationMiddleware(orgMod),
		RateLimit: middleware.NewRateLimitMiddleware(
			cache.NewRateLimiter(redisClient, "test-rl-ordering"),
			middleware.ByAuthenticatedSubject, cache.PerMinute(60),
		),
		RLS: noop, Idempotency: noop, MFA: noop,
	}
	api := e.Group("/api")
	orgMod.Register(api, deps)
	billingMod.Register(api, deps)

	newReq := func(method, path, body, subject string) *http.Request {
		req := httpserver.JSONTestRequest(method, path, body)
		req.Header.Set("X-Test-Subject", subject)
		return req
	}

	wCreate := httptest.NewRecorder()
	e.ServeHTTP(wCreate, newReq(http.MethodPost, "/api/organizations",
		`{"slug":"rl-hdr-test","name":"RL Header Test","plan":"solo","cycle":"monthly"}`, ownerSub))
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", wCreate.Code, wCreate.Body)
	}
	var createResp map[string]any
	if err := json.NewDecoder(wCreate.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	orgID := createResp["data"].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})

	cases := []struct {
		name string
		path string
	}{
		{"billing-scoped route", "/api/organizations/" + orgID + "/billing"},
		{"organization-scoped route", "/api/organizations/" + orgID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			e.ServeHTTP(w, newReq(http.MethodGet, tc.path, "", outsiderSub))
			if w.Code != http.StatusForbidden {
				t.Fatalf("non-member request: want 403, got %d: %s", w.Code, w.Body)
			}
			if hdr := w.Header().Get("X-RateLimit-Limit"); hdr != "" {
				t.Errorf("X-RateLimit-Limit must be absent on a non-member's 403 (Org must run before RateLimit), got %q", hdr)
			}
		})
	}
}
