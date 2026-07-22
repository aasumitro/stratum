// Package logger builds a process-wide *slog.Logger and provides a way to
// attach request-scoped fields (request ID, org ID) via context, so every
// module logs through the same structure without importing each other.
package logger

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

// New builds the root logger. format is "json" (production) or "text"
// (local dev, human-readable). level is one of debug|info|warn|error.
// otelHandler is optional (pass nil to skip it — this is the normal case
// when OTel is disabled, see otel.Setup): when provided, every log record
// is sent to both stdout and the OTel collector via a fan-out handler, so
// local tailing and centralized observability both work from one call site.
func New(level, format string, otelHandler slog.Handler) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var consoleHandler slog.Handler
	if format == "text" {
		consoleHandler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		consoleHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	handler := consoleHandler
	if otelHandler != nil {
		// slog.MultiHandler is a Go 1.26 stdlib addition — fans a record
		// out to every handler given, exactly the behavior we want for
		// console + OTel. If this project ever needs to build with an
		// older Go toolchain, fall back to a small hand-rolled
		// implementation (Enabled/Handle/WithAttrs/WithGroup looping
		// over a []slog.Handler, cloning the record per the slog
		// handler-writing guide).
		handler = slog.NewMultiHandler(consoleHandler, otelHandler)
	}

	l := slog.New(handler)
	slog.SetDefault(l) // so any stray slog.Info() calls still go somewhere sane
	return l
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithContext attaches a logger to ctx. Middleware calls this once per
// request after adding request_id/org_id attributes via .With(...).
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves the request-scoped logger, falling back to the
// process default if none was attached (e.g. in a background goroutine
// that forgot to propagate it — better a generic log line than a panic).
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
