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
	Managed   metric.Int64Counter
	Converted metric.Int64Counter
}

// Counters returns the service counters wired during Setup.
func (p *Provider) Counters() *Counters { return p.counters }

func (p *Provider) attachMetrics(ctx context.Context, cfg Config) error {
	if cfg.Endpoint == "" {
		mp := metricnoop.NewMeterProvider()
		p.counters = mustCounters(mp.Meter(meterName))
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
	p.counters = mustCounters(mp.Meter(meterName))
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
	return &Counters{Managed: managed, Converted: converted}
}
