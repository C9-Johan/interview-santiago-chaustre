package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// ConversationMemory is a JSONL-backed ConversationMemoryStore. On startup the
// file is scanned and folded into an in-memory map (latest-wins per
// ConversationKey) so reads are O(1). Writes append a full snapshot line and
// fsync so restarts hydrate the latest state.
//
// Safe for concurrent use.
type ConversationMemory struct {
	mu      sync.Mutex
	path    string
	writer  *os.File
	records map[domain.ConversationKey]domain.ConversationMemoryRecord
	byGuest map[string][]domain.ConversationKey
}

// NewConversationMemory opens <dir>/conversation_memory.jsonl for append and
// hydrates the in-memory map from its contents. Returns an error only when
// directory creation or file open fails; malformed lines are skipped so a
// partially-written log does not block startup.
func NewConversationMemory(dir string) (*ConversationMemory, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "conversation_memory.jsonl")
	records, byGuest, err := hydrateMemoryLog(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &ConversationMemory{
		path:    path,
		writer:  f,
		records: records,
		byGuest: byGuest,
	}, nil
}

// Close flushes and closes the underlying file.
func (m *ConversationMemory) Close() error {
	if err := m.writer.Close(); err != nil {
		return fmt.Errorf("close conversation_memory: %w", err)
	}
	return nil
}

// Reset truncates the JSONL log and clears in-memory indexes. The demo Reset
// endpoint calls this so a follow-up turn cannot inherit stale guest_profile
// or thread context from the previous run.
func (m *ConversationMemory) Reset(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := truncateAndReopen(m.writer, m.path)
	if err != nil {
		return fmt.Errorf("conversation_memory reset: %w", err)
	}
	m.writer = f
	m.records = make(map[domain.ConversationKey]domain.ConversationMemoryRecord, 64)
	m.byGuest = make(map[string][]domain.ConversationKey, 64)
	return nil
}

// Get returns the record for k, or a zero record when absent.
func (m *ConversationMemory) Get(_ context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[k], nil
}

// Update applies mut to the record under k, persists the resulting snapshot
// as one JSONL line, and re-indexes by guest. mut runs under the store lock
// and must not block.
func (m *ConversationMemory) Update(_ context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[k]
	mut(&r)
	m.records[k] = r
	m.indexByGuest(r.GuestID, k)
	return m.appendSnapshot(r)
}

// ListByGuest returns up to limit records for guestID, newest UpdatedAt first.
func (m *ConversationMemory) ListByGuest(_ context.Context, guestID string, limit int) ([]domain.ConversationMemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := m.byGuest[guestID]
	out := make([]domain.ConversationMemoryRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.records[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ repository.ConversationMemoryStore = (*ConversationMemory)(nil)

func (m *ConversationMemory) indexByGuest(guestID string, k domain.ConversationKey) {
	if guestID == "" {
		return
	}
	for _, existing := range m.byGuest[guestID] {
		if existing == k {
			return
		}
	}
	m.byGuest[guestID] = append(m.byGuest[guestID], k)
}

func (m *ConversationMemory) appendSnapshot(r domain.ConversationMemoryRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal conversation_memory: %w", err)
	}
	if _, err := m.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write conversation_memory: %w", err)
	}
	if err := m.writer.Sync(); err != nil {
		return fmt.Errorf("sync conversation_memory: %w", err)
	}
	return nil
}

// hydrateMemoryLog scans path and folds every JSONL line into the in-memory
// map. Later lines overwrite earlier ones for the same ConversationKey, so a
// full history of snapshots converges on the latest state.
func hydrateMemoryLog(path string) (map[domain.ConversationKey]domain.ConversationMemoryRecord, map[string][]domain.ConversationKey, error) {
	records := make(map[domain.ConversationKey]domain.ConversationMemoryRecord, 64)
	byGuest := make(map[string][]domain.ConversationKey, 64)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return records, byGuest, nil
		}
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec domain.ConversationMemoryRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		records[rec.ConversationKey] = rec
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan %s: %w", path, err)
	}
	for k, r := range records {
		if r.GuestID == "" {
			continue
		}
		byGuest[r.GuestID] = append(byGuest[r.GuestID], k)
	}
	return records, byGuest, nil
}
