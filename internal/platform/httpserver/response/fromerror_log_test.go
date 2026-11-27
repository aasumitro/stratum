package response

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/logger"
)

// captureLog points slog's default logger at a buffer for the test's length.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestFromError_InternalLogsItsCause(t *testing.T) {
	logs := captureLog(t)
	c, w := newCtx("", "")
	cause := errors.New("pq: relation billing.nope does not exist")

	FromError(c, apperr.Internal("PLAN_CHANGE_FAILED", "failed to change plan", cause))

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "billing.nope") {
		t.Errorf("the cause reached the client: %s", w.Body)
	}
	got := logs.String()
	for _, want := range []string{"level=ERROR", "code=PLAN_CHANGE_FAILED", "billing.nope"} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q: want it to contain %q", got, want)
		}
	}
}

func TestFromError_UnclassifiedErrorIsLogged(t *testing.T) {
	logs := captureLog(t)
	c, _ := newCtx("", "")

	FromError(c, errors.New("unexpected EOF"))

	if got := logs.String(); !strings.Contains(got, "code=INTERNAL_ERROR") || !strings.Contains(got, "unexpected EOF") {
		t.Errorf("log %q: want the code and the error", got)
	}
}

func TestFromError_ClientErrorIsNotLogged(t *testing.T) {
	logs := captureLog(t)
	c, _ := newCtx("", "")

	FromError(c, apperr.Validation("BAD", "bad input"))

	if logs.Len() != 0 {
		t.Errorf("a 4xx was logged: %q", logs)
	}
}

// The 500's log line goes to the request-scoped logger, so it carries the
// request_id the client got in the body, and it is written exactly once.
func TestFromError_LogsOnceOnTheRequestLogger(t *testing.T) {
	c, _ := newCtx("", "")
	var buf bytes.Buffer
	reqLogger := slog.New(slog.NewTextHandler(&buf, nil)).With("request_id", "req-123")
	c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), reqLogger))

	FromError(c, apperr.Internal("X_FAILED", "failed", errors.New("boom")))

	got := buf.String()
	if !strings.Contains(got, "request_id=req-123") {
		t.Errorf("log %q: want the request_id", got)
	}
	if n := strings.Count(got, "request failed"); n != 1 {
		t.Errorf("want one request failed line, got %d: %q", n, got)
	}
}

// A request the client abandoned is a warning; a cancel from inside the server,
// with the request still live, stays an error.
func TestFromError_CancelledRequestIsAWarning(t *testing.T) {
	cancelled := fmt.Errorf("query: %w", context.Canceled)
	for name, abandon := range map[string]bool{"client gone": true, "request live": false} {
		logs := captureLog(t)
		c, _ := newCtx("", "")
		if abandon {
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(ctx)
		}

		FromError(c, apperr.Internal("X_FAILED", "failed", cancelled))

		want := "level=ERROR"
		if abandon {
			want = "level=WARN"
		}
		if got := logs.String(); !strings.Contains(got, want) {
			t.Errorf("%s: log %q, want %s", name, got, want)
		}
	}
}
