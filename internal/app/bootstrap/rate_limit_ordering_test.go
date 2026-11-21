package bootstrap_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/modules/organization"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
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
	orgMod := organization.New(pool, testWebhookEncryptionKey, "", 1)
	billingMod := billing.New(pool, billing.ProviderConfig{},
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

func TestWebhookRateLimit_Ceiling(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL required")
	}

	redisClient, err := cache.NewClient(t.Context(), config.RedisConfig{URL: redisURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	ip1 := fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
	ip2 := fmt.Sprintf("%d.%d.%d.%d", b[4], b[5], b[6], b[7])

	gin.SetMode(gin.TestMode)
	e := gin.New()

	// Mimic the NewAPIRouter webhook setup
	// We use 10 to keep the test fast, verifying the middleware enforces
	// whatever ceiling we give it without doing 6000 real HTTP round trips.
	webhookRateMW := middleware.NewRateLimitMiddleware(
		cache.NewRateLimiter(redisClient, "webhooks"),
		middleware.ByClientIP,
		cache.PerMinute(10),
	)

	webhooks := e.Group("/webhooks")
	webhooks.Use(webhookRateMW)
	webhooks.POST("/dummy", func(c *gin.Context) { c.Status(200) })

	fire := func(ip string) int {
		req := httpserver.JSONTestRequest(http.MethodPost, "/webhooks/dummy", "")
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w.Code
	}

	// Burn 10 requests from IP 1
	for i := range 10 {
		if code := fire(ip1); code != 200 {
			t.Fatalf("request %d failed early: got %d, want 200", i, code)
		}
	}

	// Request 11 from IP 1 should 429
	if code := fire(ip1); code != http.StatusTooManyRequests {
		t.Errorf("request 11 from IP 1: got %d, want 429", code)
	}

	// Request 1 from IP 2 should succeed (limit is per-IP)
	if code := fire(ip2); code != 200 {
		t.Errorf("request 1 from IP 2: got %d, want 200", code)
	}
}

// TestUnpaidActivation_GateRunsAfterRateLimit wires the real organization routes, the
// real billing read and the real limiter, in the order the API composes them. The
// gate costs a transaction, so a refused write must still count against the limiter. A
// non-member must learn nothing about the organization's billing, and paying must lift
// the gate on the next request.
func TestUnpaidActivation_GateRunsAfterRateLimit(t *testing.T) {
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

	const (
		ownerSub   = "owner_activation_gate_test"
		outsider   = "outsider_activation_gate_test"
		encryption = "test-webhook-secret-encryption-key-0000"
	)
	orgMod := organization.New(pool, encryption, "", 1)
	billingMod := billing.New(pool, billing.ProviderConfig{}, nil, nil, nil)

	gin.SetMode(gin.TestMode)
	e := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	api := e.Group("/api")
	orgMod.Register(api, httpserver.RouteDeps{
		Auth: func(c *gin.Context) {
			c.Set("auth.claims", middleware.Claims{Subject: c.GetHeader("X-Test-Subject")})
			c.Next()
		},
		Org: middleware.NewOrganizationMiddleware(orgMod),
		RateLimit: middleware.NewRateLimitMiddleware(
			cache.NewRateLimiter(redisClient, "test-activation-gate"),
			middleware.ByAuthenticatedSubject, cache.PerMinute(60),
		),
		ActivationGate: middleware.NewActivationGateMiddleware(billingMod),
		RLS:            noop, Idempotency: noop, MFA: noop,
	})

	do := func(method, path, body, subject string) *httptest.ResponseRecorder {
		req := httpserver.JSONTestRequest(method, path, body)
		req.Header.Set("X-Test-Subject", subject)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}

	created := do(http.MethodPost, "/api/organizations", `{"slug":"activation-gate-test","name":"Gate Test","plan":"solo","cycle":"monthly"}`, ownerSub)
	if created.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d: %s", created.Code, created.Body)
	}
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	orgID := resp.Data.ID
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM billing.invoices WHERE subscription_id IN (
			SELECT id FROM billing.subscriptions WHERE subject_id = $1)`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM organization.organizations WHERE id = $1`, orgID)
	})
	var invoiceID string
	if err := pool.QueryRow(t.Context(), `
		WITH sub AS (
			INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, currency, period_start, period_end)
			VALUES ('organization', $1, 'solo', 'active', 'USD', now(), now() + interval '1 month')
			RETURNING id
		)
		INSERT INTO billing.invoices (subscription_id, amount_cents, currency, kind)
		SELECT id, 900, 'USD', 'activation' FROM sub RETURNING id`, orgID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed unpaid activation: %v", err)
	}

	path := "/api/organizations/" + orgID
	patch := `{"name":"Renamed"}`

	blocked := do(http.MethodPatch, path, patch, ownerSub)
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "ACTIVATION_UNPAID") {
		t.Fatalf("write while unpaid: want 403 ACTIVATION_UNPAID, got %d: %s", blocked.Code, blocked.Body)
	}
	if blocked.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("a refused write must count against the limiter, want X-RateLimit-Limit set")
	}

	nonMember := do(http.MethodPatch, path, patch, outsider)
	if nonMember.Code != http.StatusForbidden || strings.Contains(nonMember.Body.String(), "ACTIVATION_UNPAID") {
		t.Errorf("non-member: want a plain 403, got %d: %s", nonMember.Code, nonMember.Body)
	}

	if read := do(http.MethodGet, path, "", ownerSub); read.Code != http.StatusOK {
		t.Errorf("read while unpaid: want 200, got %d: %s", read.Code, read.Body)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE billing.invoices SET status = 'paid', paid_at = now() WHERE id = $1`, invoiceID); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid := do(http.MethodPatch, path, patch, ownerSub); paid.Code != http.StatusOK {
		t.Errorf("write after paying: want 200, got %d: %s", paid.Code, paid.Body)
	}
}
