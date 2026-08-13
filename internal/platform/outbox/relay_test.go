package outbox_test

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/outbox"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakePublisher records every exchange/routingKey Publish is called with, so
// a test can assert exactly which rows the relay delivered without a real
// broker in this binary.
type fakePublisher struct {
	mu        sync.Mutex
	delivered []string // "exchange/routingKey"
}

func (p *fakePublisher) Publish(_ context.Context, exchange, routingKey string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delivered = append(p.delivered, exchange+"/"+routingKey)
	return nil
}

func (p *fakePublisher) has(entry string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.delivered, entry)
}

func (p *fakePublisher) PublishDelayed(_ context.Context, _, _ string, _ []byte, _ time.Duration) error {
	return nil
}

func seedOutboxRow(t *testing.T, pool *pgxpool.Pool, routingKey string, notBefore time.Time, publishedAt *time.Time) {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload, not_before, published_at)
		VALUES ($1, 'test.exchange', $2, '{}', $3, $4)`,
		id, routingKey, notBefore, publishedAt,
	)
	if err != nil {
		t.Fatalf("seed outbox row %s: %v", routingKey, err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE id = $1`, id)
	})
}

// drainUntil calls relay.RelayBatch repeatedly until condition reports true
// or 20 seconds elapse. messaging.outbox is one shared table with no
// per-test filter — RelayBatch's claim query has no test-scoping mechanism —
// and every other package's integration tests write real due rows to it
// concurrently via events.Enqueue. A single RelayBatch call can therefore
// legitimately claim a batch made up entirely of other packages' rows
// before it ever reaches this test's own — draining repeatedly (each older
// row gets exhausted eventually, since ORDER BY created_at always prefers
// them) is what makes these tests deterministic instead of flaky.
func drainUntil(t *testing.T, relay *outbox.Relay, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		relay.RelayBatch(context.Background())
		if condition() {
			return
		}
	}
	t.Fatal("condition never became true after draining messaging.outbox for 20s")
}

// TestIntegration_RelayBatch_PublishesOnlyTheOneDueRow seeds one already
// published row, one not-yet-due (future not_before) row, and one
// immediately due row, then asserts the due one is (eventually) delivered
// and marked published, while the not-yet-due row never is — proving the
// relay's WHERE clause correctly separates "nothing to do yet" and
// "already done" from "actually due now", regardless of how many batches it
// takes to drain concurrent noise from other packages' tests.
func TestIntegration_RelayBatch_PublishesOnlyTheOneDueRow(t *testing.T) {
	pool := testPool(t)
	now := time.Now()
	alreadyPublished := now.Add(-time.Hour)

	seedOutboxRow(t, pool, "test.already-published", now.Add(-time.Hour), &alreadyPublished)
	seedOutboxRow(t, pool, "test.not-yet-due", now.Add(time.Hour), nil)
	seedOutboxRow(t, pool, "test.due-now", now.Add(-time.Minute), nil)

	pub := &fakePublisher{}
	relay := outbox.NewRelay(pool, pub)

	var dueRowPublishedAt *time.Time
	drainUntil(t, relay, func() bool {
		pool.QueryRow(context.Background(),
			`SELECT published_at FROM messaging.outbox WHERE routing_key = 'test.due-now'`,
		).Scan(&dueRowPublishedAt)
		return dueRowPublishedAt != nil
	})

	if !pub.has("test.exchange/test.due-now") {
		t.Error("the due row was marked published in the DB but never actually handed to the publisher")
	}

	var untouchedPublishedAt *time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT published_at FROM messaging.outbox WHERE routing_key = 'test.not-yet-due'`,
	).Scan(&untouchedPublishedAt); err != nil {
		t.Fatalf("query not-yet-due row: %v", err)
	}
	if untouchedPublishedAt != nil {
		t.Error("not-yet-due row got published_at set — it should never be claimed regardless of how many batches ran")
	}
}

// alwaysFailPublisher always errors — used to prove a publish failure
// records the attempt instead of silently dropping it: a broker hiccup no
// longer loses the event, it just retries on the next tick.
type alwaysFailPublisher struct{}

func (alwaysFailPublisher) Publish(context.Context, string, string, []byte) error {
	return context.DeadlineExceeded
}
func (alwaysFailPublisher) PublishDelayed(context.Context, string, string, []byte, time.Duration) error {
	return context.DeadlineExceeded
}

// TestIntegration_RelayBatch_PublishFailure_RecordsAttemptAndRetries proves
// a publish failure leaves the row unpublished with attempts/last_error set
// (visible to the reconciliation query in docs/12-operations.md), rather
// than silently dropping it.
func TestIntegration_RelayBatch_PublishFailure_RecordsAttemptAndRetries(t *testing.T) {
	pool := testPool(t)
	seedOutboxRow(t, pool, "test.will-fail", time.Now().Add(-time.Minute), nil)

	relay := outbox.NewRelay(pool, alwaysFailPublisher{})

	var attempts int
	var lastError *string
	drainUntil(t, relay, func() bool {
		pool.QueryRow(context.Background(),
			`SELECT attempts, last_error FROM messaging.outbox WHERE routing_key = 'test.will-fail'`,
		).Scan(&attempts, &lastError)
		return attempts > 0
	})

	var publishedAt *time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT published_at FROM messaging.outbox WHERE routing_key = 'test.will-fail'`,
	).Scan(&publishedAt); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if publishedAt != nil {
		t.Error("row got published_at set despite the publisher always failing")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (this row must be claimed at most once per RelayBatch call)", attempts)
	}
	if lastError == nil || *lastError == "" {
		t.Error("last_error is empty, want the publish error recorded")
	}
}
