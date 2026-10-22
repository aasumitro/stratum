package outbox_test

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts/events"
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

// selectiveFailPublisher fails Publish only for routing keys in failRoutingKeys and
// records every other publish — used to prove a poison row's repeated failure does not
// block a healthy row claimed in the same batch.
type selectiveFailPublisher struct {
	mu              sync.Mutex
	failRoutingKeys map[string]bool
	delivered       []string // "exchange/routingKey"
}

func (p *selectiveFailPublisher) Publish(_ context.Context, exchange, routingKey string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failRoutingKeys[routingKey] {
		return context.DeadlineExceeded
	}
	p.delivered = append(p.delivered, exchange+"/"+routingKey)
	return nil
}

func (p *selectiveFailPublisher) has(entry string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.delivered, entry)
}

func seedOutboxRow(t *testing.T, pool *pgxpool.Pool, routingKey string, notBefore time.Time, publishedAt *time.Time) {
	t.Helper()
	id := uuid.NewV7().String()
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

// seedOutboxRowAtAttempts inserts a row that is immediately due (not_before defaults to
// now()) with attempts pre-set to the given value, so a test can put a row one failure
// short of maxAttempts without driving real backoff through every prior attempt.
func seedOutboxRowAtAttempts(t *testing.T, pool *pgxpool.Pool, routingKey string, attempts int) {
	t.Helper()
	id := uuid.NewV7().String()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload, attempts)
		VALUES ($1, 'test.exchange', $2, '{}', $3)`,
		id, routingKey, attempts,
	)
	if err != nil {
		t.Fatalf("seed outbox row %s at attempts=%d: %v", routingKey, attempts, err)
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

// TestIntegration_RelayBatch_PoisonRowExhaustsAttempts_HealthyRowStillPublishes is the
// regression test for F-01: before claimUnpublished excluded exhausted rows, a row that
// could never publish was reclaimed forever. This seeds one row one failure short of the
// cap and one healthy row, and proves the poison row is claimed exactly once more
// (crossing the cap, never again), while the healthy row publishes regardless.
func TestIntegration_RelayBatch_PoisonRowExhaustsAttempts_HealthyRowStillPublishes(t *testing.T) {
	const maxAttempts = 20 // must match outbox.maxAttempts (relay.go) — unexported, duplicated here
	pool := testPool(t)

	seedOutboxRowAtAttempts(t, pool, "test.poison", maxAttempts-1)
	seedOutboxRow(t, pool, "test.healthy-behind-poison", time.Now().Add(-time.Minute), nil)

	pub := &selectiveFailPublisher{failRoutingKeys: map[string]bool{"test.poison": true}}
	relay := outbox.NewRelay(pool, pub)

	var poisonAttempts int
	var poisonPublishedAt *time.Time
	drainUntil(t, relay, func() bool {
		pool.QueryRow(context.Background(),
			`SELECT attempts, published_at FROM messaging.outbox WHERE routing_key = 'test.poison'`,
		).Scan(&poisonAttempts, &poisonPublishedAt)
		return pub.has("test.exchange/test.healthy-behind-poison") && poisonAttempts >= maxAttempts
	})

	if poisonPublishedAt != nil {
		t.Error("poison row got published_at set despite the publisher always failing it")
	}
	if poisonAttempts != maxAttempts {
		t.Errorf("attempts = %d, want exactly %d (attempts < maxAttempts excludes it from every claim past the cap, so it can never be incremented beyond it)", poisonAttempts, maxAttempts)
	}

	relay.RelayBatch(context.Background())
	var poisonAttemptsAfter int
	if err := pool.QueryRow(t.Context(),
		`SELECT attempts FROM messaging.outbox WHERE routing_key = 'test.poison'`,
	).Scan(&poisonAttemptsAfter); err != nil {
		t.Fatalf("query poison row after extra cycle: %v", err)
	}
	if poisonAttemptsAfter != poisonAttempts {
		t.Errorf("attempts changed from %d to %d after an extra RelayBatch — poison row is still being reclaimed past the cap", poisonAttempts, poisonAttemptsAfter)
	}
}

// capturingHandler is a slog.Handler test double that records every emitted slog.Record — used to
// assert on the exact log line RelayBatch emits for an exhausted row, since that transition has no
// other observable signal (no counter, no dead-letter, only the log).
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) find(level slog.Level, message string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level && r.Message == message {
			return r, true
		}
	}
	return slog.Record{}, false
}

func attrInt64(r slog.Record, key string) (val int64, found bool) {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val, found = a.Value.Int64(), true
			return false
		}
		return true
	})
	return val, found
}

// TestIntegration_RelayBatch_ExhaustedRowLogsDistinctExhaustionMessage is the regression test for
// the outbox exhaustion signal: before this fix, a row's final log line at maxAttempts still read
// "will retry" — indistinguishable from every retry before it, so an operator had no way to tell a
// delayed row from a permanently abandoned one. This seeds a row one failure short of the cap,
// drains until the failure that crosses it fires, and asserts the resulting log line is the
// distinct ERROR exhaustion message with attempts == maxAttempts, not the ordinary WARN retry line.
func TestIntegration_RelayBatch_ExhaustedRowLogsDistinctExhaustionMessage(t *testing.T) {
	const maxAttempts = 20 // must match outbox.maxAttempts (relay.go) — unexported, duplicated here
	pool := testPool(t)

	seedOutboxRowAtAttempts(t, pool, "test.exhaust-log", maxAttempts-1)

	handler := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	pub := &selectiveFailPublisher{failRoutingKeys: map[string]bool{"test.exhaust-log": true}}
	relay := outbox.NewRelay(pool, pub)

	var attempts int
	drainUntil(t, relay, func() bool {
		pool.QueryRow(context.Background(),
			`SELECT attempts FROM messaging.outbox WHERE routing_key = 'test.exhaust-log'`,
		).Scan(&attempts)
		return attempts >= maxAttempts
	})

	rec, ok := handler.find(slog.LevelError, "outbox: row exhausted, ABANDONED — will never be delivered")
	if !ok {
		t.Fatal("no ERROR-level exhaustion log emitted for the row that just crossed maxAttempts")
	}
	if logged, ok := attrInt64(rec, "attempts"); !ok || logged != maxAttempts {
		t.Errorf("exhaustion log attempts = %v (found=%v), want %d", logged, ok, maxAttempts)
	}
}

// TestIntegration_DelayedEvent_RelayDeliversToRealExchange proves a delayed
// event enqueued on the real billing exchange with the real routing key is
// published straight to billing.events once its not_before is due. This is
// the relay-boundary coverage whose absence let delayed billing events route
// to a parking exchange and silently sit in a TTL-less, consumer-less queue
// forever — pointing EnqueueDelayed's call sites back at a delay
// exchange/key makes this fail.
func TestIntegration_DelayedEvent_RelayDeliversToRealExchange(t *testing.T) {
	pool := testPool(t)

	orgID := uuid.NewV7().String()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	// delay = 0 -> not_before = now(), immediately due.
	if err := events.EnqueueDelayed(t.Context(), tx, events.ExchangeBilling,
		events.RoutingKeySubscriptionCheck, "billing", orgID,
		events.SubscriptionCheck{SubscriptionID: uuid.NewV7().String(), SubjectType: "organization", SubjectID: orgID},
		0); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("EnqueueDelayed: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID)
	})

	pub := &fakePublisher{}
	relay := outbox.NewRelay(pool, pub)

	want := events.ExchangeBilling + "/" + events.RoutingKeySubscriptionCheck
	drainUntil(t, relay, func() bool { return pub.has(want) })

	var publishedAt *time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT published_at FROM messaging.outbox WHERE payload->>'org_id' = $1`, orgID,
	).Scan(&publishedAt); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if publishedAt == nil {
		t.Error("delayed row was handed to the publisher but never marked published")
	}
}
