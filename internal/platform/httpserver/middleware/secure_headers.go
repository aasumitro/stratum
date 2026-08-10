package middleware

import "github.com/gin-gonic/gin"

// SecureHeaders sets standard security headers on HTTP responses.
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store") // per-tenant JSON should not be cached
		c.Next()
	}
}
