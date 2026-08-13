package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/db"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return dsn
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := db.NewPostgresPool(t.Context(), config.PostgresConfig{
		URL: testDSN(t), MaxOpenConns: 5, MaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewPostgresPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNewPostgresPool_PingsAndQueries(t *testing.T) {
	pool := testPool(t)
	// a real query exercises the attached query tracer (TraceQueryStart/End)
	var one int
	if err := pool.QueryRow(t.Context(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query: %v", err)
	}
	if one != 1 {
		t.Errorf("SELECT 1 = %d", one)
	}
}

func TestNewPostgresPool_InvalidURL(t *testing.T) {
	_, err := db.NewPostgresPool(t.Context(), config.PostgresConfig{URL: "://not a url", MaxOpenConns: 1})
	if err == nil {
		t.Error("expected an error parsing an invalid postgres URL")
	}
}

func TestNewPostgresPool_Unreachable(t *testing.T) {
	testDSN(t) // skip when no DB env configured
	// valid DSN shape, but nothing is listening → ping fails fast
	_, err := db.NewPostgresPool(t.Context(), config.PostgresConfig{
		URL:          "postgres://stratum:stratum@127.0.0.1:1/stratum?sslmode=disable&connect_timeout=2",
		MaxOpenConns: 1, MaxIdleTime: time.Minute,
	})
	if err == nil {
		t.Error("expected a ping error for an unreachable database")
	}
}

func TestWithTx_CommitsOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.db_tx_test (id text primary key)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP TABLE IF EXISTS public.db_tx_test`) })

	err := db.WithTx(ctx, pool, func(tx db.Querier) error {
		_, e := tx.Exec(ctx, `INSERT INTO public.db_tx_test (id) VALUES ('commit-1')`)
		return e
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM public.db_tx_test WHERE id = 'commit-1'`).Scan(&n)
	if n != 1 {
		t.Errorf("committed row not found, count = %d", n)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.db_tx_test (id text primary key)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP TABLE IF EXISTS public.db_tx_test`) })

	boom := errors.New("boom")
	err := db.WithTx(ctx, pool, func(tx db.Querier) error {
		if _, e := tx.Exec(ctx, `INSERT INTO public.db_tx_test (id) VALUES ('rollback-1')`); e != nil {
			return e
		}
		return boom // triggers rollback
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTx should return fn's error, got %v", err)
	}

	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM public.db_tx_test WHERE id = 'rollback-1'`).Scan(&n)
	if n != 0 {
		t.Errorf("row must be rolled back, count = %d", n)
	}
}

// TestWithTx_RollsBackOnPanic guards against a regression where fn
// panicking unwound past the error-handling rollback entirely — neither
// Commit nor Rollback ran, leaking the leased pool connection even though
// an outer recovery middleware (in the real server) catches the panic and
// the request survives.
func TestWithTx_RollsBackOnPanic(t *testing.T) {
	pool := testPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.db_tx_test (id text primary key)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP TABLE IF EXISTS public.db_tx_test`) })

	func() {
		defer func() {
			if p := recover(); p == nil {
				t.Error("WithTx should re-panic after rolling back, recovered nothing")
			}
		}()
		_ = db.WithTx(ctx, pool, func(tx db.Querier) error {
			if _, e := tx.Exec(ctx, `INSERT INTO public.db_tx_test (id) VALUES ('panic-1')`); e != nil {
				return e
			}
			panic("boom")
		})
	}()

	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM public.db_tx_test WHERE id = 'panic-1'`).Scan(&n)
	if n != 0 {
		t.Errorf("row must be rolled back after a panic, count = %d", n)
	}

	// The connection WithTx leased for the panicking transaction must have
	// been returned to the pool (via Rollback), not leaked — a fresh query
	// on the same pool proves it isn't stuck mid-transaction/exhausted.
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Errorf("pool unusable after a panicking WithTx: %v", err)
	}
}

func TestHasQuerier(t *testing.T) {
	if db.HasQuerier(context.Background()) {
		t.Error("bare context should report no querier")
	}

	ctx := db.WithQuerier(context.Background(), (*pgxpool.Pool)(nil))
	if !db.HasQuerier(ctx) {
		t.Error("context after WithQuerier should report a querier present")
	}

	// WithoutQuerier stores a literal nil, which must still read as absent —
	// otherwise a background goroutine derived via context.WithoutCancel
	// could pick up a stale/cleared querier instead of falling back to the pool.
	if db.HasQuerier(db.WithoutQuerier(ctx)) {
		t.Error("WithoutQuerier should clear the querier, not leave one present")
	}
}

func TestQuerierFromContext(t *testing.T) {
	pool := testPool(t)

	// no querier stored → returns the fallback
	if got := db.QuerierFromContext(t.Context(), pool); got != db.Querier(pool) {
		t.Error("empty context should return the fallback querier")
	}

	// stored querier is returned in preference to the fallback
	ctx := db.WithQuerier(t.Context(), pool)
	if got := db.QuerierFromContext(ctx, nil); got != db.Querier(pool) {
		t.Error("stored querier should be returned")
	}
}
