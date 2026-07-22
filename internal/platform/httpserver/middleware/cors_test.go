package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

func TestNewCORSMiddleware_EmptyOriginsOutsideDevelopment_Errors(t *testing.T) {
	_, err := middleware.NewCORSMiddleware(middleware.CORSConfig{})
	if err == nil {
		t.Fatal("expected an error when AllowedOrigins is empty and IsDevelopment is false, got nil")
	}
}

func TestNewCORSMiddleware_EmptyOriginsInDevelopment_ReflectsWithCredentials(t *testing.T) {
	mw, err := middleware.NewCORSMiddleware(middleware.CORSConfig{IsDevelopment: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(mw)
	e.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example" {
		t.Errorf("Allow-Origin = %q, want reflected origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
}

func TestNewCORSMiddleware_ConfiguredOrigins_RejectsUnlistedOriginWithoutCredentials(t *testing.T) {
	mw, err := middleware.NewCORSMiddleware(middleware.CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(mw)
	e.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for a disallowed origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q, want empty for a disallowed origin", got)
	}
}

func TestNewCORSMiddleware_ConfiguredOrigins_AllowsListedOriginWithCredentials(t *testing.T) {
	mw, err := middleware.NewCORSMiddleware(middleware.CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(mw)
	e.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want https://app.example.com", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
}

// TestNewCORSMiddleware_SetsVaryOrigin regression-tests that every response
// carries Vary: Origin, matched or not — the Allow-Origin/Allow-Credentials
// pair differs per request Origin, so a shared cache without this header
// could serve one origin's CORS response to a different origin's request.
func TestNewCORSMiddleware_SetsVaryOrigin(t *testing.T) {
	mw, err := middleware.NewCORSMiddleware(middleware.CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(mw)
	e.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, origin := range []string{"https://app.example.com", "https://evil.example"} {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("origin %q: Vary = %q, want Origin", origin, got)
		}
	}
}
