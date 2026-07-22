package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/cache"
	"github.com/aasumitro/stratum/internal/platform/config"
	dbpkg "github.com/aasumitro/stratum/internal/platform/db"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	c, err := cache.NewClient(t.Context(), config.RedisConfig{URL: url})
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func testDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := dbpkg.NewPostgresPool(t.Context(), config.PostgresConfig{URL: dsn, MaxOpenConns: 3, MaxIdleTime: time.Minute})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// --- idempotency ---

func idempotencyEngine(ns *cache.Namespace, calls *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.POST("/pay", NewIdempotencyMiddleware(ns), func(c *gin.Context) {
		*calls++
		c.JSON(http.StatusOK, gin.H{"charged": true, "n": *calls})
	})
	return e
}

func TestIdempotency_NoKey_PassesThrough(t *testing.T) {
	ns := cache.NewNamespace(testRedis(t), "idem-nokey")
	calls := 0
	e := idempotencyEngine(ns, &calls)

	for range 2 {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(`{}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
	}
	if calls != 2 {
		t.Errorf("without an Idempotency-Key both requests should execute, calls = %d", calls)
	}
}

func TestIdempotency_ReplaysCachedResponse(t *testing.T) {
	ns := cache.NewNamespace(testRedis(t), "idem-replay-"+time.Now().Format("150405.000000"))
	calls := 0
	e := idempotencyEngine(ns, &calls)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(`{"amount":100}`))
		req.Header.Set("Idempotency-Key", "key-abc")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w
	}

	first := do()
	second := do()
	if calls != 1 {
		t.Errorf("handler should run once; replay served from cache, calls = %d", calls)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("replay body mismatch: %q vs %q", first.Body, second.Body)
	}
}

func TestIdempotency_SameKeyDifferentBody_409(t *testing.T) {
	ns := cache.NewNamespace(testRedis(t), "idem-conflict-"+time.Now().Format("150405.000000"))
	calls := 0
	e := idempotencyEngine(ns, &calls)

	req1 := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(`{"amount":100}`))
	req1.Header.Set("Idempotency-Key", "key-x")
	w1 := httptest.NewRecorder()
	e.ServeHTTP(w1, req1)

	// same key, different body → 409
	req2 := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(`{"amount":999}`))
	req2.Header.Set("Idempotency-Key", "key-x")
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("same key + different body: want 409, got %d", w2.Code)
	}
}

func TestIdempotency_InFlightLock_409(t *testing.T) {
	ns := cache.NewNamespace(testRedis(t), "idem-lock-"+time.Now().Format("150405.000000"))
	calls := 0
	e := idempotencyEngine(ns, &calls)

	// simulate an in-flight original request by pre-holding the lock key
	key := idempotencyCacheKey("", http.MethodPost, "/pay", "key-inflight")
	if err := ns.Set(t.Context(), key+":lock", "1", time.Minute); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/pay", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "key-inflight")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("concurrent duplicate (lock held): want 409, got %d", w.Code)
	}
	if calls != 0 {
		t.Errorf("handler must not run while a duplicate is in flight, calls = %d", calls)
	}
}

func TestIdempotencyCacheKey_Scoping(t *testing.T) {
	a := idempotencyCacheKey("user1", "POST", "/pay", "k")
	b := idempotencyCacheKey("user2", "POST", "/pay", "k")
	c := idempotencyCacheKey("user1", "POST", "/refund", "k")
	if a == b || a == c {
		t.Error("idempotency key must be scoped by subject and path")
	}
}

// --- rate limit middleware ---

func TestRateLimitMiddleware_AllowsThenBlocks(t *testing.T) {
	rl := cache.NewRateLimiter(testRedis(t), "rlmw-"+time.Now().Format("150405.000000"))
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/x", NewRateLimitMiddleware(rl, ByClientIP, cache.PerHour(1)), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "203.0.113.55:1111"
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w.Code
	}

	if code := do(); code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", code)
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Errorf("second request over the 1/hour limit: want 429, got %d", code)
	}
}

// --- RLS transaction middleware ---

func rlsEngineWithOrg(pool *pgxpool.Pool, orgID string, capture *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		if orgID != "" {
			c.Set(organizationContextKey, contracts.OrganizationInfo{ID: orgID, Status: "active"})
		}
		c.Next()
	})
	e.GET("/x", NewRLSTxMiddleware(pool), func(c *gin.Context) {
		// the middleware puts the RLS-scoped tx in context; read the setting back
		q := dbpkg.QuerierFromContext(c.Request.Context(), pool)
		var got string
		_ = q.QueryRow(c.Request.Context(), `SELECT current_setting('app.organization_id', true)`).Scan(&got)
		if capture != nil {
			*capture = got
		}
		c.Status(http.StatusOK)
	})
	return e
}

func TestRLSMiddleware_SetsOrgConfigInTx(t *testing.T) {
	pool := testDBPool(t)
	var seen string
	e := rlsEngineWithOrg(pool, "11111111-1111-1111-1111-111111111111", &seen)

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if seen != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("app.organization_id inside the tx = %q, want the org id", seen)
	}
}

func TestRLSMiddleware_NoOrg_PassesThrough(t *testing.T) {
	pool := testDBPool(t)
	e := rlsEngineWithOrg(pool, "", nil) // no org in context → middleware is a no-op

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("no org context: want pass-through 200, got %d", w.Code)
	}
}
