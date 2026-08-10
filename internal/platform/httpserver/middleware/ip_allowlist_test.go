package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

// ipAllowlistEngine builds a test engine with no trusted proxies configured
// — matching this codebase's safe production default (httpserver.New with
// an empty config.TrustedProxies) — so Context.ClientIP() falls back to the
// raw TCP peer address exactly like a directly-exposed service would see.
func ipAllowlistEngine(t *testing.T, cidrs []string) *gin.Engine {
	t.Helper()
	mw, err := middleware.NewIPAllowlistMiddleware(cidrs)
	if err != nil {
		t.Fatalf("NewIPAllowlistMiddleware: %v", err)
	}
	e := gin.New()
	if err := e.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	e.Use(mw)
	e.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}

func TestIPAllowlist_EmptyList_AllowsAll(t *testing.T) {
	e := ipAllowlistEngine(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("empty cidrs: want 200, got %d", w.Code)
	}
}

func TestIPAllowlist_InRange_Allowed(t *testing.T) {
	e := ipAllowlistEngine(t, []string{"203.0.113.0/24"})
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("in-range peer: want 200, got %d", w.Code)
	}
}

func TestIPAllowlist_OutOfRange_Forbidden(t *testing.T) {
	e := ipAllowlistEngine(t, []string{"203.0.113.0/24"})
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "198.51.100.9:5555"
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("out-of-range peer: want 403, got %d", w.Code)
	}
}

// With no trusted proxy configured (this codebase's default), a caller
// forging X-Forwarded-For must not be able to talk its way past the
// allowlist — the whole point of gating ClientIP() behind
// SetTrustedProxies instead of trusting every peer, gin's own default.
func TestIPAllowlist_NoTrustedProxy_ForwardedForHeaderIsIgnored(t *testing.T) {
	e := ipAllowlistEngine(t, []string{"203.0.113.0/24"})
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "198.51.100.9:5555" // real peer: outside the allowlist
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("spoofed X-Forwarded-For with no trusted proxy configured should not bypass the allowlist: want 403, got %d", w.Code)
	}
}

// Once the immediate peer is explicitly configured as a trusted proxy
// (matching a real deployment behind a load balancer/ingress),
// X-Forwarded-For from that trusted hop must be honored — otherwise every
// request behind a real proxy would resolve to the proxy's own IP and the
// allowlist would reject legitimate traffic outright.
func TestIPAllowlist_TrustedProxy_HonorsForwardedFor(t *testing.T) {
	mw, err := middleware.NewIPAllowlistMiddleware([]string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("NewIPAllowlistMiddleware: %v", err)
	}
	e := gin.New()
	if err := e.SetTrustedProxies([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	e.Use(mw)
	e.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "10.0.0.5:5555" // the trusted proxy's own address
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("real client behind a trusted proxy should be resolved via X-Forwarded-For: want 200, got %d", w.Code)
	}
}

func TestNewIPAllowlistMiddleware_InvalidCIDR_ReturnsError(t *testing.T) {
	_, err := middleware.NewIPAllowlistMiddleware([]string{"203.0.113.0/24", "not-a-cidr"})
	if err == nil {
		t.Fatal("want an error for a malformed CIDR entry, got nil")
	}
}
