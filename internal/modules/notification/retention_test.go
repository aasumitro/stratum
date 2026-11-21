package notification_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/notification"
)

// These tests share a database with local development, and a sweep touches every
// row past its cutoff. A cutoff a century back keeps a run from removing anything
// but the rows it seeded.
const (
	sweepDays = 36500
	expired   = sweepDays + 1
	current   = sweepDays - 1
)

const retentionOrg = "0e1e0000-0000-0000-0000-0000000000d1"

func notificationWorkerModule(t *testing.T) (admin *pgxpool.Pool, mod *notification.Module) {
	t.Helper()
	admin = testPoolNotif(t)
	dsn := os.Getenv("POSTGRES_WORKER_URL")
	if dsn == "" {
		t.Skip("POSTGRES_WORKER_URL not set")
	}
	worker, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open worker pool: %v", err)
	}
	t.Cleanup(worker.Close)

	cleanup := func() {
		admin.Exec(context.Background(), `DELETE FROM notification.messages WHERE organization_id = $1`, retentionOrg)
	}
	cleanup()
	t.Cleanup(cleanup)
	return admin, notification.NewModuleForTest(worker)
}

func TestSweepRetention_Messages(t *testing.T) {
	admin, mod := notificationWorkerModule(t)

	for _, age := range []int{expired, current} {
		if _, err := admin.Exec(t.Context(), `
			INSERT INTO notification.messages (organization_id, kind, channel, body, created_at)
			VALUES ($1, 'in_app', 'retention', $2, now() - make_interval(days => $3))`,
			retentionOrg, strconv.Itoa(age), age); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, err := mod.SweepRetention(t.Context(), sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	rows, err := admin.Query(t.Context(), `SELECT body FROM notification.messages WHERE organization_id = $1`, retentionOrg)
	if err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(bodies) != 1 || bodies[0] != "36499" {
		t.Errorf("want only the current row left, got %v", bodies)
	}
}

func TestSweepRetention_MessagesDrainPastOneBatch(t *testing.T) {
	admin, mod := notificationWorkerModule(t)

	if _, err := admin.Exec(t.Context(), `
		INSERT INTO notification.messages (organization_id, kind, channel, body, created_at)
		SELECT $1, 'in_app', 'retention', 'x', now() - make_interval(days => $2) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, retentionOrg, expired); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := mod.SweepRetention(t.Context(), sweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var left int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM notification.messages WHERE organization_id = $1`, retentionOrg).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d left, want one sweep to drain more than one batch", left)
	}
}
