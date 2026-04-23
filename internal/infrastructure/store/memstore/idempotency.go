// Package memstore is the in-memory, interface-satisfying default for
// storage interfaces. Swap to Redis/Mongo/SQLite later by implementing
// the same repository.* contracts.
package memstore

import (
	"context"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Idempotency tracks (ConversationKey, postID) tuples in two states:
// inflight (claimed but not complete) and complete. Both count as "already"
// for subsequent SeenOrClaim calls — the orchestrator is expected to drop.
type Idempotency struct {
	mu      sync.Mutex
	claimed map[string]bool // key = k + "|" + postID; value true = complete
}

// NewIdempotency returns an empty Idempotency store.
func NewIdempotency() *Idempotency {
	return &Idempotency{claimed: make(map[string]bool, 1024)}
}

// SeenOrClaim reports whether (k, postID) has been seen before. When false,
// a new inflight claim is recorded. Safe for concurrent callers.
func (i *Idempotency) SeenOrClaim(_ context.Context, k domain.ConversationKey, postID string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	key := string(k) + "|" + postID
	if _, ok := i.claimed[key]; ok {
		return true, nil
	}
	i.claimed[key] = false
	return false, nil
}

// Complete flips an inflight claim to complete. Idempotent — calling it twice
// is not an error; calling it for a never-claimed key also succeeds (defensive).
func (i *Idempotency) Complete(_ context.Context, k domain.ConversationKey, postID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claimed[string(k)+"|"+postID] = true
	return nil
}

// Reset drops every recorded claim so the demo Reset endpoint can replay a
// previously-seen postID without the orchestrator dedupe-dropping it.
func (i *Idempotency) Reset(_ context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claimed = make(map[string]bool, 1024)
	return nil
}
