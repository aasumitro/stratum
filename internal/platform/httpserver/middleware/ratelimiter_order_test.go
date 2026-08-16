package middleware_test

// Confirms the rate limiter runs after auth, so ByAuthenticatedSubject sees
// the JWT subject instead of silently falling back to client IP. Requires a
// real Redis — skips like every other Redis-backed test in this codebase
// (see platform/cache/cache_test.go) when TEST_REDIS_URL isn't set.

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

func TestRateLimit_RunsAfterAuth_KeysBySubjectNotIP(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	redisClient, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	gin.SetMode(gin.TestMode)
	e := gin.New()
	limiter := cache.NewRateLimiter(redisClient, "test-rl-order")
	rateMW := middleware.NewRateLimitMiddleware(limiter, middleware.ByAuthenticatedSubject, cache.PerHour(1))

	// Mirrors the real wiring: an auth-equivalent middleware sets claims,
	// then the rate limiter — never the other way around (see
	// RouteDeps.RateLimit).
	e.GET("/x",
		func(c *gin.Context) {
			c.Set("auth.claims", middleware.Claims{Subject: c.GetHeader("X-Test-Subject")})
			c.Next()
		},
		rateMW,
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	suffix := time.Now().Format("150405.000000")
	do := func(subject, ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Test-Subject", subject+"-"+suffix)
		req.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w.Code
	}

	// Same subject, different IPs, limit=1/hr — the 2nd request must be
	// blocked because it shares the subject's bucket, proving the key came
	// from claims (post-auth) and not from ClientIP.
	if code := do("user-a", "10.0.0.1"); code != http.StatusOK {
		t.Fatalf("first request for user-a: want 200, got %d", code)
	}
	if code := do("user-a", "10.0.0.2"); code != http.StatusTooManyRequests {
		t.Errorf("same subject from a different IP: want 429 (shared subject bucket), got %d", code)
	}

	// A different subject must get its own bucket regardless of IP.
	if code := do("user-b", "10.0.0.1"); code != http.StatusOK {
		t.Errorf("different subject: want 200 (separate bucket), got %d", code)
	}
}

// TestByClientIP_KeyFunc_SharesBucketAcrossSubjects proves ByClientIP's
// failure mode: it collapses every caller behind the same address into one
// shared counter, regardless of who they are — a real risk for any route
// sitting behind a shared proxy or load balancer, where many distinct
// callers can resolve to the same client IP. See
// TestStripeWebhook_ManyUnsignedRequests_NeverRateLimited in
// internal/modules/billing/handler_test.go for a regression test against
// the actual webhook route/handler chain; this test only covers the
// ByClientIP primitive in isolation, same as its ByAuthenticatedSubject
// sibling above.
func TestByClientIP_KeyFunc_SharesBucketAcrossSubjects(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	redisClient, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	gin.SetMode(gin.TestMode)
	e := gin.New()
	limiter := cache.NewRateLimiter(redisClient, "test-rl-webhook")
	rateMW := middleware.NewRateLimitMiddleware(limiter, middleware.ByClientIP, cache.PerHour(1))

	e.GET("/webhooks", rateMW, func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/webhooks", nil)
		req.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w.Code
	}

	// ByClientIP ignores everything but the address, so the key must be
	// randomized per test run, not just per subject (which it never reads)
	// — a fixed IP would collide with the previous run's still-live bucket
	// under PerHour(1) and fail nondeterministically.
	sharedIP := fmt.Sprintf("10.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(256))
	otherIP := fmt.Sprintf("10.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(256))
	for otherIP == sharedIP {
		otherIP = fmt.Sprintf("10.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(256))
	}

	if code := do(sharedIP); code != http.StatusOK {
		t.Fatalf("first request for %s: want 200, got %d", sharedIP, code)
	}
	if code := do(sharedIP); code != http.StatusTooManyRequests {
		t.Errorf("second request from the same IP: want 429 (shared IP bucket), got %d", code)
	}

	// A different IP must get its own bucket.
	if code := do(otherIP); code != http.StatusOK {
		t.Errorf("different IP: want 200 (separate bucket), got %d", code)
	}
}
