package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// meterName is the instrumentation name under which all service-defined
// metrics are registered. Stable across releases so Grafana queries survive.
const meterName = "github.com/chaustre/inquiryiq"

// Counters holds the service's conversion metrics. Both counters are safe to
// call on the noop meter — when metrics are disabled the Add calls no-op.
type Counters struct {
	Managed          metric.Int64Counter
	Converted        metric.Int64Counter
	ToggleFlips      metric.Int64Counter
	DispatchAccepted metric.Int64Counter
	DispatchDropped  metric.Int64Counter
}

// Histograms holds the service's LLM confidence distributions and dispatch
// queue latency. Buckets are chosen around the gate thresholds (0.65
// classifier, 0.70 generator) so operators can see calibration drift at a
// glance; dispatch buckets cover the expected fast-path (tens of ms) up to
// a worker-saturated backlog (tens of seconds).
type Histograms struct {
	ClassifierConfidence metric.Float64Histogram
	GeneratorConfidence  metric.Float64Histogram
	DispatchQueueLatency metric.Float64Histogram
}

// UpDowns holds the service's UpDownCounters. Split from Counters so Grafana
// queries against a monotonic counter do not accidentally surface gauges.
type UpDowns struct {
	DispatchQueueDepth metric.Int64UpDownCounter
}

// UpDowns returns the gauges wired during Setup.
func (p *Provider) UpDowns() *UpDowns { return p.updowns }

// Counters returns the service counters wired during Setup.
func (p *Provider) Counters() *Counters { return p.counters }

// Histograms returns the service confidence histograms wired during Setup.
func (p *Provider) Histograms() *Histograms { return p.histograms }

func (p *Provider) attachMetrics(ctx context.Context, cfg Config) error {
	if cfg.Endpoint == "" {
		m := metricnoop.NewMeterProvider().Meter(meterName)
		p.counters = mustCounters(m)
		p.histograms = mustHistograms(m)
		p.updowns = mustUpDowns(m)
		return nil
	}
	exp, err := otlpmetrichttp.New(ctx, metricExporterOptions(cfg)...)
	if err != nil {
		return fmt.Errorf("otlp metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(mp)
	p.meterShutdown = mp.Shutdown
	m := mp.Meter(meterName)
	p.counters = mustCounters(m)
	p.histograms = mustHistograms(m)
	p.updowns = mustUpDowns(m)
	return nil
}

func metricExporterOptions(cfg Config) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
	}
	return opts
}

func mustCounters(m metric.Meter) *Counters {
	managed, _ := m.Int64Counter("inquiryiq.conversations.managed",
		metric.WithDescription("Bot-managed guest conversations that produced an auto-reply"),
	)
	converted, _ := m.Int64Counter("inquiryiq.conversations.converted",
		metric.WithDescription("Bot-managed conversations that ended in a confirmed reservation"),
	)
	flips, _ := m.Int64Counter("inquiryiq.admin.toggle_flips",
		metric.WithDescription("Operator-driven runtime toggle flips via /admin/*"),
	)
	accepted, _ := m.Int64Counter("inquiryiq.dispatch.accepted",
		metric.WithDescription("Turns enqueued into the orchestrator worker pool"),
	)
	dropped, _ := m.Int64Counter("inquiryiq.dispatch.dropped",
		metric.WithDescription("Turns dropped by the worker pool due to queue saturation"),
	)
	return &Counters{
		Managed: managed, Converted: converted, ToggleFlips: flips,
		DispatchAccepted: accepted, DispatchDropped: dropped,
	}
}

// confidenceBuckets brackets 0..1 around the auto-send gate thresholds so
// operators can see the tail below 0.65/0.70 where escalations pile up.
var confidenceBuckets = []float64{0.0, 0.25, 0.5, 0.6, 0.65, 0.7, 0.75, 0.8, 0.9, 0.95, 1.0}

// dispatchLatencyBuckets expect ms→tens-of-seconds when the queue is saturated.
var dispatchLatencyBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30}

func mustHistograms(m metric.Meter) *Histograms {
	cls, _ := m.Float64Histogram("inquiryiq.classifier.confidence",
		metric.WithDescription("Classifier self-rated confidence per turn"),
		metric.WithExplicitBucketBoundaries(confidenceBuckets...),
	)
	gen, _ := m.Float64Histogram("inquiryiq.generator.confidence",
		metric.WithDescription("Generator self-rated confidence per reply"),
		metric.WithExplicitBucketBoundaries(confidenceBuckets...),
	)
	dispatch, _ := m.Float64Histogram("inquiryiq.dispatch.queue_latency",
		metric.WithDescription("Seconds a turn waited between enqueue and worker pickup"),
		metric.WithExplicitBucketBoundaries(dispatchLatencyBuckets...),
	)
	return &Histograms{
		ClassifierConfidence: cls,
		GeneratorConfidence:  gen,
		DispatchQueueLatency: dispatch,
	}
}

func mustUpDowns(m metric.Meter) *UpDowns {
	depth, _ := m.Int64UpDownCounter("inquiryiq.dispatch.queue_depth",
		metric.WithDescription("Turns currently waiting in the worker-pool queue"),
	)
	return &UpDowns{DispatchQueueDepth: depth}
}
