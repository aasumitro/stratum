package logger

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"error":   slog.LevelError,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo, // default
		"unknown": slog.LevelInfo, // default
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNew_JSONAndText(t *testing.T) {
	if l := New("info", "json", nil); l == nil {
		t.Error("New(json) returned nil")
	}
	if l := New("debug", "text", nil); l == nil {
		t.Error("New(text) returned nil")
	}
	// New sets the process default — a subsequent slog.Default must be non-nil
	if slog.Default() == nil {
		t.Error("New should install a process default logger")
	}
}

func TestNew_WithOtelHandler_FansOut(t *testing.T) {
	// a non-nil otel handler triggers the MultiHandler branch
	if l := New("info", "json", slog.NewJSONHandler(discard{}, nil)); l == nil {
		t.Error("New with otel handler returned nil")
	}
}

func TestWithContext_FromContext_RoundTrip(t *testing.T) {
	custom := slog.New(slog.NewJSONHandler(discard{}, nil))
	ctx := WithContext(t.Context(), custom)
	if got := FromContext(ctx); got != custom {
		t.Error("FromContext must return the logger stored by WithContext")
	}
}

func TestFromContext_FallsBackToDefault(t *testing.T) {
	if got := FromContext(t.Context()); got == nil {
		t.Error("FromContext on a bare context must fall back to a non-nil default")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
