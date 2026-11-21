package middleware

import "github.com/gin-gonic/gin"

// SecureHeaders sets standard security headers on HTTP responses. hsts adds
// Strict-Transport-Security: off in development, where browsers would otherwise
// pin localhost to HTTPS. It leaves out includeSubDomains and preload because the
// API host cannot speak for its sibling hosts.
func SecureHeaders(hsts bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store") // per-tenant JSON should not be cached
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		c.Next()
	}
}
