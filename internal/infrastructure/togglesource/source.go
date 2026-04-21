// Package togglesource is the runtime-mutable source of truth for
// domain.Toggles. The orchestrator reads Current() on every turn so an
// operator can flip auto-response off mid-incident without redeploying; the
// admin HTTP handler mutates via SetAutoResponse. Every flip is logged and
// counted so the audit trail is not dependent on a correlated request log.
package togglesource

import (
	"context"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// eventPublisher is the consumer-side contract Source uses to emit flip
// events for downstream subscribers (Slack, PagerDuty). Nil is safe.
type eventPublisher interface {
	Publish(ctx context.Context, topic string, payload any)
}

// Source is a concurrency-safe, in-memory holder for domain.Toggles. A nil
// logger disables audit logging; a nil counter disables metrics. Both are
// optional so tests can instantiate without telemetry wiring.
type Source struct {
	mu      sync.RWMutex
	t       domain.Toggles
	log     *slog.Logger
	counter metric.Int64Counter
	events  eventPublisher
}

// New constructs a Source seeded with initial. log, counter, and events may
// all be nil.
func New(initial domain.Toggles, log *slog.Logger, counter metric.Int64Counter) *Source {
	return &Source{t: initial, log: log, counter: counter}
}

// WithEvents returns a Source that also publishes flip events on the given
// bus. Chaining keeps the original New signature backward-compatible.
func (s *Source) WithEvents(events eventPublisher) *Source {
	s.events = events
	return s
}

// Current returns a snapshot of the current toggles. Safe for concurrent
// callers; the returned value is a copy so callers can mutate freely.
func (s *Source) Current() domain.Toggles {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.t
}

// SetAutoResponse flips the AutoResponseEnabled flag and returns the previous
// and new values so the caller can include both in its audit response.
// A no-op flip (same value as current) is still logged and counted because
// operators sometimes retry a toggle-off during an incident and the audit
// trail must show the intent, not just the state change.
func (s *Source) SetAutoResponse(ctx context.Context, enabled bool, actor string) (prev, now bool) {
	s.mu.Lock()
	prev = s.t.AutoResponseEnabled
	s.t.AutoResponseEnabled = enabled
	now = s.t.AutoResponseEnabled
	s.mu.Unlock()
	s.audit(ctx, "auto_response", prev, now, actor)
	return prev, now
}

func (s *Source) audit(ctx context.Context, field string, prev, now bool, actor string) {
	if s.log != nil {
		s.log.InfoContext(ctx, "toggle_flip",
			slog.String("field", field),
			slog.Bool("prev", prev),
			slog.Bool("now", now),
			slog.String("actor", actor),
		)
	}
	if s.counter != nil {
		s.counter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("field", field),
				attribute.Bool("enabled", now),
			),
		)
	}
	if s.events != nil {
		s.events.Publish(ctx, "toggle.flipped", map[string]any{
			"field": field,
			"prev":  prev,
			"now":   now,
			"actor": actor,
		})
	}
}
