package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	otellogbridge "go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// Providers holds the three SDK providers plus the slog.Handler bridge
// that internal/platform/logger uses to build the process logger. main.go
// holds this only long enough to wire logger.New(...) and defer Shutdown.
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *metric.MeterProvider
	Logger *sdklog.LoggerProvider

	// LogBridge is an slog.Handler that forwards records to Logger.
	// Don't pass this field directly to logger.New — call SlogHandler()
	// instead, which correctly collapses a nil *Handler to a nil
	// interface. Nil if telemetry is disabled.
	LogBridge *otellogbridge.Handler
}

// SlogHandler returns LogBridge as an slog.Handler, or a true nil
// interface (not a non-nil interface wrapping a nil *Handler) when
// telemetry is disabled. Callers must use this instead of passing
// p.LogBridge directly to logger.New — a nil *otelslog.Handler boxed
// into the slog.Handler interface is non-nil from the interface's
// perspective, which would make logger.New wrongly think OTel is
// enabled and panic on first use.
func (p *Providers) SlogHandler() slog.Handler {
	if p == nil || p.LogBridge == nil {
		return nil
	}
	return p.LogBridge
}

// Setup builds and registers all three OTel providers against a single
// collector endpoint and resource. If cfg.CollectorURL is empty,
// telemetry is disabled entirely (Providers fields are nil, LogBridge is
// nil) — this is the expected/normal state for local development without
// a collector running, and callers must treat it as a valid outcome, not
// an error.
//
// isLocal controls TLS (insecure for a local collector) and trace
// sampling (100% locally vs traceSampleRatio elsewhere) — pass
// cfg.Env == "development".
func Setup(
	ctx context.Context, cfg config.OTelConfig,
	serviceName, serviceVersion string, isLocal bool,
) (*Providers, func(context.Context) error, error) {
	if cfg.CollectorURL == "" {
		return &Providers{}, func(context.Context) error { return nil }, nil
	}

	res, err := newResource(ctx, serviceName, serviceVersion)
	if err != nil {
		return nil, nil, err
	}

	tracerProvider, err := newTracerProvider(ctx, isLocal, cfg.CollectorURL, res)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up tracing: %w", err)
	}

	// Every outbound HTTP call (Supabase admin API, storage) gets a client
	// span for free — neither internal/modules/account/service.go nor
	// internal/platform/storage sets a custom Transport, so both fall back
	// to this package-level default at request time.
	http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)

	meterProvider, err := newMeterProvider(ctx, isLocal, cfg.CollectorURL, res)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up metrics: %w", err)
	}

	loggerProvider, err := newLoggerProvider(ctx, isLocal, cfg.CollectorURL, res)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up logging: %w", err)
	}

	logBridge := otellogbridge.NewHandler(serviceName, otellogbridge.WithLoggerProvider(loggerProvider))

	shutdown := func(shutdownCtx context.Context) error {
		return errors.Join(
			tracerProvider.Shutdown(shutdownCtx),
			meterProvider.Shutdown(shutdownCtx),
			loggerProvider.Shutdown(shutdownCtx),
		)
	}

	return &Providers{
		Tracer:    tracerProvider,
		Meter:     meterProvider,
		Logger:    loggerProvider,
		LogBridge: logBridge,
	}, shutdown, nil
}
