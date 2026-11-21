package audit

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"github.com/aasumitro/stratum/internal/testsupport"
)

type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

// dropWarnings returns the count attribute of each "audit events dropped" Warn.
func (h *captureHandler) dropWarnings() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var counts []int64
	for _, r := range h.records {
		if r.Message != "audit events dropped" || r.Level != slog.LevelWarn {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "count" {
				counts = append(counts, a.Value.Int64())
			}
			return true
		})
	}
	return counts
}

func TestEnqueue_FullBufferDropsAndCounts(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	w := newWriter(nil, slog.Default(), 400, 1)

	w.enqueue(event{action: "kept"})
	if got := testsupport.CounterTotal(t, reader, "stratum.audit.events.dropped"); got != 0 {
		t.Errorf("before overflow: dropped = %d, want 0", got)
	}

	w.enqueue(event{action: "lost"})
	if got := testsupport.CounterTotal(t, reader, "stratum.audit.events.dropped"); got != 1 {
		t.Errorf("after overflow: dropped = %d, want 1", got)
	}
	if got := w.droppedSinceLog.Load(); got != 1 {
		t.Errorf("droppedSinceLog = %d, want 1", got)
	}
	if e := <-w.ch; e.action != "kept" {
		t.Errorf("buffer holds %q, want the event enqueued first", e.action)
	}
}

// Drops are reported at most once per minute, with the count since the last
// report, and nothing is logged for a minute without drops.
func TestRun_DropWarningIsRateLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		capture := &captureHandler{}
		w := newWriter(nil, slog.New(capture), 400, 1)
		go w.run()

		w.droppedSinceLog.Add(3)
		w.droppedSinceLog.Add(2)
		synctest.Wait()
		if got := capture.dropWarnings(); len(got) != 0 {
			t.Errorf("warned before the interval elapsed: %v", got)
		}

		time.Sleep(dropLogInterval + time.Second)
		synctest.Wait()
		if got := capture.dropWarnings(); len(got) != 1 || got[0] != 5 {
			t.Errorf("after one interval: warnings = %v, want [5]", got)
		}

		time.Sleep(dropLogInterval)
		synctest.Wait()
		if got := capture.dropWarnings(); len(got) != 1 {
			t.Errorf("an interval without drops logged again: %v", got)
		}

		w.droppedSinceLog.Add(1)
		close(w.ch)
		<-w.done
		if got := capture.dropWarnings(); len(got) != 2 || got[1] != 1 {
			t.Errorf("shutdown must report what is left: warnings = %v", got)
		}
	})
}

func TestFlush_FailureIsCounted(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	// Nothing listens on port 1, so the batch fails without needing a database.
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	w := newWriter(pool, slog.New(slog.DiscardHandler), 400, 1)
	w.flush([]event{{actor: "a", action: "POST", resource: "/r", statusCode: 500, metadata: []byte("{}")}})

	if got := testsupport.CounterTotal(t, reader, "stratum.audit.write.failures"); got != 1 {
		t.Errorf("write failures = %d, want 1", got)
	}
}

func TestCleanup_FailureIsCounted(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	// Nothing listens on port 1, so the sweep fails without needing a database.
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	w := newWriter(pool, slog.New(slog.DiscardHandler), 400, 1)
	w.cleanup(t.Context())

	if got := testsupport.CounterTotal(t, reader, "stratum.retention.sweep.failures", attribute.String("module", "audit")); got != 1 {
		t.Errorf("sweep failures = %d, want 1", got)
	}
}

func engineWith(mw gin.HandlerFunc, status int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(mw)
	e.Any("/probe", func(c *gin.Context) { c.Status(status) })
	return e
}

func TestAuthFailures_CountedOnEveryMethodIncludingUnauditedGET(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	w := newWriter(nil, slog.Default(), 400, 16)

	for _, tc := range []struct {
		method string
		status int
	}{
		{http.MethodGet, http.StatusUnauthorized},
		{http.MethodPost, http.StatusForbidden},
		{http.MethodPost, http.StatusBadRequest},
		{http.MethodGet, http.StatusOK},
	} {
		engineWith(w.Middleware(), tc.status).ServeHTTP(
			httptest.NewRecorder(), httptest.NewRequest(tc.method, "/probe", strings.NewReader("{}")))
	}

	route := attribute.String("route", "/probe")
	if got := testsupport.CounterTotal(t, reader, "stratum.http.auth.failures", attribute.Int("status", 401), route); got != 1 {
		t.Errorf("401 count = %d, want 1", got)
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.http.auth.failures", attribute.Int("status", 403), route); got != 1 {
		t.Errorf("403 count = %d, want 1", got)
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.http.auth.failures"); got != 2 {
		t.Errorf("total = %d, want only the 401 and the 403", got)
	}
}

func TestFailureMiddleware_RecordsOnlyFailuresWithoutTheBody(t *testing.T) {
	reader := testsupport.ManualMetricReader(t)
	w := newWriter(nil, slog.Default(), 400, 16)

	request := func(status int) {
		req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(`{"card":"secret"}`))
		req.Header.Set("User-Agent", "provider-agent")
		engineWith(w.FailureMiddleware(), status).ServeHTTP(httptest.NewRecorder(), req)
	}

	request(http.StatusOK)
	if len(w.ch) != 0 {
		t.Fatalf("a 200 callback must not be audited, %d events queued", len(w.ch))
	}

	request(http.StatusUnauthorized)
	if len(w.ch) != 1 {
		t.Fatalf("want one event for the 401, got %d", len(w.ch))
	}
	e := <-w.ch
	if e.actor != "anonymous" || e.action != http.MethodPost || e.resource != "/probe" || e.statusCode != http.StatusUnauthorized {
		t.Errorf("event = %+v", e)
	}
	if string(e.metadata) != "{}" {
		t.Errorf("metadata = %s, want {}", e.metadata)
	}
	if e.userAgent != "provider-agent" {
		t.Errorf("userAgent = %q", e.userAgent)
	}
	if got := testsupport.CounterTotal(t, reader, "stratum.http.auth.failures", attribute.Int("status", 401)); got != 1 {
		t.Errorf("auth failures = %d, want 1", got)
	}
}
