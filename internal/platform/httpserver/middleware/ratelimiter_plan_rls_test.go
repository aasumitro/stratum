package middleware_test

// Regression test for NewRateLimitMiddleware's organization-scoped branch,
// which calls contracts.BillingReader.GetSubscriptionBySubject with no
// ambient RLS transaction — this middleware sits outside every module's own
// route group, so it never carries a querier in context. Connects as the
// RLS-constrained app role (POSTGRES_APP_URL) for the subscription seed and
// the call under test, and requires a real Redis (TEST_REDIS_URL, same
// convention as ratelimiter_order_test.go) since resolving the correct
// limit is only observable end-to-end via limiter.Allow's response headers.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/messaging"
)

func testAppPoolRateLimit(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_APP_URL")
	if dsn == "" {
		t.Skip("POSTGRES_APP_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupRateLimitFixtures deletes this test's rows. billing.subscriptions
// is FORCE RLS, so its delete must run with org context set in the same
// transaction — a plain pool.Exec as the app role would silently affect 0
// rows and leave the fixture behind for the next run.
func cleanupRateLimitFixtures(pool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	_ = db.WithTx(ctx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
		return err
	})
}

// TestRateLimitMiddleware_ResolvesRealPlanRate_NoAmbientTx verifies the
// rate limiter resolves a real plan's rate with no ambient billing
// transaction: a real, active Growth-plan subscription (whose catalog
// entry lists api_rate_limit=720 — db/migrations/000004_billing.up.sql)
// must not fall back to the flat defaultLimit (300) just because
// GetSubscriptionBySubject's own lookup runs with no org context set — the
// middleware's own fail-open would otherwise silently mask the lookup
// error as "no subscription found". Must resolve X-RateLimit-Limit: 720
// once the lookup routes a bare
// context through withOrgTx.
func TestRateLimitMiddleware_ResolvesRealPlanRate_NoAmbientTx(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	pool := testAppPoolRateLimit(t)

	const orgID = "00000000-0000-0000-0000-0000000c1201"
	cleanupRateLimitFixtures(pool, orgID)
	t.Cleanup(func() { cleanupRateLimitFixtures(pool, orgID) })

	ctx := context.Background()

	// Seed the subscription the way the interactive flow does: inside a
	// transaction with org context set, via the same RLS-constrained role
	// GetSubscriptionBySubject itself will run as.
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(0, 1, 0)
	if err := db.WithTx(ctx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO billing.subscriptions (subject_type, subject_id, plan, status, currency, period_start, period_end)
			VALUES ('organization', $1, 'growth', 'active', 'USD', $2, $3)`,
			orgID, periodStart, periodEnd)
		return err
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	redisClient, err := cache.NewClient(t.Context(), config.RedisConfig{URL: redisURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	billingMod := billing.New(pool, messaging.NoopPublisher{}, billing.ProviderConfig{}, nil, nil, nil)

	gin.SetMode(gin.TestMode)
	e := gin.New()
	limiter := cache.NewRateLimiter(redisClient, "test-rl-plan")
	rateMW := middleware.NewRateLimitMiddleware(limiter, middleware.ByClientIP, cache.PerMinute(300),
		middleware.RateLimitCatalog{Billing: billingMod, Catalog: billingMod})
	e.GET("/organizations/:organizationID/x", rateMW, func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/organizations/"+orgID+"/x", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("request: want 200, got %d: %s", w.Code, w.Body)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "720" {
		t.Errorf("X-RateLimit-Limit = %q, want \"720\" (Growth's real catalog rate) — GetSubscriptionBySubject was blocked by FORCE RLS with no org context set, falling back to the flat defaultLimit", got)
	}
}
