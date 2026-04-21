package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ConfidenceRecorder adapts the telemetry Histograms to the narrow interface
// processinquiry expects, so the application layer never imports the OTel SDK.
type ConfidenceRecorder struct {
	histograms *Histograms
}

// NewConfidenceRecorder binds to the Provider's histograms. A nil histograms
// bundle makes the recorder a safe no-op so wiring never panics.
func NewConfidenceRecorder(h *Histograms) *ConfidenceRecorder {
	return &ConfidenceRecorder{histograms: h}
}

// RecordClassifier buckets the classifier's self-rated confidence tagged with
// the resolved primary_code so operators can chart calibration per taxonomy.
func (r *ConfidenceRecorder) RecordClassifier(ctx context.Context, primaryCode string, confidence float64) {
	if r == nil || r.histograms == nil || r.histograms.ClassifierConfidence == nil {
		return
	}
	r.histograms.ClassifierConfidence.Record(ctx, confidence, metric.WithAttributes(
		attribute.String("primary_code", primaryCode),
	))
}

// RecordGenerator buckets the generator's self-rated reply confidence.
func (r *ConfidenceRecorder) RecordGenerator(ctx context.Context, primaryCode string, confidence float64) {
	if r == nil || r.histograms == nil || r.histograms.GeneratorConfidence == nil {
		return
	}
	r.histograms.GeneratorConfidence.Record(ctx, confidence, metric.WithAttributes(
		attribute.String("primary_code", primaryCode),
	))
}
