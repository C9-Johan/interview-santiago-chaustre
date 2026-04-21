// Package langsmith integrates github.com/langchain-ai/langsmith-go with the
// project's telemetry pipeline. Setup registers LangSmith's OTLP span
// processor on an existing tracer provider, so every span that already flows
// to our Alloy/Tempo target is additionally forwarded to LangSmith. The
// traceopenai middleware injects LangSmith-specific GenAI attributes on
// OpenAI-compatible HTTP calls.
package langsmith

import (
	"context"
	"fmt"
	"net/http"

	langsmith "github.com/langchain-ai/langsmith-go"
	"github.com/langchain-ai/langsmith-go/instrumentation/traceopenai"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Config selects the LangSmith destination. Leave APIKey empty to disable —
// Setup returns a no-op provider in that case and never contacts LangSmith.
type Config struct {
	APIKey      string
	ProjectName string
	ServiceName string
	Endpoint    string
}

// Provider owns the LangSmith tracer attached to the project's TracerProvider.
// A disabled Provider is safe to call Shutdown on.
type Provider struct {
	tp       trace.TracerProvider
	shutdown func(context.Context) error
}

// Tracer returns the underlying tracer provider LangSmith registered against.
// Callers pass this into traceopenai.WithTracerProvider so OpenAI-bound spans
// use the correct provider.
func (p *Provider) Tracer() trace.TracerProvider {
	return p.tp
}

// Shutdown flushes pending LangSmith exports; a no-op when disabled.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

// Enabled reports whether the LangSmith exporter is actually configured.
// Useful when a caller wants to branch between the traced and untraced paths
// (e.g. skip wrapping the OpenAI HTTP client when LangSmith is off).
func (p *Provider) Enabled() bool {
	return p.shutdown != nil
}

// Setup attaches a LangSmith span processor to sdkTP. When cfg.APIKey is empty
// or sdkTP is nil (the project's telemetry is disabled), Setup returns a
// no-op Provider so call sites stay oblivious.
func Setup(sdkTP *sdktrace.TracerProvider, cfg Config) (*Provider, error) {
	if cfg.APIKey == "" || sdkTP == nil {
		var fallback trace.TracerProvider
		if sdkTP != nil {
			fallback = sdkTP
		}
		return &Provider{tp: fallback}, nil
	}
	opts := []langsmith.OTelTracerOption{
		langsmith.WithAPIKey(cfg.APIKey),
		langsmith.WithProjectName(cfg.ProjectName),
	}
	if cfg.ServiceName != "" {
		opts = append(opts, langsmith.WithServiceName(cfg.ServiceName))
	}
	if cfg.Endpoint != "" {
		opts = append(opts, langsmith.WithEndpoint(cfg.Endpoint))
	}
	ls, err := langsmith.NewOTel(sdkTP, opts...)
	if err != nil {
		return nil, fmt.Errorf("langsmith attach: %w", err)
	}
	return &Provider{tp: sdkTP, shutdown: ls.Shutdown}, nil
}

// WrapOpenAIHTTPClient returns base wrapped with the LangSmith traceopenai
// middleware. When the Provider is disabled the wrap is skipped and base is
// returned unchanged — avoids the tiny per-request cost when nothing is
// listening.
func (p *Provider) WrapOpenAIHTTPClient(base *http.Client) *http.Client {
	if !p.Enabled() {
		return base
	}
	return traceopenai.WrapClient(base, traceopenai.WithTracerProvider(p.tp))
}
