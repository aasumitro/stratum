package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/logger"
)

// NewRecoveryMiddleware recovers from a panic anywhere downstream,
// logs it (with stack trace) through the request-scoped logger so it's
// correlated with request_id/trace_id, and returns a generic 500 —
// never the panic value or stack trace itself, since that can leak
// internal details (file paths, SQL, struct field names) to the client.
//
// This replaces gin.Recovery() so panics go through our structured
// logger instead of gin's default stderr writer.
func NewRecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log := logger.FromContext(c.Request.Context())
				log.Error("panic recovered",
					"panic", rec,
					"stack", string(debug.Stack()),
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
		}()
		c.Next()
	}
}
