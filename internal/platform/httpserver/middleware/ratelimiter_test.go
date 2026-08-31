package middleware

// Internal (package middleware) so resolvePlanRate — unexported — is directly
// testable, mirroring the pure_test.go convention used elsewhere in this
// codebase for unexported helpers. Cache is left nil throughout: this
// codebase has no Redis test harness anywhere (platform/cache has zero test
// files), so these tests cover the catalog-read replacement logic only, not
// the caching mechanics.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/cache"
)

type stubRateCatalogReader struct {
	plans map[string]contracts.PlanInfo
	err   error
}

func (s stubRateCatalogReader) GetPlanByID(_ context.Context, id string) (*contracts.PlanInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	p, ok := s.plans[id]
	if !ok {
		return nil, errors.New("plan not found")
	}
	return &p, nil
}
func (s stubRateCatalogReader) ListPlans(_ context.Context) ([]contracts.PlanInfo, error) {
	return nil, nil
}
func (s stubRateCatalogReader) ListFeatures(_ context.Context) ([]contracts.FeatureInfo, error) {
	return nil, nil
}
func (s stubRateCatalogReader) ListAddons(_ context.Context) ([]contracts.AddonInfo, error) {
	return nil, nil
}
func (s stubRateCatalogReader) GetAddonByID(_ context.Context, _ string) (*contracts.AddonInfo, error) {
	return nil, errors.New("addon not found")
}
func (s stubRateCatalogReader) ValidateCouponCode(_ context.Context, _, _ string) error {
	return errors.New("coupon not found")
}

func rawConfig(requestsPerMinute int) json.RawMessage {
	b, _ := json.Marshal(apiRateLimitConfig{RequestsPerMinute: requestsPerMinute})
	return b
}

func TestResolvePlanRate_ReadsCatalogConfigValue(t *testing.T) {
	rc := RateLimitCatalog{
		Catalog: stubRateCatalogReader{plans: map[string]contracts.PlanInfo{
			"growth": {ID: "growth", ConfigValues: map[string]json.RawMessage{
				"api_rate_limit": rawConfig(600),
			}},
		}},
	}

	rate, ok := rc.resolvePlanRate(t.Context(), "growth")
	if !ok {
		t.Fatal("want ok=true for plan with api_rate_limit config")
	}
	if rate != 600 {
		t.Errorf("want rate=600, got %d", rate)
	}
}

func TestResolvePlanRate_UnlimitedPlan_ReturnsNegative(t *testing.T) {
	rc := RateLimitCatalog{
		Catalog: stubRateCatalogReader{plans: map[string]contracts.PlanInfo{
			"custom": {ID: "custom", ConfigValues: map[string]json.RawMessage{
				"api_rate_limit": rawConfig(-1),
			}},
		}},
	}

	rate, ok := rc.resolvePlanRate(t.Context(), "custom")
	if !ok {
		t.Fatal("want ok=true")
	}
	if rate != -1 {
		t.Errorf("want rate=-1 (unlimited), got %d", rate)
	}
}

func TestResolvePlanRate_PlanLookupError_ReturnsNotOK(t *testing.T) {
	rc := RateLimitCatalog{Catalog: stubRateCatalogReader{err: errors.New("db down")}}

	if _, ok := rc.resolvePlanRate(t.Context(), "solo"); ok {
		t.Error("want ok=false on plan lookup error")
	}
}

func TestResolvePlanRate_NoConfigValue_ReturnsNotOK(t *testing.T) {
	rc := RateLimitCatalog{
		Catalog: stubRateCatalogReader{plans: map[string]contracts.PlanInfo{
			"solo": {ID: "solo"}, // no ConfigValues at all
		}},
	}

	if _, ok := rc.resolvePlanRate(t.Context(), "solo"); ok {
		t.Error("want ok=false when plan has no api_rate_limit entitlement")
	}
}

// TestRateLimitMiddleware_LimiterError_FailsOpenAndLogs covers the branch
// where limiter.Allow returns an error (Redis unreachable): the request
// must still be served (fail open) and the failure must be logged at
// ERROR so a limiter outage is not silent. The limiter is pointed at a
// dead address so Allow's dial fails without needing a Redis harness.
func TestRateLimitMiddleware_LimiterError_FailsOpenAndLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	deadClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = deadClient.Close() })
	limiter := cache.NewRateLimiter(deadClient, "test-rl-failopen")

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	handlerRan := false
	e := gin.New()
	e.Use(NewRateLimitMiddleware(limiter, ByClientIP, cache.PerMinute(1)))
	e.GET("/t", func(c *gin.Context) {
		handlerRan = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))

	if !handlerRan || w.Code != http.StatusOK {
		t.Fatalf("limiter error should fail open: handlerRan=%v, status=%d, want true/200", handlerRan, w.Code)
	}
	log := logBuf.String()
	if !strings.Contains(log, "level=ERROR") ||
		!strings.Contains(log, "ratelimit: allow check failed, letting request through") {
		t.Errorf("expected an ERROR log line for the fail-open branch, got: %q", log)
	}
}
