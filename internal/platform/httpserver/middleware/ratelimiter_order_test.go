package middleware_test

// Confirms the rate limiter runs after auth, so ByAuthenticatedSubject sees
// the JWT subject instead of silently falling back to client IP. Requires a
// real Redis — skips like every other Redis-backed test in this codebase
// (see platform/cache/cache_test.go) when TEST_REDIS_URL isn't set.

import (
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
