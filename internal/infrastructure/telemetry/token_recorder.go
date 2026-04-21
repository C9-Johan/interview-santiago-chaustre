package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// TokenRecorder is the small surface llm.Client consumes to report token
// spend per stage (classifier/generator/critic). Keeping it a thin wrapper
// around the Counters keeps the client's dependency on telemetry narrow and
// easy to fake in tests.
type TokenRecorder struct {
	tokens metric.Int64Counter
	calls  metric.Int64Counter
}

// NewTokenRecorder constructs a recorder backed by c. A nil Counters
// argument yields a recorder whose methods no-op, safe to wire in tests.
func NewTokenRecorder(c *Counters) *TokenRecorder {
	if c == nil {
		return &TokenRecorder{}
	}
	return &TokenRecorder{tokens: c.LLMTokens, calls: c.LLMCalls}
}

// Record bumps the call counter and the three token counters (prompt,
// completion, total) with {model, stage, outcome, kind} labels. outcome is
// "ok" for successful calls; callers pass "error" when the call failed so
// spend + failures remain distinguishable in Grafana.
func (r *TokenRecorder) Record(ctx context.Context, model, stage, outcome string, prompt, completion, total int) {
	if r == nil {
		return
	}
	baseAttrs := []attribute.KeyValue{
		attribute.String("model", model),
		attribute.String("stage", stage),
		attribute.String("outcome", outcome),
	}
	if r.calls != nil {
		r.calls.Add(ctx, 1, metric.WithAttributes(baseAttrs...))
	}
	if r.tokens == nil {
		return
	}
	add := func(kind string, n int) {
		if n <= 0 {
			return
		}
		attrs := append([]attribute.KeyValue(nil), baseAttrs...)
		attrs = append(attrs, attribute.String("kind", kind))
		r.tokens.Add(ctx, int64(n), metric.WithAttributes(attrs...))
	}
	add("prompt", prompt)
	add("completion", completion)
	add("total", total)
}
