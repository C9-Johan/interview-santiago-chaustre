// Package dispatch is the bounded-worker backpressure layer between the
// debouncer and the orchestrator. Debouncer flushes enqueue a job into a
// fixed-capacity channel; a pool of workers drains the channel and invokes
// the inner FlushFn. When the queue is full the debouncer flush returns
// immediately so the service never blocks in-process work behind LLM latency,
// and the upstream caller records an escalation so the turn is never silently
// dropped. Graceful Stop drains every pending job with a budget, then
// cancels.
package dispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Handler is the narrow contract the pool consumes — typically a flush
// closure that fetches conversation context and calls orch.Run. Workers call
// this synchronously; its error handling is its own responsibility.
type Handler func(ctx context.Context, t domain.Turn)

// Config tunes pool sizing and observability hooks. Workers and QueueSize
// default to reasonable values if zero; Log and metrics fields are optional.
type Config struct {
	Workers   int
	QueueSize int
	Log       *slog.Logger

	// Accepted ticks once per successfully-enqueued job. Attributes carry
	// platform when available for per-channel capacity planning.
	Accepted metric.Int64Counter
	// Dropped ticks once per backpressure drop. Same attribute shape as
	// Accepted so a single Grafana query can compute (dropped / accepted).
	Dropped metric.Int64Counter
	// QueueDepth is an UpDownCounter reflecting the current number of jobs
	// waiting in the channel. Useful for spotting saturation before drops.
	QueueDepth metric.Int64UpDownCounter
	// QueueLatency records the time a job waited before a worker picked it
	// up — directly observable backpressure.
	QueueLatency metric.Float64Histogram
}

// Pool is the worker/queue bundle. Start and Stop are not re-entrant; use
// once per process.
type Pool struct {
	cfg     Config
	handler Handler
	queue   chan job
	wg      sync.WaitGroup

	metricsCtx context.Context
	done       chan struct{}
	closeOnce  sync.Once
}

type job struct {
	ctx        context.Context
	turn       domain.Turn
	enqueuedAt time.Time
}

// New constructs a Pool. Defaults: 8 workers, queue size 64. handler must be
// non-nil. Pool must be Start()ed before Enqueue.
func New(cfg Config, handler Handler) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 64
	}
	return &Pool{
		cfg:        cfg,
		handler:    handler,
		queue:      make(chan job, cfg.QueueSize),
		metricsCtx: context.Background(),
		done:       make(chan struct{}),
	}
}

// Start spawns cfg.Workers goroutines that drain the queue until Stop is
// called. Safe to call once.
func (p *Pool) Start() {
	for range p.cfg.Workers {
		p.wg.Add(1)
		go p.worker()
	}
}

// Enqueue attempts a non-blocking send on the queue. Returns true when the
// job was accepted, false when the queue is full OR the pool is stopping.
// Callers must record an escalation in the false case so the turn is not
// silently dropped.
func (p *Pool) Enqueue(ctx context.Context, t domain.Turn, platform string) bool {
	select {
	case <-p.done:
		p.tick(p.cfg.Dropped, 1, platform)
		return false
	default:
	}
	j := job{ctx: ctx, turn: t, enqueuedAt: time.Now()}
	select {
	case p.queue <- j:
		p.tick(p.cfg.Accepted, 1, platform)
		p.depth(1)
		return true
	default:
		p.tick(p.cfg.Dropped, 1, platform)
		if p.cfg.Log != nil {
			p.cfg.Log.WarnContext(ctx, "dispatch_backpressure_drop",
				slog.String("post_id", t.LastPostID),
				slog.String("conversation_key", string(t.Key)),
				slog.Int("queue_capacity", p.cfg.QueueSize),
			)
		}
		return false
	}
}

// Stop signals the pool to drain. New Enqueue calls return false; workers
// finish any pending queued jobs and exit. Returns when every worker has
// exited or ctx is canceled, whichever comes first.
func (p *Pool) Stop(ctx context.Context) {
	p.closeOnce.Do(func() { close(p.done) })
	finished := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
	}
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.done:
			p.drainRemaining()
			return
		case j := <-p.queue:
			p.run(j)
		}
	}
}

// drainRemaining runs whatever is already queued when Stop fires. All workers
// race on the shared queue; the one that observes the empty default branch
// exits, leaving any concurrent sender (pre-close) to still complete normally
// because the queue channel is never closed.
func (p *Pool) drainRemaining() {
	for {
		select {
		case j := <-p.queue:
			p.run(j)
		default:
			return
		}
	}
}

func (p *Pool) run(j job) {
	p.depth(-1)
	p.recordLatency(time.Since(j.enqueuedAt))
	p.handler(j.ctx, j.turn)
}

func (p *Pool) depth(delta int64) {
	if p.cfg.QueueDepth != nil {
		p.cfg.QueueDepth.Add(p.metricsCtx, delta)
	}
}

func (p *Pool) tick(c metric.Int64Counter, n int64, platform string) {
	if c == nil {
		return
	}
	c.Add(p.metricsCtx, n, metric.WithAttributes(attribute.String("platform", platform)))
}

func (p *Pool) recordLatency(d time.Duration) {
	if p.cfg.QueueLatency == nil {
		return
	}
	p.cfg.QueueLatency.Record(p.metricsCtx, d.Seconds())
}
