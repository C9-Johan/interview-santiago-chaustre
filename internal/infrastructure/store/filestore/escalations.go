// Package filestore contains append-only file-backed stores used as the
// durable companion to the in-memory stores in memstore.
package filestore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Escalations is the JSONL-durable half of the escalation store. Callers pair
// it with memstore.EscalationRing for fast List; this type only owns writes.
// Safe for concurrent use.
type Escalations struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewEscalations opens <dir>/escalations.jsonl for append. The directory is
// created with 0o755 if absent. Callers should Close the returned value on
// shutdown.
func NewEscalations(dir string) (*Escalations, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "escalations.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open escalations: %w", err)
	}
	return &Escalations{path: path, writer: f}, nil
}

// Close closes the underlying file.
func (e *Escalations) Close() error {
	if err := e.writer.Close(); err != nil {
		return fmt.Errorf("close escalations: %w", err)
	}
	return nil
}

// Reset truncates the durable escalations log. Wired into the demo Reset
// endpoint via the EscalationRing wrapper.
func (e *Escalations) Reset(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, err := truncateAndReopen(e.writer, e.path)
	if err != nil {
		return fmt.Errorf("escalations reset: %w", err)
	}
	e.writer = f
	return nil
}

// Append serializes one escalation as a single JSONL line and fsyncs it so
// the record survives process crashes.
func (e *Escalations) Append(_ context.Context, es domain.Escalation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := json.Marshal(es)
	if err != nil {
		return fmt.Errorf("marshal escalation: %w", err)
	}
	if _, err := e.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write escalation: %w", err)
	}
	if err := e.writer.Sync(); err != nil {
		return fmt.Errorf("sync escalation: %w", err)
	}
	return nil
}
