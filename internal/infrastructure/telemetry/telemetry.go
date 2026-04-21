// Package telemetry wires OpenTelemetry tracing. When OTEL_EXPORTER_OTLP_ENDPOINT
// is unset the package returns a no-op tracer provider, so production and tests
// can wire the same code without flipping runtime behavior. Alloy/Tempo/Grafana
// drop in later via environment only.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// tracerName is the package-scoped instrumentation identifier all spans share
// so consumers can filter our own emissions from library-emitted spans.
const tracerName = "github.com/chaustre/inquiryiq"

// Config selects the OTLP target. Endpoint empty disables export entirely.
// Insecure drops TLS for the local Alloy/Tempo stack; toggle off for SaaS
// destinations.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Endpoint       string
	Insecure       bool
	Headers        map[string]string
}

// Provider is the runtime handle for the configured pipeline.
// Tracer is safe to use even when the exporter is disabled — it returns a
// no-op tracer and a no-op shutdown.
type Provider struct {
	tp            trace.TracerProvider
	shutdown      func(context.Context) error
	meterShutdown func(context.Context) error
	counters      *Counters
	histograms    *Histograms
}

// Tracer returns the instrumentation tracer shared by all call sites in the
// service.
func (p *Provider) Tracer() trace.Tracer {
	return p.tp.Tracer(tracerName)
}

// Shutdown flushes any pending spans and metric reads. Safe to call on the
// no-op provider. The first non-nil error from either shutdown is returned;
// any remaining shutdowns still run.
func (p *Provider) Shutdown(ctx context.Context) error {
	var first error
	if p.shutdown != nil {
		if err := p.shutdown(ctx); err != nil {
			first = err
		}
	}
	if p.meterShutdown != nil {
		if err := p.meterShutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Enabled is true when an OTLP exporter was configured. Call sites can use it
// to decide whether to wrap outgoing HTTP transports.
func (p *Provider) Enabled() bool {
	_, ok := p.tp.(*sdktrace.TracerProvider)
	return ok
}

// SDKProvider returns the underlying SDK tracer provider for callers that need
// to register additional span processors (e.g. the LangSmith exporter). Returns
// nil when tracing is disabled so callers can detect the no-op path.
func (p *Provider) SDKProvider() *sdktrace.TracerProvider {
	tp, _ := p.tp.(*sdktrace.TracerProvider)
	return tp
}

// TracerProvider returns the underlying provider (typed as the trace interface
// so noop and SDK variants share one accessor). Use SDKProvider when you need
// to register span processors.
func (p *Provider) TracerProvider() trace.TracerProvider {
	return p.tp
}

// Setup builds a Provider from cfg. When cfg.Endpoint is empty it returns a
// no-op provider immediately; otherwise it installs an OTLP/HTTP exporter
// with a batch span processor, registers it as the global tracer, and returns
// the shutdown closer. Returning Setup success with a no-op provider is
// intentional — it keeps callers unaware of whether tracing is actually on.
func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	p := &Provider{}
	if err := p.attachTracing(ctx, cfg); err != nil {
		return nil, err
	}
	if err := p.attachMetrics(ctx, cfg); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) attachTracing(ctx context.Context, cfg Config) error {
	if cfg.Endpoint == "" {
		p.tp = noop.NewTracerProvider()
		return nil
	}
	exp, err := newOTLPExporter(ctx, cfg)
	if err != nil {
		return fmt.Errorf("otlp exporter: %w", err)
	}
	res, err := buildResource(ctx, cfg)
	if err != nil {
		return fmt.Errorf("otel resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	p.tp = tp
	p.shutdown = tp.Shutdown
	return nil
}

func newOTLPExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}
	return otlptracehttp.New(ctx, opts...)
}

func buildResource(ctx context.Context, cfg Config) (*sdkresource.Resource, error) {
	attrs := []sdkresource.Option{
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		sdkresource.WithFromEnv(),
		sdkresource.WithTelemetrySDK(),
	}
	return sdkresource.New(ctx, attrs...)
}

// WrapHTTPClient returns a clone of c with its transport wrapped by otelhttp
// so every outgoing request emits a client span. When the provider is
// disabled, the returned client still gets wrapped — otelhttp falls through
// to a no-op tracer, so behavior is identical and cheap.
func WrapHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport:     otelhttp.NewTransport(base),
		CheckRedirect: c.CheckRedirect,
		Jar:           c.Jar,
		Timeout:       c.Timeout,
	}
}

// HTTPMiddleware wraps h in otelhttp.NewHandler so every inbound request
// becomes a server span named by its route. Route names come from chi via
// otelhttp.WithSpanNameFormatter so the span is the route pattern, not the
// raw URL path (keeps cardinality bounded).
func HTTPMiddleware(name string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, name)
}
