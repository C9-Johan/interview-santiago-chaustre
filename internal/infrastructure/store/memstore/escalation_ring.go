// Package memstore contains in-memory implementations of the domain stores.
// It is paired with filestore for durable companion writes.
package memstore

import (
	"context"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// durableAppender is the narrow unexported interface EscalationRing uses to
// persist each escalation. Satisfied by filestore.Escalations.
type durableAppender interface {
	Append(ctx context.Context, e domain.Escalation) error
}

// EscalationRing combines an in-RAM ring buffer (latest N) with a durable
// appender (JSONL) so GET /escalations is fast and restarts do not lose data
// beyond what the ring holds. Safe for concurrent use.
type EscalationRing struct {
	mu       sync.Mutex
	capacity int
	buf      []domain.Escalation // oldest -> newest, bounded to capacity
	durable  durableAppender
}

// NewEscalationRing returns a ring of capacity items backed by durable. A
// non-positive capacity is coerced to the default of 500. The durable
// appender is optional; pass nil to use the ring as an ephemeral buffer.
func NewEscalationRing(capacity int, durable durableAppender) *EscalationRing {
	if capacity < 1 {
		capacity = 500
	}
	return &EscalationRing{
		capacity: capacity,
		buf:      make([]domain.Escalation, 0, capacity),
		durable:  durable,
	}
}

// Record appends to the durable writer and inserts into the ring. If the
// durable write fails the ring is left unchanged so durable and in-memory
// state cannot diverge.
func (r *EscalationRing) Record(ctx context.Context, e domain.Escalation) error {
	if r.durable != nil {
		if err := r.durable.Append(ctx, e); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.capacity {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, e)
	return nil
}

// List returns the last limit escalations, newest first. A limit of zero or
// a limit greater than the ring size returns the full ring.
func (r *EscalationRing) List(_ context.Context, limit int) ([]domain.Escalation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.buf) {
		limit = len(r.buf)
	}
	out := make([]domain.Escalation, 0, limit)
	for i := len(r.buf) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.buf[i])
	}
	return out, nil
}
