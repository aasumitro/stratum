package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otellog "go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newLoggerProvider builds a LoggerProvider exporting to collectorURL over
// OTLP/gRPC. This is registered as the global log provider via
// otellog/global so that internal/platform/logger's otelslog-bridged
// *slog.Logger (built afterward in Setup) picks it up automatically —
// every slog.Info/Error call anywhere in the app then also becomes an
// OTel log record correlated with the active trace/span ID.
func newLoggerProvider(ctx context.Context, isLocal bool, collectorURL string, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	options := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(collectorURL),
		otlploggrpc.WithTimeout(dialTimeout),
	}
	if isLocal {
		options = append(options, otlploggrpc.WithInsecure())
	}

	exporter, err := otlploggrpc.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("creating otlp log exporter: %w", err)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)

	otellog.SetLoggerProvider(provider)

	return provider, nil
}
