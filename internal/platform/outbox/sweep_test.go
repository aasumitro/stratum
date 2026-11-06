package outbox_test

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/outbox"
)

func TestSweep(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	retentionDays := 30
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	id1 := uuid.NewV7().String()
	id2 := uuid.NewV7().String()
	id3 := uuid.NewV7().String()
	// Sweep only ever deletes row 1 (published + past retention) by design —
	// rows 2 and 3 are asserted to survive it, so this test must remove them
	// itself. Otherwise row 3 (published_at still NULL) looks exactly like a
	// real undelivered event to any relay running against this same
	// database, and it retries forever against a "ex"/"rk" exchange that
	// was never meant to exist.
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE id = ANY($1)`, []string{id1, id2, id3})
	})

	// 1. row with published_at set to retentionDays+1 days ago (should be swept)
	if _, err := pool.Exec(ctx, `INSERT INTO messaging.outbox (id, exchange, routing_key, payload, published_at, created_at) VALUES ($1, 'ex', 'rk', '{}', $2, $2)`, id1, cutoff.Add(-24*time.Hour)); err != nil {
		t.Fatalf("insert row 1: %v", err)
	}

	// 2. row with published_at set to retentionDays-1 days ago (should NOT be swept)
	if _, err := pool.Exec(ctx, `INSERT INTO messaging.outbox (id, exchange, routing_key, payload, published_at, created_at) VALUES ($1, 'ex', 'rk', '{}', $2, $2)`, id2, cutoff.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert row 2: %v", err)
	}

	// 3. row with published_at left NULL and created_at set to retentionDays+1 days ago (should NOT be swept)
	if _, err := pool.Exec(ctx, `INSERT INTO messaging.outbox (id, exchange, routing_key, payload, created_at) VALUES ($1, 'ex', 'rk', '{}', $2)`, id3, cutoff.Add(-24*time.Hour)); err != nil {
		t.Fatalf("insert row 3: %v", err)
	}

	if err := outbox.Sweep(ctx, pool, retentionDays); err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messaging.outbox WHERE id = $1`, id1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("expected row 1 to be deleted")
	}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messaging.outbox WHERE id = $1`, id2).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("expected row 2 to be kept")
	}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messaging.outbox WHERE id = $1`, id3).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("expected row 3 to be kept")
	}
}

// The snapshot makes the deltas exact: other packages write to this table
// concurrently, but a REPEATABLE READ transaction sees only its own changes.
func TestBacklog_ClassifiesEachRowState(t *testing.T) {
	ctx := t.Context()
	pool := testPool(t)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	dueBefore, exhaustedBefore, err := outbox.Backlog(ctx, tx)
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}

	seed := func(notBefore string, attempts int, publishedAt string) {
		t.Helper()
		if _, err := tx.Exec(ctx, `
			INSERT INTO messaging.outbox (id, exchange, routing_key, payload, not_before, attempts, published_at)
			VALUES ($1, 'ex', 'rk', '{}', now() + $2::interval, $3, now() + $4::interval)`,
			uuid.NewV7().String(), notBefore, attempts, publishedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seedUnpublished := func(notBefore string, attempts int) {
		t.Helper()
		if _, err := tx.Exec(ctx, `
			INSERT INTO messaging.outbox (id, exchange, routing_key, payload, not_before, attempts)
			VALUES ($1, 'ex', 'rk', '{}', now() + $2::interval, $3)`,
			uuid.NewV7().String(), notBefore, attempts); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	seedUnpublished("-1 minute", 0)   // due and never tried: counts as due
	seedUnpublished("30 minutes", 5)  // failed, waiting out its backoff: still counts as due
	seedUnpublished("1 month", 0)     // scheduled for later: not late, counts in neither
	seedUnpublished("-1 minute", 20)  // abandoned at the retry ceiling: exhausted, not due
	seed("-1 minute", 0, "-1 second") // already published: counts in neither

	due, exhausted, err := outbox.Backlog(ctx, tx)
	if err != nil {
		t.Fatalf("Backlog: %v", err)
	}
	if got := due - dueBefore; got != 2 {
		t.Errorf("due grew by %d, want 2", got)
	}
	if got := exhausted - exhaustedBefore; got != 1 {
		t.Errorf("exhausted grew by %d, want 1", got)
	}
}
