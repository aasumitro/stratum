package billing_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func TestRLSIsolation(t *testing.T) {
	testDBURL := os.Getenv("TEST_DATABASE_URL")
	appURL := os.Getenv("POSTGRES_APP_URL")
	workerURL := os.Getenv("POSTGRES_WORKER_URL")

	if testDBURL == "" || appURL == "" || workerURL == "" {
		t.Skip("TEST_DATABASE_URL, POSTGRES_APP_URL, and POSTGRES_WORKER_URL required for RLS isolation tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Seed two organizations using the owner (test pool)
	testPool, err := pgxpool.New(ctx, testDBURL)
	if err != nil {
		t.Fatalf("failed to connect to test db: %v", err)
	}

	orgA := "00000000-0000-0000-0000-00000000000c"
	orgB := "00000000-0000-0000-0000-00000000000d"
	subA := "11111111-1111-1111-1111-11111111111c"
	subB := "11111111-1111-1111-1111-11111111111d"
	invA := "22222222-2222-2222-2222-22222222222c"
	invB := "22222222-2222-2222-2222-22222222222d"

	t.Cleanup(func() {
		testPool.Close()
	})
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, "DELETE FROM billing.invoices WHERE id IN ($1, $2)", invA, invB)
		_, _ = testPool.Exec(cleanupCtx, "DELETE FROM billing.subscriptions WHERE id IN ($1, $2)", subA, subB)
		_, _ = testPool.Exec(cleanupCtx, "DELETE FROM organization.organizations WHERE id IN ($1, $2)", orgA, orgB)
	})

	// Pre-cleanup in case a previous run panicked before t.Cleanup
	_, _ = testPool.Exec(ctx, "DELETE FROM billing.invoices WHERE id IN ($1, $2)", invA, invB)
	_, _ = testPool.Exec(ctx, "DELETE FROM billing.subscriptions WHERE id IN ($1, $2)", subA, subB)
	_, _ = testPool.Exec(ctx, "DELETE FROM organization.organizations WHERE id IN ($1, $2)", orgA, orgB)

	_, err = testPool.Exec(ctx, `
		INSERT INTO organization.organizations (id, name, slug, owner_id) VALUES 
		($1, 'Org A', 'org-a', '33333333-3333-3333-3333-333333333333'),
		($2, 'Org B', 'org-b', '44444444-4444-4444-4444-444444444444')
	`, orgA, orgB)
	if err != nil {
		t.Fatalf("failed to insert orgs: %v", err)
	}

	_, err = testPool.Exec(ctx, `
		INSERT INTO billing.subscriptions (id, subject_type, subject_id, plan, status, currency) VALUES 
		($1, 'organization', $2, 'solo', 'active', 'USD'),
		($3, 'organization', $4, 'solo', 'active', 'USD')
	`, subA, orgA, subB, orgB)
	if err != nil {
		t.Fatalf("failed to insert subscriptions: %v", err)
	}

	_, err = testPool.Exec(ctx, `
		INSERT INTO billing.invoices (id, subscription_id, amount_cents, currency, status) VALUES 
		($1, $2, 1000, 'USD', 'pending'),
		($3, $4, 1000, 'USD', 'pending')
	`, invA, subA, invB, subB)
	if err != nil {
		t.Fatalf("failed to insert invoices: %v", err)
	}

	// 2. Open app pool (stratum_app role)
	appPool, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("failed to connect app pool: %v", err)
	}
	defer appPool.Close()

	t.Run("app role enforces RLS", func(t *testing.T) {
		appTx, err := appPool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}
		defer appTx.Rollback(ctx)

		// Scoped to orgA
		err = db.SetOrgContext(ctx, appTx, orgA)
		if err != nil {
			t.Fatalf("failed to set context: %v", err)
		}

		// SELECT returns only orgA's rows
		var count int
		err = appTx.QueryRow(ctx, "SELECT count(*) FROM billing.invoices WHERE id IN ($1, $2)", invA, invB).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 invoice, got %d", count)
		}

		var seenSub string
		err = appTx.QueryRow(ctx, "SELECT subscription_id FROM billing.invoices WHERE id IN ($1, $2)", invA, invB).Scan(&seenSub)
		if err != nil {
			t.Fatalf("failed to query subscription_id: %v", err)
		}
		if seenSub != subA {
			t.Errorf("expected subscription_id %s, got %s", subA, seenSub)
		}

		// UPDATE orgB's invoice affects 0 rows
		cmdTag, err := appTx.Exec(ctx, "UPDATE billing.invoices SET status = 'paid' WHERE id = $1", invB)
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if cmdTag.RowsAffected() != 0 {
			t.Errorf("expected 0 rows affected, got %d", cmdTag.RowsAffected())
		}

		// INSERT for orgB is rejected
		_, err = appTx.Exec(ctx, `
			INSERT INTO billing.invoices (id, subscription_id, amount_cents, currency, status) 
			VALUES (gen_random_uuid(), $1, 1000, 'USD', 'pending')
		`, subB)
		if err == nil {
			t.Fatalf("inserting for orgB while scoped to orgA should fail")
		}
		// The error should be a RLS policy violation (new row violates row-level security policy)
		if !strings.Contains(err.Error(), "new row violates row-level security policy") {
			t.Errorf("expected RLS violation error, got: %v", err)
		}
	})

	// 3. Open worker pool (stratum_worker role)
	workerPool, err := pgxpool.New(ctx, workerURL)
	if err != nil {
		t.Fatalf("failed to connect worker pool: %v", err)
	}
	defer workerPool.Close()

	t.Run("worker role bypasses RLS", func(t *testing.T) {
		workerTx, err := workerPool.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin tx: %v", err)
		}
		defer workerTx.Rollback(ctx)

		// SELECT returns both orgs' rows (2 rows total)
		var count int
		err = workerTx.QueryRow(ctx, "SELECT count(*) FROM billing.invoices WHERE id IN ($1, $2)", invA, invB).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query count: %v", err)
		}
		if count != 2 {
			t.Errorf("expected 2 invoices, got %d", count)
		}

		// UPDATE orgA's invoice affects 1 row
		cmdTag, err := workerTx.Exec(ctx, "UPDATE billing.invoices SET status = 'paid' WHERE id = $1", invA)
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if cmdTag.RowsAffected() != 1 {
			t.Errorf("expected 1 row affected, got %d", cmdTag.RowsAffected())
		}

		// INSERT for orgB succeeds
		_, err = workerTx.Exec(ctx, `
			INSERT INTO billing.invoices (id, subscription_id, amount_cents, currency, status) 
			VALUES (gen_random_uuid(), $1, 1000, 'USD', 'pending')
		`, subB)
		if err != nil {
			t.Fatalf("worker should be able to insert for orgB: %v", err)
		}
	})
}
