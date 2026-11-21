package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/testsupport"
)

func TestRateLimit_RejectionIsCountedPerLimiter(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	rdb, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	reader := testsupport.ManualMetricReader(t)

	// A fresh namespace per run, because the bucket outlives the test in Redis.
	limiterName := fmt.Sprintf("test-rl-metric-%d", time.Now().UnixNano())
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(middleware.NewRateLimitMiddleware(
		cache.NewRateLimiter(rdb, limiterName),
		func(*gin.Context) string { return "same-key" },
		cache.PerMinute(1),
	))
	e.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	codes := make([]int, 0, 2)
	for range 2 {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		codes = append(codes, w.Code)
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want [200 429]", codes)
	}

	limiter := attribute.String("limiter", limiterName)
	if got := testsupport.CounterTotal(t, reader, "stratum.ratelimit.rejections", limiter); got != 1 {
		t.Errorf("rejections = %d, want 1", got)
	}
}
