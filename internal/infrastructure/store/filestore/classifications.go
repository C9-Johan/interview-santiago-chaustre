package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Classifications persists each completed classification as a JSONL record
// keyed on postID. All writes are serialized via mu so concurrent appends
// are atomic; reads open a fresh file handle.
type Classifications struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewClassifications ensures dir exists and opens data/classifications.jsonl
// for append.
func NewClassifications(dir string) (*Classifications, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "classifications.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Classifications{path: p, writer: f}, nil
}

// Close flushes and closes the underlying file.
func (c *Classifications) Close() error { return c.writer.Close() }

type classificationLine struct {
	PostID         string                `json:"post_id"`
	Classification domain.Classification `json:"classification"`
}

// Put serializes one classification record as a single JSONL line and fsyncs.
func (c *Classifications) Put(_ context.Context, postID string, cls domain.Classification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(classificationLine{PostID: postID, Classification: cls})
	if err != nil {
		return fmt.Errorf("marshal classification: %w", err)
	}
	if _, err := c.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write classification: %w", err)
	}
	return c.writer.Sync()
}

// Get scans the file for the newest record with matching postID. It returns
// ErrNotFound (wrapped) if no line has that postID.
func (c *Classifications) Get(_ context.Context, postID string) (domain.Classification, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return domain.Classification{}, fmt.Errorf("open %s: %w", c.path, err)
	}
	defer func() { _ = f.Close() }() // scan-only; close error is not actionable here.
	var match domain.Classification
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line classificationLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.PostID == postID {
			match = line.Classification
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return domain.Classification{}, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return domain.Classification{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}
