// Package debouncer implements repository.Debouncer with a sliding window
// bounded by a hard max-wait cap. One goroutine per buffered conversation is
// avoided — flush is driven by time.AfterFunc and guarded by a single mutex.
package debouncer

import (
	"context"
	"sync"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// FlushFn is the orchestrator entry point invoked when a buffer flushes.
type FlushFn func(ctx context.Context, t domain.Turn)

type convBuffer struct {
	key         domain.ConversationKey
	messages    []domain.Message
	seenPostIDs map[string]struct{}
	timer       *time.Timer
	createdAt   time.Time
}

// Timed is the production Debouncer. Construction injects window, maxWait,
// and Clock (for maxWait calculation) plus the flush callback.
//
// Concurrency: safe for concurrent Push/CancelIfHostReplied/Stop calls; all
// mutable state is guarded by mu. Flush callbacks run on the goroutine that
// fires time.AfterFunc, without mu held.
type Timed struct {
	mu      sync.Mutex
	buffers map[domain.ConversationKey]*convBuffer
	window  time.Duration
	maxWait time.Duration
	clock   repository.Clock
	flush   FlushFn
	stopped bool
}

// NewTimed constructs a Timed debouncer. flush is called on every successful
// flush in the goroutine that fires the timer. window is the sliding quiet
// window re-armed on each Push; maxWait is the hard ceiling measured from the
// first Push of a buffer.
func NewTimed(window, maxWait time.Duration, c repository.Clock, flush FlushFn) *Timed {
	return &Timed{
		buffers: make(map[domain.ConversationKey]*convBuffer, 64),
		window:  window,
		maxWait: maxWait,
		clock:   c,
		flush:   flush,
	}
}

// Push records msg under k and (re)arms the flush timer. Duplicate PostIDs
// within the current buffer are dropped silently. Push is a no-op after Stop.
func (d *Timed) Push(_ context.Context, k domain.ConversationKey, msg domain.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	buf, ok := d.buffers[k]
	if !ok {
		buf = &convBuffer{
			key:         k,
			seenPostIDs: make(map[string]struct{}, 4),
			createdAt:   d.clock.Now(),
		}
		d.buffers[k] = buf
	}
	if _, dup := buf.seenPostIDs[msg.PostID]; dup {
		return
	}
	buf.seenPostIDs[msg.PostID] = struct{}{}
	buf.messages = append(buf.messages, msg)
	if buf.timer != nil {
		buf.timer.Stop()
	}
	wait := d.windowClamped(buf)
	buf.timer = time.AfterFunc(wait, func() { d.fire(k) })
}

func (d *Timed) windowClamped(buf *convBuffer) time.Duration {
	remaining := d.maxWait - d.clock.Since(buf.createdAt)
	wait := d.window
	if remaining < wait {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// CancelIfHostReplied drops an active buffer for k when role is not a guest.
// Guest roles are a no-op so the sliding window keeps accumulating their burst.
func (d *Timed) CancelIfHostReplied(k domain.ConversationKey, role domain.Role) {
	if role == domain.RoleGuest {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf, ok := d.buffers[k]
	if !ok {
		return
	}
	if buf.timer != nil {
		buf.timer.Stop()
	}
	delete(d.buffers, k)
}

// Stop prevents further Push and cancels pending timers. Idempotent.
func (d *Timed) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for k, buf := range d.buffers {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(d.buffers, k)
	}
}

func (d *Timed) fire(k domain.ConversationKey) {
	d.mu.Lock()
	buf, ok := d.buffers[k]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.buffers, k)
	d.mu.Unlock()
	msgs := append([]domain.Message{}, buf.messages...)
	turn := domain.Turn{
		Key:        buf.key,
		Messages:   msgs,
		LastPostID: msgs[len(msgs)-1].PostID,
	}
	d.flush(context.Background(), turn)
}
