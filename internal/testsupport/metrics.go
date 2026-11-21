package testsupport

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ManualMetricReader routes every instrument created after this call to a
// reader the test collects from by hand, and restores the previous provider
// afterwards. It swaps a process-wide global, so a test using it cannot run in
// parallel with another that records metrics.
func ManualMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader, name string) (metricdata.Aggregation, bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m.Data, true
			}
		}
	}
	return nil, false
}

func hasAttributes(set attribute.Set, match []attribute.KeyValue) bool {
	for _, want := range match {
		if got, ok := set.Value(want.Key); !ok || got != want.Value {
			return false
		}
	}
	return true
}

// CounterTotal sums the named counter's data points that carry every attribute
// in match. A counter that never recorded totals zero.
func CounterTotal(t *testing.T, reader *sdkmetric.ManualReader, name string, match ...attribute.KeyValue) int64 {
	t.Helper()
	data, found := collect(t, reader, name)
	if !found {
		return 0
	}
	sum, ok := data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s is %T, not an int64 counter", name, data)
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if hasAttributes(dp.Attributes, match) {
			total += dp.Value
		}
	}
	return total
}

// GaugeValue returns the named gauge's value for the data point carrying every
// attribute in match, and whether such a data point exists.
func GaugeValue(t *testing.T, reader *sdkmetric.ManualReader, name string, match ...attribute.KeyValue) (int64, bool) {
	t.Helper()
	data, found := collect(t, reader, name)
	if !found {
		return 0, false
	}
	gauge, ok := data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s is %T, not an int64 gauge", name, data)
	}
	for _, dp := range gauge.DataPoints {
		if hasAttributes(dp.Attributes, match) {
			return dp.Value, true
		}
	}
	return 0, false
}
