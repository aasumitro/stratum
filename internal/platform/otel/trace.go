package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// traceSampleRatio applies in non-local environments: 25% of traces
	// are kept. Local/dev always samples 100% so a developer reproducing
	// a bug never has to wonder whether their trace simply wasn't sampled.
	traceSampleRatio  = 0.25
	traceMaxQueueSize = 1024
	traceMaxBatchSize = 256
)

// newTracerProvider builds a TracerProvider exporting to collectorURL over
// OTLP/gRPC. ParentBased+TraceIDRatioBased sampling means any trace whose
// parent was already sampled stays sampled (so a single user request
// traced end-to-end doesn't get cut off partway through by independent
// per-span sampling decisions).
func newTracerProvider(
	ctx context.Context,
	isLocal bool,
	collectorURL string,
	res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(collectorURL),
		otlptracegrpc.WithTimeout(dialTimeout),
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(traceSampleRatio))
	if isLocal {
		options = append(options, otlptracegrpc.WithInsecure())
		sampler = sdktrace.AlwaysSample()
	}

	exporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient(options...))
	if err != nil {
		return nil, fmt.Errorf("creating otlp trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxQueueSize(traceMaxQueueSize),
			sdktrace.WithMaxExportBatchSize(traceMaxBatchSize)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	return provider, nil
}
