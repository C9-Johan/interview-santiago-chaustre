// Package llm wraps sashabaranov/go-openai with a configurable BaseURL so the
// same code runs against OpenAI, DeepSeek (default in v1), or any other
// OpenAI-compatible endpoint. The concrete type Client satisfies
// repository.LLMClient.
package llm

import (
	"context"
	"net/http"

	openai "github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// tracerName identifies spans this package emits so LangSmith, Tempo, and
// Grafana can group them as "LLM calls" separately from transport spans.
const tracerName = "github.com/chaustre/inquiryiq/llm"

func tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Compile-time assertion that *Client satisfies the exported LLMClient
// contract; a signature drift on either side becomes a build error.
var _ repository.LLMClient = (*Client)(nil)

// tokenRecorder is the narrow consumer-side contract Client uses to report
// per-stage spend. Satisfied by *telemetry.TokenRecorder; nil is safe.
type tokenRecorder interface {
	Record(ctx context.Context, model, stage, outcome string, prompt, completion, total int)
}

// Client is the production LLMClient.
type Client struct {
	c      *openai.Client
	tokens tokenRecorder
}

// buildConfig aggregates every knob NewClient accepts so Option can mutate
// both SDK-level (HTTP client) and client-level (token recorder) concerns
// through a single functional type. Unexported — the only public surface is
// Option + the With* constructors.
type buildConfig struct {
	sdk    openai.ClientConfig
	tokens tokenRecorder
}

// Option mutates the client's build-time configuration. Use WithHTTPClient to
// inject a pre-wrapped transport (otelhttp, LangSmith traceopenai) and
// WithTokenRecorder to surface per-stage token spend.
type Option func(*buildConfig)

// WithHTTPClient installs hc as the SDK's underlying HTTP client. Passing nil
// is a no-op.
func WithHTTPClient(hc *http.Client) Option {
	return func(cfg *buildConfig) {
		if hc != nil {
			cfg.sdk.HTTPClient = hc
		}
	}
}

// WithTokenRecorder installs a TokenRecorder so every Chat call reports
// prompt/completion/total tokens to the service's counter. Nil is safe — the
// client no-ops when the recorder is absent.
func WithTokenRecorder(r tokenRecorder) Option {
	return func(cfg *buildConfig) {
		cfg.tokens = r
	}
}

// NewClient constructs a Client against the given BaseURL and API key.
// BaseURL examples: "https://api.deepseek.com/v1", "https://api.openai.com/v1".
func NewClient(baseURL, apiKey string, opts ...Option) *Client {
	cfg := buildConfig{sdk: openai.DefaultConfig(apiKey)}
	cfg.sdk.BaseURL = baseURL
	for _, o := range opts {
		o(&cfg)
	}
	return &Client{c: openai.NewClientWithConfig(cfg.sdk), tokens: cfg.tokens}
}

// Chat forwards the request to the underlying SDK and decorates the surrounding
// span with the semantic attributes LangSmith surfaces in its run viewer
// (model, temperature, token usage, finish reason, tool-call count). Errors
// are recorded as span events so operators see which turn failed and why.
// Latency is measured implicitly via span start/end.
func (c *Client) Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	ctx, span := tracer().Start(ctx, "llm.chat", trace.WithAttributes(
		attribute.String("llm.vendor", "openai-compatible"),
		attribute.String("llm.model", req.Model),
		attribute.Float64("llm.temperature", float64(req.Temperature)),
		attribute.Int("llm.max_tokens", req.MaxCompletionTokens),
		attribute.Int("llm.message_count", len(req.Messages)),
		attribute.Int("llm.tool_count", len(req.Tools)),
		attribute.String("llm.response_format", responseFormatName(req.ResponseFormat)),
	))
	defer span.End()

	stage := string(StageFromContext(ctx))
	resp, err := c.c.CreateChatCompletion(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "chat_completion_failed")
		if c.tokens != nil {
			c.tokens.Record(ctx, req.Model, stage, "error", 0, 0, 0)
		}
		return resp, err
	}
	annotateResponse(span, resp)
	if c.tokens != nil {
		c.tokens.Record(ctx, req.Model, stage, "ok",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
	return resp, nil
}

// annotateResponse mirrors LangSmith's GenAI attribute names so runs line up
// with its native instrumentation on the ingest side.
func annotateResponse(span trace.Span, resp openai.ChatCompletionResponse) {
	span.SetAttributes(
		attribute.Int("llm.prompt_tokens", resp.Usage.PromptTokens),
		attribute.Int("llm.completion_tokens", resp.Usage.CompletionTokens),
		attribute.Int("llm.total_tokens", resp.Usage.TotalTokens),
		attribute.Int("llm.choice_count", len(resp.Choices)),
		attribute.String("llm.id", resp.ID),
	)
	if len(resp.Choices) == 0 {
		span.SetStatus(codes.Error, "no_choices_returned")
		return
	}
	first := resp.Choices[0]
	span.SetAttributes(
		attribute.String("llm.finish_reason", string(first.FinishReason)),
		attribute.Int("llm.tool_calls.count", len(first.Message.ToolCalls)),
	)
	for i := range first.Message.ToolCalls {
		tc := first.Message.ToolCalls[i]
		span.AddEvent("llm.tool_call", trace.WithAttributes(
			attribute.String("tool.name", tc.Function.Name),
			attribute.String("tool.call_id", tc.ID),
			attribute.Int("tool.arguments_bytes", len(tc.Function.Arguments)),
		))
	}
}

func responseFormatName(f *openai.ChatCompletionResponseFormat) string {
	if f == nil {
		return ""
	}
	return string(f.Type)
}
