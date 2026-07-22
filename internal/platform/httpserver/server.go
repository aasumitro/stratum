// Package httpserver builds the Gin engine and wraps it in a stdlib
// *http.Server for graceful shutdown. Per Gin's own documentation, no
// third-party graceful-shutdown library is needed since Go 1.8 —
// http.Server.Shutdown() is sufficient and is what's used here.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
)

// New builds a *gin.Engine with the baseline middleware every route in
// the app needs, in the order that makes each subsequent middleware's
// assumptions valid:
//
//  1. otelgin — creates the root span; everything after this can read
//     it from c.Request.Context().
//  2. RequestID — generates/echoes X-Request-ID and binds a logger
//     enriched with request_id + trace_id/span_id (reads the span
//     otelgin just created).
//  3. Recovery — recovers panics using the logger RequestID just bound,
//     so even a panic gets a correlated log line.
//
// Auth, organization resolution, and rate-limiting are NOT included here —
// they're route-group-specific (some routes are public, some need
// different rate limits) and are added by each module's Register(...)
// call instead. serviceName is passed through to otelgin for span
// attribution.
func New(serviceName string, baseLogger *slog.Logger, ginMode string) *gin.Engine {
	gin.SetMode(ginMode) // "debug" | "release" | "test" — pass cfg.Env-derived value from main.go

	engine := gin.New()
	engine.Use(
		otelgin.Middleware(serviceName),
		middleware.NewRequestIDMiddleware(baseLogger),
		middleware.NewRecoveryMiddleware(),
	)

	return engine
}

// Server wraps engine in a stdlib *http.Server configured with sane
// timeouts, ready for Run/Shutdown.
type Server struct {
	httpServer *http.Server
}

// NewServer builds a Server listening on port, serving engine.
//
// WriteTimeout is deliberately left unset: it would apply to the entire
// response write, including GET /me/notifications/stream's long-lived SSE
// connection, cutting it off mid-stream. ReadTimeout/IdleTimeout don't
// have that problem — SSE requests have no body to read, and an actively
// streaming connection is never idle — so they're safe to set globally.
func NewServer(engine *gin.Engine, port string) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              ":" + port,
			Handler:           engine,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			IdleTimeout:       idleTimeout,
		},
	}
}

// Run starts serving and blocks until the server stops (either from an
// error or a call to Shutdown elsewhere causing ListenAndServe to
// return http.ErrServerClosed, which is the expected, non-error exit path).
func (s *Server) Run() error {
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server error: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server: stops accepting new connections
// and waits up to shutdownTimeout for in-flight requests to finish
// before forcibly closing remaining connections.
func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	return nil
}
