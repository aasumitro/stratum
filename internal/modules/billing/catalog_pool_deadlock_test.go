package billing

// Regression test for the catalog-helper connection-pool self-deadlock this
// test guards against: findPlanByID (and its four siblings) queried via
// s.pool directly instead of s.querier(ctx), so a caller already holding
// one pool connection (an RLS transaction) blocked trying to Acquire() a
// second one from the same pool — fatal once concurrent requests reach
// Infra.Pool.MaxConns, since every held connection's release then depends
// on that same blocked second acquire succeeding first. A pool capped at
// MaxConns=1 with its one connection already checked out reproduces the
// exact deadlock deterministically, without needing real concurrency.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func TestFindPlanByID_NoPoolSelfDeadlock(t *testing.T) {
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

	// Check out the pool's only connection via an open transaction, the
	// same way the RLS middleware holds one for the lifetime of a request.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	ctx := db.WithQuerier(t.Context(), tx)
	mod := NewModuleForTest(pool, nil)

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	plan, err := mod.svc.findPlanByID(ctx, "solo")
	if err != nil {
		t.Fatalf("findPlanByID: %v", err)
	}
	if plan.ID != "solo" {
		t.Fatalf("plan.ID = %q, want %q", plan.ID, "solo")
	}
}
