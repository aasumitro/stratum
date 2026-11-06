package account_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/account"
)

// These tests share a database with local development, and a sweep touches every
// row past its cutoff. A cutoff a century back keeps a run from removing anything
// but the rows it seeded.
const (
	sweepDays = 36500
	expired   = sweepDays + 1
	current   = sweepDays - 1
	// Retention above sweepDays, for the table a test is not looking at.
	keepAll = 73000
)

// workerModule builds the module on the role production sweeps with, so a missing
// grant fails here. Fixtures go through the returned admin pool.
func workerModule(t *testing.T) (admin *pgxpool.Pool, mod *account.Module) {
	t.Helper()
	admin = testPool(t)
	dsn := os.Getenv("POSTGRES_WORKER_URL")
	if dsn == "" {
		t.Skip("POSTGRES_WORKER_URL not set")
	}
	worker, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open worker pool: %v", err)
	}
	t.Cleanup(worker.Close)
	return admin, account.NewModuleForTest(worker)
}

func seedLoginEvent(t *testing.T, admin *pgxpool.Pool, sub string, ageDays int) {
	t.Helper()
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO account.login_events (auth_sub, created_at)
		VALUES ($1, now() - make_interval(days => $2))`, sub, ageDays); err != nil {
		t.Fatalf("seed login event: %v", err)
	}
}

func cleanupAccountRetention(t *testing.T, admin *pgxpool.Pool, sub string) {
	t.Helper()
	cleanup := func() {
		ctx := context.Background()
		admin.Exec(ctx, `DELETE FROM account.login_events WHERE auth_sub = $1`, sub)
		admin.Exec(ctx, `DELETE FROM account.tasks WHERE auth_sub = $1`, sub)
	}
	cleanup()
	t.Cleanup(cleanup)
}

func TestSweepRetention_LoginEvents(t *testing.T) {
	admin, mod := workerModule(t)
	const sub = "retention_login_events"
	cleanupAccountRetention(t, admin, sub)

	seedLoginEvent(t, admin, sub, expired)
	seedLoginEvent(t, admin, sub, current)

	if _, err := mod.SweepRetention(t.Context(), sweepDays, keepAll); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var remaining, oldest int
	if err := admin.QueryRow(t.Context(), `
		SELECT count(*), COALESCE(max(extract(day FROM now() - created_at))::int, 0)
		FROM account.login_events WHERE auth_sub = $1`, sub).Scan(&remaining, &oldest); err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	if remaining != 1 || oldest >= sweepDays {
		t.Errorf("want only the current row left, got %d rows, oldest %d days", remaining, oldest)
	}
}

func TestSweepRetention_ExportPayloads(t *testing.T) {
	admin, mod := workerModule(t)
	const sub = "retention_exports"
	cleanupAccountRetention(t, admin, sub)

	seed := func(kind, status string, ageDays int) string {
		t.Helper()
		var id string
		if err := admin.QueryRow(t.Context(), `
			INSERT INTO account.tasks (auth_sub, kind, status, result, completed_at)
			VALUES ($1, $2, $3, '{"export":"payload"}', now() - make_interval(days => $4))
			RETURNING id`, sub, kind, status, ageDays).Scan(&id); err != nil {
			t.Fatalf("seed task: %v", err)
		}
		return id
	}
	expiredExport := seed("export_data", "completed", expired)
	currentExport := seed("export_data", "completed", current)
	failedExport := seed("export_data", "failed", expired)
	deletion := seed("delete_account", "completed", expired)

	if _, err := mod.SweepRetention(t.Context(), keepAll, sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	for id, wantCleared := range map[string]bool{
		expiredExport: true,
		currentExport: false,
		failedExport:  false,
		deletion:      false,
	} {
		var cleared bool
		if err := admin.QueryRow(t.Context(),
			`SELECT result IS NULL FROM account.tasks WHERE id = $1`, id).Scan(&cleared); err != nil {
			t.Fatalf("read task %s (its row must stay): %v", id, err)
		}
		if cleared != wantCleared {
			t.Errorf("task %s: cleared = %v, want %v", id, cleared, wantCleared)
		}
	}
}

// Reselecting rows already cleared would pin every batch on them, and this sweep
// would never finish, so it runs under a deadline.
func TestSweepRetention_ExportBatchesMoveForward(t *testing.T) {
	admin, mod := workerModule(t)
	const sub = "retention_export_batches"
	cleanupAccountRetention(t, admin, sub)

	if _, err := admin.Exec(t.Context(), `
		INSERT INTO account.tasks (auth_sub, kind, status, result, completed_at)
		SELECT $1, 'export_data', 'completed', '{}', now() - make_interval(days => $2) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, sub, expired); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cleared, err := mod.SweepRetention(ctx, keepAll, sweepDays)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if cleared < 10001 {
		t.Errorf("cleared %d rows, want at least the 10,001 seeded", cleared)
	}

	var left int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM account.tasks WHERE auth_sub = $1 AND result IS NOT NULL`, sub).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d payloads left, want one sweep to drain more than one batch", left)
	}
}

func TestSweepRetention_LoginEventsDrainPastOneBatch(t *testing.T) {
	admin, mod := workerModule(t)
	const sub = "retention_login_batches"
	cleanupAccountRetention(t, admin, sub)

	if _, err := admin.Exec(t.Context(), `
		INSERT INTO account.login_events (auth_sub, created_at)
		SELECT $1, now() - make_interval(days => $2) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, sub, expired); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := mod.SweepRetention(t.Context(), sweepDays, keepAll); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var left int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM account.login_events WHERE auth_sub = $1`, sub).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d left, want one sweep to drain more than one batch", left)
	}
}

// One table failing must not hold the other table back, and the error must still
// reach the caller so it can log and retry on the next tick.
func TestSweepRetention_OneTableFailingDoesNotStopTheOther(t *testing.T) {
	admin, mod := workerModule(t)
	const sub = "retention_partial_failure"
	cleanupAccountRetention(t, admin, sub)

	suffix := fmt.Sprintf("retention_fail_%d", os.Getpid())
	ctx := t.Context()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION account.%[1]s() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN
			IF OLD.auth_sub = '%[2]s' THEN
				RAISE EXCEPTION 'injected sweep failure';
			END IF;
			RETURN OLD;
		END;
		$f$`, suffix, sub)); err != nil {
		t.Fatalf("create failing function: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %[1]s BEFORE DELETE ON account.login_events
		FOR EACH ROW EXECUTE FUNCTION account.%[1]s()`, suffix)); err != nil {
		t.Fatalf("create failing trigger: %v", err)
	}
	t.Cleanup(func() {
		background := context.Background()
		admin.Exec(background, fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON account.login_events`, suffix))
		admin.Exec(background, fmt.Sprintf(`DROP FUNCTION IF EXISTS account.%[1]s()`, suffix))
	})

	seedLoginEvent(t, admin, sub, expired)
	var exportID string
	if err := admin.QueryRow(ctx, `
		INSERT INTO account.tasks (auth_sub, kind, status, result, completed_at)
		VALUES ($1, 'export_data', 'completed', '{}', now() - make_interval(days => $2))
		RETURNING id`, sub, expired).Scan(&exportID); err != nil {
		t.Fatalf("seed export: %v", err)
	}

	if _, err := mod.SweepRetention(ctx, sweepDays, sweepDays); err == nil {
		t.Error("want the login-event failure returned")
	}

	var cleared bool
	if err := admin.QueryRow(ctx, `SELECT result IS NULL FROM account.tasks WHERE id = $1`, exportID).Scan(&cleared); err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !cleared {
		t.Error("the export sweep must still run when the login-event sweep fails")
	}
}
