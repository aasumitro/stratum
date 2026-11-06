package billing_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/modules/billing"
)

// These tests share a database with local development, and a sweep touches every
// row past its cutoff. A cutoff a century back keeps a run from removing anything
// but the rows it seeded.
const (
	retentionSweepDays = 36500
	retentionProvider  = "retention-test"
)

func billingWorkerModule(t *testing.T) (admin *pgxpool.Pool, mod *billing.Module) {
	t.Helper()
	admin = testPoolBilling(t)
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
		admin.Exec(context.Background(), `DELETE FROM billing.webhook_events WHERE provider = $1`, retentionProvider)
	}
	cleanup()
	t.Cleanup(cleanup)
	return admin, billing.New(worker, billing.ProviderConfig{}, nil, nil, nil)
}

func TestSweepRetention_WebhookEvents(t *testing.T) {
	admin, mod := billingWorkerModule(t)

	for event, age := range map[string]int{"expired": retentionSweepDays + 1, "current": retentionSweepDays - 1} {
		if _, err := admin.Exec(t.Context(), `
			INSERT INTO billing.webhook_events (provider, event_id, received_at)
			VALUES ($1, $2, now() - make_interval(days => $3))`, retentionProvider, event, age); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, err := mod.SweepRetention(t.Context(), retentionSweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var n int
	var survivor string
	if err := admin.QueryRow(t.Context(), `
		SELECT count(*), COALESCE(max(event_id), '') FROM billing.webhook_events WHERE provider = $1`,
		retentionProvider).Scan(&n, &survivor); err != nil {
		t.Fatalf("read survivors: %v", err)
	}
	if n != 1 || survivor != "current" {
		t.Errorf("want only the current marker left, got %d rows, event %q", n, survivor)
	}
}

func TestSweepRetention_WebhookEventsDrainPastOneBatch(t *testing.T) {
	admin, mod := billingWorkerModule(t)

	if _, err := admin.Exec(t.Context(), `
		INSERT INTO billing.webhook_events (provider, event_id, received_at)
		SELECT $1, g::text, now() - make_interval(days => $2) - make_interval(secs => g)
		FROM generate_series(1, 10001) AS g`, retentionProvider, retentionSweepDays+1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := mod.SweepRetention(t.Context(), retentionSweepDays); err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}

	var left int
	if err := admin.QueryRow(t.Context(),
		`SELECT count(*) FROM billing.webhook_events WHERE provider = $1`, retentionProvider).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d left, want one sweep to drain more than one batch", left)
	}
}
