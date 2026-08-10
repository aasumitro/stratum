package middleware

import (
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/aasumitro/stratum/internal/platform/logger"
)

const requestIDHeader = "X-Request-ID"

// maxRequestIDLen bounds a caller-supplied X-Request-ID before it's echoed
// back, logged, or used as a span attribute — an upstream gateway's own IDs
// are always well under this, so it only ever trims an adversarial value.
const maxRequestIDLen = 64

// sanitizeRequestID strips non-printable/control characters and caps length
// on a caller-supplied X-Request-ID. The header is attacker-reachable
// (unauthenticated, arrives before any auth middleware) and flows straight
// into structured logs and the response — an unsanitized value could inject
// control characters into log output or bloat every log line for a request.
// Truncates by rune, not byte, so a multi-byte character straddling the
// limit isn't split into invalid UTF-8.
func sanitizeRequestID(id string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, id)
	if utf8.RuneCountInString(clean) <= maxRequestIDLen {
		return clean
	}
	return string([]rune(clean)[:maxRequestIDLen])
}

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
		requestID := sanitizeRequestID(c.GetHeader(requestIDHeader))
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
