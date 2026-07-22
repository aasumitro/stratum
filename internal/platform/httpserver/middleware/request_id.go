package middleware

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/aasumitro/stratum/internal/platform/logger"
)

const requestIDHeader = "X-Request-ID"

// NewRequestIDMiddleware generates a request ID (or reuses one supplied
// by the caller/an upstream proxy in the X-Request-ID header — common
// when this service sits behind a gateway that already assigns one),
// echoes it back on the response, and binds a child logger carrying
// request_id (plus trace_id/span_id, if otelgin.Middleware ran earlier
// in the chain and attached a span to the request context) so every log
// line for this request is automatically correlated — see
// internal/platform/logger.FromContext, which handler/service code calls
// to retrieve this logger rather than using the package-level default.
//
// Must run AFTER otelgin.Middleware (if used) so trace.SpanContextFromContext
// finds a span, and BEFORE any other middleware that logs, so they get
// the enriched logger.
func NewRequestIDMiddleware(baseLogger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(requestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Header(requestIDHeader, requestID)

		reqLogger := baseLogger.With(slog.String("request_id", requestID))

		if spanCtx := trace.SpanContextFromContext(c.Request.Context()); spanCtx.IsValid() {
			reqLogger = reqLogger.With(
				slog.String("trace_id", spanCtx.TraceID().String()),
				slog.String("span_id", spanCtx.SpanID().String()),
			)
		}

		ctx := logger.WithContext(c.Request.Context(), reqLogger)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
