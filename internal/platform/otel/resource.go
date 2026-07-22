// Package otel wires up the OpenTelemetry SDK for traces, metrics, and
// logs, all exported via OTLP/gRPC to a single collector endpoint. This is
// intentionally one package, not split per-signal, because all three
// providers share one Resource and one lifecycle (Setup returns a single
// Shutdown func that flushes and closes all three).
//
// Every module gets telemetry for free through this package — no module
// imports the OTel SDK directly. A module that wants a custom span or
// counter calls otel.Tracer(name) / otel.Meter(name) (the global API,
// which is already wired to our providers by Setup), not anything in this
// package directly.
package otel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// newResource builds the Resource attached to every span, metric, and log
// record this service emits. WithProcess/WithHost/WithOS/WithContainer
// pull in runtime facts (pid, hostname, OS, container ID) automatically;
// only service.name and service.version need to come from us.
func newResource(ctx context.Context, serviceName, serviceVersion string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
		resource.WithContainer(),
		resource.WithOS(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("building otel resource: %w", err)
	}
	return res, nil
}

// dialTimeout bounds how long Setup waits for the initial exporter
// connections before giving up. Exporters reconnect in the background
// afterward, so this only affects startup, not steady-state delivery.
const dialTimeout = 5 * time.Second
