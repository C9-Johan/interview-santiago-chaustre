// Package filestore provides JSONL-backed implementations of the durable
// repository contracts. v1 choice — swap to Mongo/Postgres later via the
// same interfaces.
package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Webhooks is a JSONL append-only WebhookStore. All writes are serialized
// via mu so concurrent appends are atomic. Reads open a fresh file handle.
type Webhooks struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewWebhooks ensures parent dir exists and opens the file in append mode.
func NewWebhooks(dir string) (*Webhooks, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "webhooks.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Webhooks{path: p, writer: f}, nil
}

// Close flushes and closes the writer.
func (w *Webhooks) Close() error { return w.writer.Close() }

// Append serializes rec as one JSON line and fsyncs.
func (w *Webhooks) Append(_ context.Context, rec repository.WebhookRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}
	if _, err := w.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write webhook: %w", err)
	}
	return w.writer.Sync()
}

// ErrNotFound is returned by Get when no record has the given postID.
var ErrNotFound = errors.New("record not found")

// Get scans the file for the newest record with matching postID.
func (w *Webhooks) Get(_ context.Context, postID string) (repository.WebhookRecord, error) {
	f, err := os.Open(w.path)
	if err != nil {
		return repository.WebhookRecord{}, fmt.Errorf("open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }() // scan-only; close error is not actionable here.
	var match repository.WebhookRecord
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec repository.WebhookRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.PostID == postID {
			match = rec
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return repository.WebhookRecord{}, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return repository.WebhookRecord{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}

// Since returns all records whose ReceivedAt is within d of now.
func (w *Webhooks) Since(_ context.Context, d time.Duration) ([]repository.WebhookRecord, error) {
	cutoff := time.Now().Add(-d)
	f, err := os.Open(w.path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }()
	var out []repository.WebhookRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec repository.WebhookRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.ReceivedAt.After(cutoff) {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}
