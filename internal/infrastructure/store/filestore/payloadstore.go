package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// jsonlStore is a generic append-only JSONL store keyed by postID. Writes are
// serialized via mu; reads open a fresh handle and keep the newest record for
// the requested postID. Shared by Classifications and Replies so the two
// stores differ only in their file name and the serialized payload type.
type jsonlStore[T any] struct {
	mu     sync.Mutex
	path   string
	writer *os.File
	label  string // "classification" / "reply" — used in error messages
}

type jsonlLine[T any] struct {
	PostID  string `json:"post_id"`
	Payload T      `json:"payload"`
}

// newJSONLStore ensures dir exists and opens dir/filename for append. label is
// the human-readable noun surfaced in wrapped errors.
func newJSONLStore[T any](dir, filename, label string) (*jsonlStore[T], error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, filename)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &jsonlStore[T]{path: p, writer: f, label: label}, nil
}

// Close flushes and closes the underlying file.
func (s *jsonlStore[T]) Close() error { return s.writer.Close() }

// Reset truncates the underlying JSONL file and reopens it for append. Used
// by the demo Reset endpoint so a presenter can wipe state between runs
// without bouncing the process.
func (s *jsonlStore[T]) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := truncateAndReopen(s.writer, s.path)
	if err != nil {
		return fmt.Errorf("%s reset: %w", s.label, err)
	}
	s.writer = f
	return nil
}

// Put serializes one record as a single JSONL line and fsyncs.
func (s *jsonlStore[T]) Put(_ context.Context, postID string, v T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(jsonlLine[T]{PostID: postID, Payload: v})
	if err != nil {
		return fmt.Errorf("marshal %s: %w", s.label, err)
	}
	if _, err := s.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", s.label, err)
	}
	return s.writer.Sync()
}

// Get scans the file for the newest record matching postID. Returns
// ErrNotFound (wrapped) if no line has that postID.
func (s *jsonlStore[T]) Get(_ context.Context, postID string) (T, error) {
	var zero T
	f, err := os.Open(s.path)
	if err != nil {
		return zero, fmt.Errorf("open %s: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()
	var match T
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line jsonlLine[T]
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.PostID == postID {
			match = line.Payload
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return zero, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return zero, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}
