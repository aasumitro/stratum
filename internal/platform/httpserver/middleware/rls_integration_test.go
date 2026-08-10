package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/db"
	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// rlsTestPool opens a pool capped at one connection, which makes a
// connection leaked by a panic path (the bug this test guards against)
// observable: any request after the leak blocks forever waiting for a
// connection instead of getting one back from the pool.
func rlsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func rlsTestEngine(pool *pgxpool.Pool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	// Mirrors production topology: a global Recovery middleware registered
	// outermost, before the route-group-specific RLS middleware.
	e.Use(func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	})
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_test"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))
	e.GET("/panic", func(c *gin.Context) { panic("boom") })
	e.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}

func TestIntegration_RLSTxMiddleware_PanicDoesNotLeakConnection(t *testing.T) {
	pool := rlsTestPool(t)
	e := rlsTestEngine(pool)

	w1 := httptest.NewRecorder()
	e.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if w1.Code != http.StatusInternalServerError {
		t.Fatalf("first request status = %d, want 500", w1.Code)
	}

	done := make(chan int, 1)
	go func() {
		w2 := httptest.NewRecorder()
		e.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/ok", nil))
		done <- w2.Code
	}()

	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("second request status = %d, want 200", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second request blocked — the panicking request's connection was never released back to the pool")
	}
}

// TestIntegration_RLSTxMiddleware_QueuedEventFlushesOnlyOnCommit regression-
// tests the db.QueueEvent/FlushPendingEvents plumbing this middleware
// drives: an event queued during a request that ultimately fails (rolled
// back) must never fire, and one queued during a successful request
// (committed) must fire exactly once, after the commit.
func TestIntegration_RLSTxMiddleware_QueuedEventFlushesOnlyOnCommit(t *testing.T) {
	pool := rlsTestPool(t)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_event_test"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))

	var fired []string
	e.GET("/fail", func(c *gin.Context) {
		db.QueueEvent(c.Request.Context(), func() { fired = append(fired, "fail") })
		c.Status(http.StatusBadRequest)
	})
	e.GET("/succeed", func(c *gin.Context) {
		db.QueueEvent(c.Request.Context(), func() { fired = append(fired, "succeed") })
		c.Status(http.StatusOK)
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", nil))
	if len(fired) != 0 {
		t.Fatalf("event queued on a rolled-back request must not fire, fired = %v", fired)
	}

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/succeed", nil))
	if len(fired) != 1 || fired[0] != "succeed" {
		t.Errorf("fired = %v, want exactly [\"succeed\"]", fired)
	}
}

// TestIntegration_RLSTxMiddleware_ResponseExceedsCap_RollsBack covers
// bufferedWriter's cap: a handler writing more than responseBufferCap must
// fail the request (500) with the transaction rolled back — proven the
// same way TestIntegration_RLSTxMiddleware_QueuedEventFlushesOnlyOnCommit
// proves rollback above: a db.QueueEvent callback queued during the
// request must never fire.
func TestIntegration_RLSTxMiddleware_ResponseExceedsCap_RollsBack(t *testing.T) {
	pool := rlsTestPool(t)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_cap_test"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))

	var fired []string
	e.GET("/toolarge", func(c *gin.Context) {
		db.QueueEvent(c.Request.Context(), func() { fired = append(fired, "toolarge") })
		c.Status(http.StatusOK)
		oversized := make([]byte, 6<<20) // 6MB > the 5MB cap
		_, _ = c.Writer.Write(oversized)
	})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/toolarge", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("response exceeding the buffer cap: want 500, got %d", w.Code)
	}
	if len(fired) != 0 {
		t.Fatalf("event queued on a cap-exceeded (rolled-back) request must not fire, fired = %v", fired)
	}
}

func TestIntegration_RLSTxMiddleware_CommitFailure(t *testing.T) {
	pool := rlsTestPool(t)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_test_commit_fail"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))
	e.GET("/commit-fail", func(c *gin.Context) {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte(`{"success":true}`))

		// Force the transaction to an aborted state so Commit() fails.
		tx := db.QuerierFromContext(c.Request.Context(), nil)
		_, _ = tx.Exec(c.Request.Context(), "SELECT 1/0")
	})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/commit-fail", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when commit fails, got %d", w.Code)
	}
	expectedBody := `{"error":"database error"}`
	if w.Body.String() != expectedBody {
		t.Fatalf("expected %q, got %q", expectedBody, w.Body.String())
	}
}
