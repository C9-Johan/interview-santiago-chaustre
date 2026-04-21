package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ConversionRecorder adapts the internal Counters to the Metrics interface
// expected by the trackconversion use case, so the application layer never
// imports the OTel SDK directly.
type ConversionRecorder struct {
	counters *Counters
}

// NewConversionRecorder binds to the Provider's counters. When counters is
// nil the recorder becomes a no-op so wiring is always safe.
func NewConversionRecorder(c *Counters) *ConversionRecorder {
	return &ConversionRecorder{counters: c}
}

// RecordManaged ticks the inquiryiq.conversations.managed counter with the
// platform and primary_code attributes Grafana needs.
func (r *ConversionRecorder) RecordManaged(ctx context.Context, platform, primaryCode string) {
	if r == nil || r.counters == nil || r.counters.Managed == nil {
		return
	}
	r.counters.Managed.Add(ctx, 1, metric.WithAttributes(
		attribute.String("platform", platform),
		attribute.String("primary_code", primaryCode),
	))
}

// RecordConverted ticks the inquiryiq.conversations.converted counter.
func (r *ConversionRecorder) RecordConverted(ctx context.Context, platform, primaryCode string) {
	if r == nil || r.counters == nil || r.counters.Converted == nil {
		return
	}
	r.counters.Converted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("platform", platform),
		attribute.String("primary_code", primaryCode),
	))
}
