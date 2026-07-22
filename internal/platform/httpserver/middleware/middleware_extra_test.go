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

func init() { gin.SetMode(gin.TestMode) }

// --- MaxBodySize ---

func TestMaxBodySize_OverLimit_Returns413(t *testing.T) {
	e := gin.New()
	e.Use(middleware.MaxBodySize(8))
	e.POST("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("this body is definitely longer than eight bytes"))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: want 413, got %d", w.Code)
	}
}

func TestMaxBodySize_UnderLimit_Passes(t *testing.T) {
	e := gin.New()
	e.Use(middleware.MaxBodySize(1024))
	e.POST("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("small"))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("small body: want 200, got %d", w.Code)
	}
}

// --- Recovery ---

func TestRecovery_PanicBecomes500_NoLeak(t *testing.T) {
	e := gin.New()
	e.Use(middleware.NewRecoveryMiddleware())
	e.GET("/boom", func(_ *gin.Context) { panic("secret internal detail: /etc/passwd") })

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic: want 500, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "secret internal detail") {
		t.Errorf("recovery must not leak the panic value to the client: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), "internal server error") {
		t.Errorf("want generic error body, got %s", w.Body)
	}
}

// --- RequestID ---

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	e := gin.New()
	e.Use(middleware.NewRequestIDMiddleware(slog.Default()))
	e.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Error("expected a generated X-Request-ID on the response")
	}
}

func TestRequestID_ReusesIncomingHeader(t *testing.T) {
	e := gin.New()
	e.Use(middleware.NewRequestIDMiddleware(slog.Default()))
	e.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("X-Request-ID", "upstream-req-123")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if got := w.Header().Get("X-Request-ID"); got != "upstream-req-123" {
		t.Errorf("want incoming request id echoed, got %q", got)
	}
}

// --- ParseCORSOrigins ---

func TestParseCORSOrigins(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"https://a.com", []string{"https://a.com"}},
		{"https://a.com, https://b.com ,https://c.com", []string{"https://a.com", "https://b.com", "https://c.com"}},
		{"https://a.com,,  ,https://b.com", []string{"https://a.com", "https://b.com"}},
	}
	for _, c := range cases {
		got := middleware.ParseCORSOrigins(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseCORSOrigins(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseCORSOrigins(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// --- rate-limit key functions ---

func TestByClientIP_UsesRemoteAddr(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "203.0.113.7:5555"
	if got := middleware.ByClientIP(c); got != "203.0.113.7" {
		t.Errorf("ByClientIP = %q, want 203.0.113.7", got)
	}
}

func TestByAuthenticatedSubject_PrefersClaims(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("auth.claims", middleware.Claims{Subject: "user-sub-42"})
	if got := middleware.ByAuthenticatedSubject(c); got != "user-sub-42" {
		t.Errorf("ByAuthenticatedSubject with claims = %q, want user-sub-42", got)
	}
}

func TestByAuthenticatedSubject_FallsBackToIP(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "198.51.100.9:443"
	// no claims set → falls back to client IP
	if got := middleware.ByAuthenticatedSubject(c); got != "198.51.100.9" {
		t.Errorf("ByAuthenticatedSubject without claims = %q, want 198.51.100.9", got)
	}
}
