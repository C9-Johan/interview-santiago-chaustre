package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/chaustre/inquiryiq/internal/infrastructure/telemetry"
)

// TestConfidenceRecorder confirms that RecordClassifier/RecordGenerator emit
// histogram observations tagged with primary_code attributes. Uses a manual
// reader so the test never talks to an exporter.
func TestConfidenceRecorder(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m := mp.Meter("test")
	cls, err := m.Float64Histogram("inquiryiq.classifier.confidence")
	if err != nil {
		t.Fatalf("classifier histogram: %v", err)
	}
	gen, err := m.Float64Histogram("inquiryiq.generator.confidence")
	if err != nil {
		t.Fatalf("generator histogram: %v", err)
	}
	rec := telemetry.NewConfidenceRecorder(&telemetry.Histograms{
		ClassifierConfidence: cls,
		GeneratorConfidence:  gen,
	})

	ctx := context.Background()
	rec.RecordClassifier(ctx, "R1", 0.42)
	rec.RecordGenerator(ctx, "Y6", 0.88)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	samples := collectHistogramSamples(t, rm)
	if got, want := samples["inquiryiq.classifier.confidence"]["R1"], 0.42; got != want {
		t.Fatalf("classifier sum for R1 = %v, want %v", got, want)
	}
	if got, want := samples["inquiryiq.generator.confidence"]["Y6"], 0.88; got != want {
		t.Fatalf("generator sum for Y6 = %v, want %v", got, want)
	}
}

// TestConfidenceRecorderNilSafe verifies wiring with nil Histograms never
// panics — required so cmd/server stays simple when metrics are disabled.
func TestConfidenceRecorderNilSafe(t *testing.T) {
	t.Parallel()
	rec := telemetry.NewConfidenceRecorder(nil)
	rec.RecordClassifier(context.Background(), "Y1", 0.9)
	rec.RecordGenerator(context.Background(), "Y1", 0.9)
	var zero *telemetry.ConfidenceRecorder
	zero.RecordClassifier(context.Background(), "Y1", 0.9)
	zero.RecordGenerator(context.Background(), "Y1", 0.9)
}

// collectHistogramSamples flattens ResourceMetrics into {metric_name: {primary_code: sum}}
// so tests can assert on the observed values without caring about buckets.
func collectHistogramSamples(t *testing.T, rm metricdata.ResourceMetrics) map[string]map[string]float64 {
	t.Helper()
	out := map[string]map[string]float64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			bucket := out[m.Name]
			if bucket == nil {
				bucket = map[string]float64{}
				out[m.Name] = bucket
			}
			for _, dp := range hist.DataPoints {
				code, _ := dp.Attributes.Value(attribute.Key("primary_code"))
				bucket[code.AsString()] = dp.Sum
			}
		}
	}
	return out
}
