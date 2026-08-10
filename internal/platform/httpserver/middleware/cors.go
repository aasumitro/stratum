package middleware

import (
	"cmp"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/geoip"
)

// CORSConfig holds the allowed origins for CORS. Empty allows all — only
// permitted when IsDevelopment is true, see NewCORSMiddleware.
type CORSConfig struct {
	AllowedOrigins []string
	IsDevelopment  bool
}

// NewCORSMiddleware returns middleware that sets CORS headers.
//
// With AllowedOrigins empty, every origin is reflected back and granted
// credentialed access — acceptable only in development, where it's local
// ergonomics (e.g. the SSE cookie-auth fallback working across dev ports).
// Outside development an empty allowlist is a startup error.
func NewCORSMiddleware(cfg CORSConfig) (gin.HandlerFunc, error) {
	if len(cfg.AllowedOrigins) == 0 && !cfg.IsDevelopment {
		return nil, errors.New("middleware.NewCORSMiddleware: CORS_ORIGINS must be set outside development")
	}

	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// The response varies per request based on Origin (Allow-Origin is
		// reflected, not fixed) — without this, a shared/CDN cache could
		// serve one origin's Allow-Origin/Allow-Credentials pair to a
		// different origin's request.
		c.Header("Vary", "Origin")

		// Access-Control-Allow-Credentials is only ever sent alongside a
		// matched/reflected origin — never unconditionally — since pairing
		// it with a response that has no (or a wildcard) Allow-Origin is
		// exactly the "credentials from anywhere" hole this fixes.
		if len(allowed) == 0 || allowed[origin] {
			origin = cmp.Or(origin, "*")
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		// X-Debug-Country-Code is only ever honored server-side in development
		// (geoip.Resolver.CountryCode) — allowed here in every environment
		// regardless, since CORS only governs what a browser may attach
		// cross-origin, not what the server trusts; a stray header on a
		// staging/production request is silently ignored either way.
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Organization-ID, X-Request-ID, Idempotency-Key, "+geoip.DebugCountryCodeHeader)
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}, nil
}

// ParseCORSOrigins splits a comma-separated string into origins.
func ParseCORSOrigins(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
