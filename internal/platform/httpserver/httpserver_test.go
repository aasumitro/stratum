package httpserver_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

func init() { gin.SetMode(gin.TestMode) }

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.NewPostgresPool(t.Context(), config.PostgresConfig{URL: dsn, MaxOpenConns: 3, MaxIdleTime: time.Minute})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNew_WiresBaselineMiddleware(t *testing.T) {
	e := httpserver.New("test-svc", slog.Default(), "test")
	e.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Fatalf("baseline engine: got %d %q", w.Code, w.Body)
	}
	// RequestID middleware is part of the baseline chain
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("baseline engine should attach X-Request-ID")
	}
}

func TestRegisterHealth_Liveness(t *testing.T) {
	e := gin.New()
	httpserver.RegisterHealth(e, httpserver.HealthDeps{})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("liveness: want 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("liveness status = %q, want ok", body["status"])
	}
}

func TestRegisterHealth_ReadyAndStats(t *testing.T) {
	pool := testPool(t)
	e := gin.New()
	httpserver.RegisterHealth(e, httpserver.HealthDeps{Pool: pool}) // redis/MQ nil → skipped

	// readiness: postgres healthy → 200
	wr := httptest.NewRecorder()
	e.ServeHTTP(wr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if wr.Code != http.StatusOK {
		t.Fatalf("readiness: want 200, got %d: %s", wr.Code, wr.Body)
	}
	var ready map[string]any
	json.Unmarshal(wr.Body.Bytes(), &ready)
	if checks, ok := ready["checks"].(map[string]any); !ok || checks["postgres"] != "healthy" {
		t.Errorf("readiness checks = %v, want postgres healthy", ready["checks"])
	}

	// stats: always 200 with pool + runtime info
	ws := httptest.NewRecorder()
	e.ServeHTTP(ws, httptest.NewRequest(http.MethodGet, "/health/stats", nil))
	if ws.Code != http.StatusOK {
		t.Fatalf("stats: want 200, got %d", ws.Code)
	}
	var stats map[string]any
	json.Unmarshal(ws.Body.Bytes(), &stats)
	if _, ok := stats["db_pool"]; !ok {
		t.Error("stats should include db_pool")
	}
}

func TestRegisterHealth_Stats_TokenGated(t *testing.T) {
	pool := testPool(t)
	e := gin.New()
	httpserver.RegisterHealth(e, httpserver.HealthDeps{Pool: pool, StatsToken: "s3cr3t"})

	// no token → 401
	w1 := httptest.NewRecorder()
	e.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/health/stats", nil))
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", w1.Code)
	}

	// wrong token → 401
	req2 := httptest.NewRequest(http.MethodGet, "/health/stats", nil)
	req2.Header.Set("X-Stats-Token", "wrong")
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", w2.Code)
	}

	// correct token → 200
	req3 := httptest.NewRequest(http.MethodGet, "/health/stats", nil)
	req3.Header.Set("X-Stats-Token", "s3cr3t")
	w3 := httptest.NewRecorder()
	e.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("correct token: want 200, got %d: %s", w3.Code, w3.Body)
	}

	// /health stays open regardless of StatsToken
	wl := httptest.NewRecorder()
	e.ServeHTTP(wl, httptest.NewRequest(http.MethodGet, "/health", nil))
	if wl.Code != http.StatusOK {
		t.Errorf("liveness must stay open: want 200, got %d", wl.Code)
	}
}

func TestRegisterHealth_Ready_NilPool_Unavailable(t *testing.T) {
	// A degraded dependency (nil pool → ping panics? no — nil *pgxpool.Pool
	// Ping returns an error) must produce 503. Use a closed pool to simulate.
	pool := testPool(t)
	pool.Close() // now Ping fails

	e := gin.New()
	httpserver.RegisterHealth(e, httpserver.HealthDeps{Pool: pool})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness with dead pool: want 503, got %d", w.Code)
	}
}

func TestServer_RunThenGracefulShutdown(t *testing.T) {
	e := gin.New()
	e.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	srv := httpserver.NewServer(e, "0") // :0 → OS-assigned free port

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()
	time.Sleep(75 * time.Millisecond) // let ListenAndServe bind

	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v after graceful shutdown, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after Shutdown")
	}
}
