package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/aasumitro/stratum/internal/platform/messaging"
	"github.com/aasumitro/stratum/internal/platform/otel"
	"github.com/aasumitro/stratum/internal/testsupport"
)

func TestRunRetentionSweeps_OneFailureDoesNotStopTheOthers(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	failures := otel.Counter("stratum.retention.sweep.failures", "")

	var ran []string
	sweep := func(module string, removed int64, err error) retentionSweep {
		return retentionSweep{module, func(context.Context) (int64, error) {
			ran = append(ran, module)
			return removed, err
		}}
	}

	var logs bytes.Buffer
	runRetentionSweeps(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)), failures, []retentionSweep{
		sweep("account", 7, nil),
		sweep("notification", 0, errors.New("boom")),
		sweep("organization", 3, errors.New("bang")),
		sweep("billing", 0, nil),
	})

	if want := []string{"account", "notification", "organization", "billing"}; !slices.Equal(ran, want) {
		t.Errorf("ran %v, want %v", ran, want)
	}
	out := logs.String()
	for _, want := range []string{"module=notification", "module=organization", "boom", "bang"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
	if got := strings.Count(out, "level=WARN"); got != 2 {
		t.Errorf("want one Warn per failure, got %d: %s", got, out)
	}
	if !strings.Contains(out, "module=account rows=7") || !strings.Contains(out, "module=organization rows=3") {
		t.Errorf("want what was removed logged, including before a partial failure: %s", out)
	}
	if strings.Contains(out, "module=billing") {
		t.Errorf("a sweep that removed nothing should stay quiet: %s", out)
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.retention.sweep.failures"); got != 2 {
		t.Errorf("failures = %d, want 2", got)
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.retention.sweep.failures", attribute.String("module", "notification")); got != 1 {
		t.Errorf("failures{module=notification} = %d, want 1", got)
	}
}

func TestRunRetentionSweeps_ShutdownIsNotAFailure(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	failures := otel.Counter("stratum.retention.sweep.failures", "")

	ctx, cancel := context.WithCancel(t.Context())
	var ran int
	var logs bytes.Buffer
	runRetentionSweeps(ctx, slog.New(slog.NewTextHandler(&logs, nil)), failures, []retentionSweep{
		{"account", func(context.Context) (int64, error) {
			ran++
			cancel()
			return 0, context.Canceled
		}},
		{"billing", func(context.Context) (int64, error) {
			ran++
			return 0, nil
		}},
	})

	if ran != 1 {
		t.Errorf("ran %d sweeps, want to stop at the first after shutdown began", ran)
	}
	if logs.Len() != 0 {
		t.Errorf("shutdown logged: %s", logs.String())
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.retention.sweep.failures"); got != 0 {
		t.Errorf("failures = %d, want 0", got)
	}
}

func TestDLQNames_ListsEachQueueOnce(t *testing.T) {
	spec := func(dlq string) *messaging.Consumer {
		return messaging.NewConsumer(nil, messaging.ConsumerSpec{DLX: messaging.DLXSpec{QueueName: dlq}}, nil, nil)
	}
	got := dlqNames([]*messaging.Consumer{spec("b.dlq"), spec("a.dlq"), spec("b.dlq"), spec("a.dlq")})
	if want := []string{"a.dlq", "b.dlq"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWorkerGauges_ReportSeededValues(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Neither row can be claimed by a relay test running in another package: one is
	// waiting out a backoff, and the other has used up its attempts.
	retrying, abandoned := uuid.NewV7().String(), uuid.NewV7().String()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM messaging.outbox WHERE id = ANY($1)`, []string{retrying, abandoned})
	})
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO messaging.outbox (id, exchange, routing_key, payload, not_before, attempts) VALUES
			($1, 'ex', 'rk', '{}', now() + interval '1 hour', 3),
			($2, 'ex', 'rk', '{}', now() - interval '1 hour', 20)`, retrying, abandoned); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	reader := testsupport.ManualMetricReader(t)
	depths := map[string]int{"a.dlq": 3, "b.dlq": 0}
	queueMessages := func(queue string) (int, error) {
		n, ok := depths[queue]
		if !ok {
			return 0, fmt.Errorf("no such queue %q", queue)
		}
		return n, nil
	}

	var logs bytes.Buffer
	registration, err := registerWorkerGauges(pool, queueMessages,
		[]string{"a.dlq", "b.dlq", "missing.dlq"}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("registerWorkerGauges: %v", err)
	}
	t.Cleanup(func() { _ = registration.Unregister() })

	// Other packages write to this table concurrently, so only a lower bound holds.
	if due, ok := testsupport.GaugeValue(t, reader, "stratum.outbox.due_unpublished"); !ok || due < 1 {
		t.Errorf("due_unpublished = %d (reported %v), want at least the seeded row", due, ok)
	}
	if exhausted, ok := testsupport.GaugeValue(t, reader, "stratum.outbox.exhausted"); !ok || exhausted < 1 {
		t.Errorf("exhausted = %d (reported %v), want at least the seeded row", exhausted, ok)
	}
	for queue, want := range depths {
		got, ok := testsupport.GaugeValue(t, reader, "stratum.dlq.messages", attribute.String("queue", queue))
		if !ok || got != int64(want) {
			t.Errorf("dlq.messages{queue=%s} = %d (reported %v), want %d", queue, got, ok, want)
		}
	}
	if _, ok := testsupport.GaugeValue(t, reader, "stratum.dlq.messages", attribute.String("queue", "missing.dlq")); ok {
		t.Error("a queue that failed to answer must not report a value")
	}
	if !strings.Contains(logs.String(), "queue=missing.dlq") {
		t.Errorf("want a Warn naming the failed queue: %s", logs.String())
	}
}
