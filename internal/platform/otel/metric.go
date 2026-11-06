package otel

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	apimetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	meterExportInterval = 60 * time.Second
	meterName           = "github.com/aasumitro/stratum"
)

// Meter returns the meter every Stratum instrument comes from. Instruments
// are no-ops until a provider is registered, so telemetry stays optional.
func Meter() apimetric.Meter {
	return otel.Meter(meterName)
}

// Counter returns the named counter from Meter. An instrument the SDK refuses
// degrades to a no-op: a telemetry problem must never reach a request path.
func Counter(name, description string) apimetric.Int64Counter {
	counter, err := Meter().Int64Counter(name, apimetric.WithDescription(description))
	if err != nil {
		slog.Warn("otel: creating counter failed", "name", name, "error", err)
		return noop.Int64Counter{}
	}
	return counter
}

// newMeterProvider builds a MeterProvider exporting to collectorURL over
// OTLP/gRPC on a periodic interval. There's no sampling concept for
// metrics — every recorded measurement counts toward the aggregate — so
// unlike traces, isLocal here only affects TLS, not export volume.
func newMeterProvider(
	ctx context.Context, isLocal bool,
	collectorURL string, res *resource.Resource,
) (*metric.MeterProvider, error) {
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
