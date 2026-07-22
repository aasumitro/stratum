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

func TestQueueEvent_RunsOnFlushNotBefore(t *testing.T) {
	ctx := db.WithPendingEvents(context.Background())

	var ran []string
	db.QueueEvent(ctx, func() { ran = append(ran, "first") })
	db.QueueEvent(ctx, func() { ran = append(ran, "second") })
	if len(ran) != 0 {
		t.Fatalf("queued events must not run before Flush, ran = %v", ran)
	}

	db.FlushPendingEvents(ctx)
	if want := []string{"first", "second"}; !equalStrings(ran, want) {
		t.Errorf("after flush: ran = %v, want %v (in order)", ran, want)
	}
}

func TestQueueEvent_FlushIsOneShot(t *testing.T) {
	ctx := db.WithPendingEvents(context.Background())

	n := 0
	db.QueueEvent(ctx, func() { n++ })
	db.FlushPendingEvents(ctx)
	db.FlushPendingEvents(ctx) // a second flush (e.g. rollback path never called) must not re-run anything
	if n != 1 {
		t.Errorf("event ran %d times, want exactly 1", n)
	}
}

func TestQueueEvent_NoQueueInContext_RunsImmediately(t *testing.T) {
	// No enclosing transaction set up a queue (db.WithPendingEvents never
	// called) — QueueEvent must run fn right away rather than silently
	// dropping it, since there's nothing that will ever flush it.
	ran := false
	db.QueueEvent(context.Background(), func() { ran = true })
	if !ran {
		t.Error("QueueEvent without an installed queue should run fn immediately")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
