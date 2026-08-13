package billing

// Regression test for recordUsage called with no ambient transaction, the
// exact shape organization.syncMemberUsage's background goroutine calls it
// with (db.WithoutQuerier(context.WithoutCancel(ctx))). Connects as the
// RLS-constrained app role (POSTGRES_APP_URL), not the superuser test pool
// the rest of this package's integration tests use — findSubscriptionBySubject
// reads billing.subscriptions, which is FORCE ROW LEVEL SECURITY, so only a
// real RLS-constrained connection can prove a background caller with no org
// context set actually gets one before that query runs.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func testAppPoolUsage(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_APP_URL")
	if dsn == "" {
		t.Skip("POSTGRES_APP_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupUsageRLSFixtures deletes this test's rows. billing.subscriptions is
// FORCE RLS, so the delete must run with org context set in the same
// transaction — a plain pool.Exec as the app role would silently affect 0
// rows and leave the fixture behind for the next run.
func cleanupUsageRLSFixtures(pool *pgxpool.Pool, orgID string) {
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM billing.usage WHERE organization_id = $1`, orgID)
	_ = db.WithTx(ctx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(ctx, tx, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM billing.subscriptions WHERE subject_type = 'organization' AND subject_id = $1`, orgID)
		return err
	})
}

// TestRecordUsage_BackgroundCaller_NoAmbientTx reproduces the exact failure
// a background caller with no org context hits: findSubscriptionBySubject
// (recordUsage's first query) returns pgx.ErrNoRows under FORCE RLS when no
// org context is set, the exact failure syncMemberUsage's background
// goroutine hits on every call before this fix. It must fail this way
// against pre-split code and pass once recordUsage routes a bare context
// through withOrgTx.
func TestRecordUsage_BackgroundCaller_NoAmbientTx(t *testing.T) {
	pool := testAppPoolUsage(t)
	const orgID = "00000000-0000-0000-0000-0000000c0601"
	cleanupUsageRLSFixtures(pool, orgID)
	t.Cleanup(func() { cleanupUsageRLSFixtures(pool, orgID) })

	// Seed the subscription the way the interactive flow does: inside a
	// transaction with org context set, via the same RLS-constrained role
	// recordUsage itself will run as.
	seedCtx := context.Background()
	r := &repository{}
	if err := db.WithTx(seedCtx, pool, func(tx db.Querier) error {
		if err := db.SetOrgContext(seedCtx, tx, orgID); err != nil {
			return err
		}
		_, err := r.insertSubscription(seedCtx, tx, subjectTypeOrganization, orgID, "solo", cycleMonthly, "USD")
		return err
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	mod := NewModuleForTest(pool, nil)

	// Matches syncMemberUsage's exact stripped-context shape (service_member.go):
	// no querier, no cancellation tied to the caller's own request.
	ctx := db.WithoutQuerier(context.WithoutCancel(context.Background()))
	if err := mod.svc.recordUsage(ctx, orgID, "members", 3); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("recordUsage failed with ErrNoRows — findSubscriptionBySubject was blocked by FORCE RLS with no org context set: %v", err)
		}
		t.Fatalf("recordUsage: %v", err)
	}

	var value int64
	err := pool.QueryRow(ctx, `SELECT value FROM billing.usage WHERE organization_id = $1 AND metric = 'members'`, orgID).Scan(&value)
	if err != nil {
		t.Fatalf("usage row not found after recordUsage: %v", err)
	}
	if value != 3 {
		t.Errorf("usage value = %d, want 3", value)
	}
}
