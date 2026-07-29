package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

func newRequestIDTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(middleware.NewRequestIDMiddleware(slog.Default()))
	e.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}

func TestNewRequestIDMiddleware_GeneratesIDWhenHeaderAbsent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	newRequestIDTestEngine().ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Error("expected a generated X-Request-ID, got empty")
	}
}

func TestNewRequestIDMiddleware_EchoesReasonableSuppliedID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "gateway-abc-123")
	w := httptest.NewRecorder()
	newRequestIDTestEngine().ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "gateway-abc-123" {
		t.Errorf("X-Request-ID = %q, want unchanged %q", got, "gateway-abc-123")
	}
}

// TestNewRequestIDMiddleware_TruncatesOversizedID confirms an
// unauthenticated, attacker-reachable header is not echoed into
// logs/response unbounded.
func TestNewRequestIDMiddleware_TruncatesOversizedID(t *testing.T) {
	oversized := strings.Repeat("a", 500)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", oversized)
	w := httptest.NewRecorder()
	newRequestIDTestEngine().ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if len(got) != 64 {
		t.Errorf("X-Request-ID length = %d, want 64", len(got))
	}
	if got != strings.Repeat("a", 64) {
		t.Errorf("X-Request-ID = %q, want the first 64 chars of the input", got)
	}
}

// TestNewRequestIDMiddleware_StripsControlCharacters guards against log
// injection via a crafted X-Request-ID (e.g. embedded newlines forging
// extra log lines).
func TestNewRequestIDMiddleware_StripsControlCharacters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "abc\r\n\x00def")
	w := httptest.NewRecorder()
	newRequestIDTestEngine().ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if strings.ContainsAny(got, "\r\n\x00") {
		t.Errorf("X-Request-ID = %q, still contains control characters", got)
	}
	if got != "abcdef" {
		t.Errorf("X-Request-ID = %q, want %q", got, "abcdef")
	}
}

// TestNewRequestIDMiddleware_AllControlCharacters_FallsBackToGeneratedID
// covers the edge case where sanitizing leaves nothing usable — must not
// echo an empty request ID back.
func TestNewRequestIDMiddleware_AllControlCharacters_FallsBackToGeneratedID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "\x00\x01\x02")
	w := httptest.NewRecorder()
	newRequestIDTestEngine().ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Error("expected a fallback generated X-Request-ID, got empty")
	}
}
