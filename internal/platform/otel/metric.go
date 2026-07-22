package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const meterExportInterval = 60 * time.Second

// newMeterProvider builds a MeterProvider exporting to collectorURL over
// OTLP/gRPC on a periodic interval. There's no sampling concept for
// metrics — every recorded measurement counts toward the aggregate — so
// unlike traces, isLocal here only affects TLS, not export volume.
func newMeterProvider(ctx context.Context, isLocal bool, collectorURL string, res *resource.Resource) (*metric.MeterProvider, error) {
	options := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(collectorURL),
		otlpmetricgrpc.WithTimeout(dialTimeout),
	}
	if isLocal {
		options = append(options, otlpmetricgrpc.WithInsecure())
	}

	exporter, err := otlpmetricgrpc.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("creating otlp metric exporter: %w", err)
	}

	provider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter,
			metric.WithInterval(meterExportInterval))),
	)

	otel.SetMeterProvider(provider)

	return provider, nil
}
