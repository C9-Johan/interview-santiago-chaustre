// Package budget tracks daily LLM spend and trips the auto-response
// kill-switch when the configured cap is exceeded. It wraps the same
// TokenRecorder contract the llm.Client already consumes, so the
// accountant slots in front of the existing counter without any changes
// in the client path — every Chat call reports spend to the inner
// recorder first, then the Watcher converts tokens to USD and compares
// against the cap.
//
// Bucketing is per UTC day; the tripped flag resets on rollover so the
// budget self-heals at midnight without operator intervention.
package budget

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// tokenRecorder mirrors the consumer-side contract llm.Client uses. The
// Watcher satisfies it so it can be passed directly to llm.WithTokenRecorder
// while chaining to an inner recorder that preserves the raw telemetry.
type tokenRecorder interface {
	Record(ctx context.Context, model, stage, outcome string, prompt, completion, total int)
}

// toggleFlipper is the narrow slice of togglesource.Source the watcher
// needs to flip auto-response when the budget is exhausted.
type toggleFlipper interface {
	SetAutoResponse(ctx context.Context, enabled bool, actor string) (prev, now bool)
}

// eventPublisher is the narrow slice of eventbus.Bus the watcher uses to
// emit budget.exceeded events for downstream subscribers (Slack, PagerDuty).
type eventPublisher interface {
	Publish(ctx context.Context, topic string, payload any)
}

// ModelPrice is the per-1k-token USD rate for a single model. Prompt and
// completion are priced separately because most providers bill them
// asymmetrically (DeepSeek, OpenAI, Anthropic).
type ModelPrice struct {
	PromptPer1K     float64
	CompletionPer1K float64
}

// Config wires the Watcher's policy. CapUSD <= 0 disables the kill-switch
// (accounting still runs so dashboards remain populated); UnknownModel is
// applied when a call reports a model absent from Prices, so a surprise
// model still contributes to the daily tally instead of silently burning.
type Config struct {
	CapUSD       float64
	Prices       map[string]ModelPrice
	UnknownModel ModelPrice
	Actor        string
}

// Watcher tracks daily LLM spend and flips the auto-response kill-switch
// when the cap is breached. Safe for concurrent use; the mutex guards the
// day/spend/tripped state because Record is called from every LLM goroutine.
type Watcher struct {
	mu      sync.Mutex
	cfg     Config
	inner   tokenRecorder
	toggles toggleFlipper
	events  eventPublisher
	log     *slog.Logger
	flips   metric.Int64Counter
	now     func() time.Time

	day     string
	spent   float64
	tripped bool
}

// New constructs a Watcher. Any of inner, toggles, events, log, or flips may
// be nil — the Watcher no-ops on the nil collaborators so tests and
// dev-mode deployments can wire partial dependencies.
func New(
	cfg Config,
	inner tokenRecorder,
	toggles toggleFlipper,
	events eventPublisher,
	log *slog.Logger,
	flips metric.Int64Counter,
) *Watcher {
	return &Watcher{
		cfg: cfg, inner: inner, toggles: toggles, events: events,
		log: log, flips: flips, now: time.Now,
	}
}

// Record satisfies the tokenRecorder contract. Delegates to the inner
// recorder first so the raw token counter is never skipped, then converts
// the call to USD and rolls it into the daily bucket. Non-ok outcomes are
// skipped because failed calls typically return zero tokens.
func (w *Watcher) Record(ctx context.Context, model, stage, outcome string, prompt, completion, total int) {
	if w == nil {
		return
	}
	if w.inner != nil {
		w.inner.Record(ctx, model, stage, outcome, prompt, completion, total)
	}
	if outcome != "ok" {
		return
	}
	cost := w.cost(model, prompt, completion)
	if cost <= 0 {
		return
	}
	w.tally(ctx, model, cost)
}

func (w *Watcher) cost(model string, prompt, completion int) float64 {
	p, ok := w.cfg.Prices[model]
	if !ok {
		p = w.cfg.UnknownModel
	}
	return float64(prompt)/1000*p.PromptPer1K + float64(completion)/1000*p.CompletionPer1K
}

func (w *Watcher) tally(ctx context.Context, model string, cost float64) {
	w.mu.Lock()
	day := w.now().UTC().Format("2006-01-02")
	if day != w.day {
		w.day = day
		w.spent = 0
		w.tripped = false
	}
	w.spent += cost
	shouldTrip := w.cfg.CapUSD > 0 && !w.tripped && w.spent >= w.cfg.CapUSD
	if !shouldTrip {
		w.mu.Unlock()
		return
	}
	w.tripped = true
	spent, limit := w.spent, w.cfg.CapUSD
	currentDay := w.day
	w.mu.Unlock()
	w.trip(ctx, model, currentDay, spent, limit)
}

func (w *Watcher) trip(ctx context.Context, model, day string, spent, limit float64) {
	actor := w.cfg.Actor
	if actor == "" {
		actor = "budget_watcher"
	}
	if w.toggles != nil {
		w.toggles.SetAutoResponse(ctx, false, actor)
	}
	if w.log != nil {
		w.log.WarnContext(ctx, "budget_exceeded",
			slog.String("day", day),
			slog.Float64("spent_usd", spent),
			slog.Float64("cap_usd", limit),
			slog.String("model", model),
			slog.String("actor", actor),
		)
	}
	if w.flips != nil {
		w.flips.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("reason", "budget_exceeded"),
				attribute.String("model", model),
			),
		)
	}
	if w.events != nil {
		w.events.Publish(ctx, "budget.exceeded", map[string]any{
			"day":       day,
			"spent_usd": spent,
			"cap_usd":   limit,
			"model":     model,
			"actor":     actor,
		})
	}
}

// Status is the snapshot the admin handler surfaces for operators.
type Status struct {
	Day      string  `json:"day"`
	SpentUSD float64 `json:"spent_usd"`
	CapUSD   float64 `json:"cap_usd"`
	Tripped  bool    `json:"tripped"`
}

// Status returns the current day's spend and whether the budget kill-switch
// has tripped this bucket. Cheap to call — takes the mutex briefly and
// copies four scalars.
func (w *Watcher) Status() Status {
	if w == nil {
		return Status{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.day
	if day == "" {
		day = w.now().UTC().Format("2006-01-02")
	}
	return Status{Day: day, SpentUSD: w.spent, CapUSD: w.cfg.CapUSD, Tripped: w.tripped}
}

// WithClock overrides the time source so tests can step through day
// rollovers deterministically. Returns the receiver for chaining.
func (w *Watcher) WithClock(now func() time.Time) *Watcher {
	if now != nil {
		w.now = now
	}
	return w
}
