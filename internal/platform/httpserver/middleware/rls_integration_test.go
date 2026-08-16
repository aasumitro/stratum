package middleware_test

import (
	"context"
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

// setupScratchTable creates (and schedules cleanup for) a throwaway table
// used to prove the middleware's commit/rollback boundary with a real DB
// write instead of an in-memory side effect — the same convention
// internal/platform/db/db_test.go uses for its own WithTx tests.
func setupScratchTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS public.rls_scratch (id text primary key)`); err != nil {
		t.Fatalf("create scratch table: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP TABLE IF EXISTS public.rls_scratch`) })
}

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

// TestIntegration_RLSTxMiddleware_CommitsOnlyOn2xx proves the middleware's
// own commit/rollback boundary with a real write made through the request's
// tx-scoped querier (db.QuerierFromContext) — the same querier
// events.Enqueue calls made from inside a handler under this middleware
// rely on to make an outbox row commit atomically with the rest of a
// request's writes. A write made on a request that ultimately fails (4xx)
// must never persist; one made on a successful (2xx) request must.
func TestIntegration_RLSTxMiddleware_CommitsOnlyOn2xx(t *testing.T) {
	pool := rlsTestPool(t)
	setupScratchTable(t, pool)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_commit_test"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))

	e.GET("/fail", func(c *gin.Context) {
		tx := db.QuerierFromContext(c.Request.Context(), nil)
		_, _ = tx.Exec(c.Request.Context(), `INSERT INTO public.rls_scratch (id) VALUES ('fail')`)
		c.Status(http.StatusBadRequest)
	})
	e.GET("/succeed", func(c *gin.Context) {
		tx := db.QuerierFromContext(c.Request.Context(), nil)
		_, _ = tx.Exec(c.Request.Context(), `INSERT INTO public.rls_scratch (id) VALUES ('succeed')`)
		c.Status(http.StatusOK)
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", nil))
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/succeed", nil))

	var ids []string
	rows, err := pool.Query(context.Background(), `SELECT id FROM public.rls_scratch`)
	if err != nil {
		t.Fatalf("query scratch rows: %v", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	if len(ids) != 1 || ids[0] != "succeed" {
		t.Errorf("surviving rows = %v, want exactly [\"succeed\"] (the /fail write must have rolled back)", ids)
	}
}

// TestIntegration_RLSTxMiddleware_ResponseExceedsCap_RollsBack covers
// bufferedWriter's cap: a handler writing more than responseBufferCap must
// fail the request (500) with the transaction rolled back — proven the same
// way TestIntegration_RLSTxMiddleware_CommitsOnlyOn2xx proves rollback
// above, with a real write through the request's tx-scoped querier.
func TestIntegration_RLSTxMiddleware_ResponseExceedsCap_RollsBack(t *testing.T) {
	pool := rlsTestPool(t)
	setupScratchTable(t, pool)
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(func(c *gin.Context) {
		c.Set("organization.organization", contracts.OrganizationInfo{ID: "ws_rls_cap_test"})
		c.Next()
	})
	e.Use(middleware.NewRLSTxMiddleware(pool))

	e.GET("/toolarge", func(c *gin.Context) {
		tx := db.QuerierFromContext(c.Request.Context(), nil)
		_, _ = tx.Exec(c.Request.Context(), `INSERT INTO public.rls_scratch (id) VALUES ('toolarge')`)
		c.Status(http.StatusOK)
		oversized := make([]byte, 6<<20) // 6MB > the 5MB cap
		_, _ = c.Writer.Write(oversized)
	})

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/toolarge", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("response exceeding the buffer cap: want 500, got %d", w.Code)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.rls_scratch WHERE id = 'toolarge'`).Scan(&count); err != nil {
		t.Fatalf("count scratch rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("write made during a cap-exceeded (rolled-back) request must not persist, count = %d", count)
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
