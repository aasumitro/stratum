package notification

// Integration tests for the notification schema's row-level security
// (migration 000010): a caller's own messages and preferences are visible
// only inside a transaction scoped to that caller (app.auth_sub), the
// worker-only idempotency table is closed to the runtime role, and the
// service's API and port paths keep working as the RLS-constrained role
// (POSTGRES_APP_URL). Fixtures go through the BYPASSRLS test role.

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/db"
)

const (
	rlsNotifAlice = "rls_n_alice"
	rlsNotifBob   = "rls_n_bob"
	rlsNotifOrg   = "0e1e0000-0000-0000-0000-0000000000c9"
	rlsNotifEvent = "0e1e0000-0000-0000-0000-0000000000e9"
)

func openNotifRLSPool(t *testing.T, env string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skip(env + " not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open %s pool: %v", env, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanupNotifRLSFixtures(admin *pgxpool.Pool) {
	ctx := context.Background()
	admin.Exec(ctx, `DELETE FROM notification.messages WHERE auth_sub IN ($1, $2)`, rlsNotifAlice, rlsNotifBob)
	admin.Exec(ctx, `DELETE FROM notification.preferences WHERE auth_sub IN ($1, $2)`, rlsNotifAlice, rlsNotifBob)
	admin.Exec(ctx, `DELETE FROM notification.processed_events WHERE event_id = $1`, rlsNotifEvent)
}

func seedNotifRLSFixtures(t *testing.T) (admin, app *pgxpool.Pool) {
	t.Helper()
	admin = openNotifRLSPool(t, "TEST_DATABASE_URL")
	app = openNotifRLSPool(t, "POSTGRES_APP_URL")
	cleanupNotifRLSFixtures(admin)
	t.Cleanup(func() { cleanupNotifRLSFixtures(admin) })

	ctx := t.Context()
	if _, err := admin.Exec(ctx, `INSERT INTO notification.messages (organization_id, auth_sub, kind, channel, body) VALUES
		($1, $2, 'in_app', 'invoice_paid', 'a1'), ($1, $2, 'in_app', 'invoice_paid', 'a2'),
		($1, $3, 'in_app', 'invoice_paid', 'b1')`, rlsNotifOrg, rlsNotifAlice, rlsNotifBob); err != nil {
		t.Fatalf("seed messages: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled)
		VALUES ($1, 'email', 'invoice_paid', true)`, rlsNotifBob); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO notification.processed_events (event_id) VALUES ($1)`, rlsNotifEvent); err != nil {
		t.Fatalf("seed processed event: %v", err)
	}
	return admin, app
}

func countRows(ctx context.Context, t *testing.T, q db.Querier, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := q.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRLS_NotificationTables_IsolateUsers(t *testing.T) {
	_, app := seedNotifRLSFixtures(t)
	ctx := t.Context()

	if n := countRows(ctx, t, app, `SELECT count(*) FROM notification.messages WHERE auth_sub IN ($1, $2)`, rlsNotifAlice, rlsNotifBob); n != 0 {
		t.Errorf("no scope: want 0 messages, got %d", n)
	}

	err := db.WithAuthTx(ctx, app, rlsNotifAlice, func(ctx context.Context, q db.Querier) error {
		if n := countRows(ctx, t, q, `SELECT count(*) FROM notification.messages WHERE auth_sub = $1`, rlsNotifAlice); n != 2 {
			t.Errorf("scope alice, own messages: want 2, got %d", n)
		}
		if n := countRows(ctx, t, q, `SELECT count(*) FROM notification.messages WHERE auth_sub = $1`, rlsNotifBob); n != 0 {
			t.Errorf("scope alice, bob's messages: want 0, got %d", n)
		}
		if n := countRows(ctx, t, q, `SELECT count(*) FROM notification.preferences WHERE auth_sub = $1`, rlsNotifBob); n != 0 {
			t.Errorf("scope alice, bob's preferences: want 0, got %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scope alice: %v", err)
	}

	err = db.WithAuthTx(ctx, app, rlsNotifAlice, func(ctx context.Context, q db.Querier) error {
		_, err := q.Exec(ctx, `INSERT INTO notification.preferences (auth_sub, channel, event_type, enabled) VALUES ($1, 'email', 'invoice_paid', false)`, rlsNotifBob)
		return err
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
		t.Errorf("scope alice writing bob's preference: want RLS violation 42501, got %v", err)
	}

	if n := countRows(ctx, t, app, `SELECT count(*) FROM notification.processed_events WHERE event_id = $1`, rlsNotifEvent); n != 0 {
		t.Errorf("processed_events as the runtime role: want 0 rows (no policy = deny-all), got %d", n)
	}
	if _, err := app.Exec(ctx, `INSERT INTO notification.processed_events (event_id) VALUES (gen_random_uuid())`); err == nil {
		t.Error("processed_events insert as the runtime role: want RLS violation, got nil")
	}
}

func TestRLS_NotificationService_WorksUnderStratumApp(t *testing.T) {
	admin, app := seedNotifRLSFixtures(t)
	ctx := t.Context()
	svc := &service{repo: &repository{}, pool: app}

	res, err := svc.listForUser(ctx, rlsNotifAlice, "", "", "", 50, 0, true)
	if err != nil || len(res.Messages) != 2 || res.Total != 2 {
		t.Fatalf("listForUser(alice): want 2 messages, total 2; got %+v, %v", res, err)
	}
	if n, err := svc.unreadCount(ctx, rlsNotifAlice, ""); err != nil || n != 2 {
		t.Fatalf("unreadCount(alice): want 2, got %d, %v", n, err)
	}
	if err := svc.markAllRead(ctx, rlsNotifAlice, ""); err != nil {
		t.Fatalf("markAllRead(alice): %v", err)
	}
	if n := countRows(ctx, t, admin, `SELECT count(*) FROM notification.messages WHERE auth_sub = $1 AND read_at IS NULL`, rlsNotifBob); n != 1 {
		t.Errorf("markAllRead(alice) must not touch bob's: want 1 unread, got %d", n)
	}
	if _, err := svc.upsertPreference(ctx, rlsNotifAlice, "email", "invoice_paid", false); err != nil {
		t.Fatalf("upsertPreference(alice): %v", err)
	}
	if err := svc.deleteAllForUser(ctx, rlsNotifAlice); err != nil {
		t.Fatalf("deleteAllForUser(alice): %v", err)
	}
	if n := countRows(ctx, t, admin, `SELECT count(*) FROM notification.messages WHERE auth_sub = $1`, rlsNotifAlice); n != 0 {
		t.Errorf("deleteAllForUser(alice): want 0 left, got %d", n)
	}
	if n := countRows(ctx, t, admin, `SELECT count(*) FROM notification.messages WHERE auth_sub = $1`, rlsNotifBob); n != 1 {
		t.Errorf("deleteAllForUser(alice) must not touch bob's: want 1, got %d", n)
	}
}
