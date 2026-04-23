package memstore

import (
	"context"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// ConversationMemory is an in-RAM ConversationMemoryStore for v1. All state
// lives in the process; restarts lose memory. v2 can slot in a durable impl
// behind the same interface without touching callers.
//
// Safe for concurrent use.
type ConversationMemory struct {
	mu      sync.Mutex
	records map[domain.ConversationKey]domain.ConversationMemoryRecord
	// byGuest indexes records by GuestID to support Layer-4 cross-conversation
	// lookups. Each update re-indexes the changed record so reads see a fresh
	// view.
	byGuest map[string][]domain.ConversationKey
}

// NewConversationMemory returns an empty store.
func NewConversationMemory() *ConversationMemory {
	return &ConversationMemory{
		records: make(map[domain.ConversationKey]domain.ConversationMemoryRecord, 64),
		byGuest: make(map[string][]domain.ConversationKey, 64),
	}
}

// Get returns the record for k, or a zero-valued record when absent.
func (m *ConversationMemory) Get(_ context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[k], nil
}

// Update applies mut to the record under k and persists the result.
// mut is invoked under the store's lock; implementations must not block.
func (m *ConversationMemory) Update(_ context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[k]
	mut(&r)
	m.records[k] = r
	if r.GuestID != "" {
		if !containsKey(m.byGuest[r.GuestID], k) {
			m.byGuest[r.GuestID] = append(m.byGuest[r.GuestID], k)
		}
	}
	return nil
}

// ListByGuest returns up to limit most-recent records for guestID.
// Ordering is by UpdatedAt desc.
func (m *ConversationMemory) ListByGuest(_ context.Context, guestID string, limit int) ([]domain.ConversationMemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := m.byGuest[guestID]
	out := make([]domain.ConversationMemoryRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.records[k])
	}
	sortMemoryByRecent(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Reset drops every record and guest index. Used by the demo Reset endpoint.
func (m *ConversationMemory) Reset(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = make(map[domain.ConversationKey]domain.ConversationMemoryRecord, 64)
	m.byGuest = make(map[string][]domain.ConversationKey, 64)
	return nil
}

var _ repository.ConversationMemoryStore = (*ConversationMemory)(nil)

func containsKey(keys []domain.ConversationKey, k domain.ConversationKey) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

func sortMemoryByRecent(rs []domain.ConversationMemoryRecord) {
	// small N (<=GUEST_MEMORY_LIMIT, default 5); insertion sort keeps the
	// file dependency-free.
	for i := 1; i < len(rs); i++ {
		j := i
		for j > 0 && rs[j].UpdatedAt.After(rs[j-1].UpdatedAt) {
			rs[j-1], rs[j] = rs[j], rs[j-1]
			j--
		}
	}
}
