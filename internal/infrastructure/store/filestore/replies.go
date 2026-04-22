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

// Replies persists each successful reply (classifier verdict + generator body
// + tool calls) as a JSONL record keyed on postID. Append-only so the tester
// UI can replay tool usage for any turn by post_id.
type Replies struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewReplies ensures dir exists and opens data/replies.jsonl for append.
func NewReplies(dir string) (*Replies, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "replies.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Replies{path: p, writer: f}, nil
}

// Close flushes and closes the underlying file.
func (r *Replies) Close() error { return r.writer.Close() }

type replyLine struct {
	PostID string       `json:"post_id"`
	Reply  domain.Reply `json:"reply"`
}

// Put serializes one reply record as a single JSONL line and fsyncs.
func (r *Replies) Put(_ context.Context, postID string, reply domain.Reply) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.Marshal(replyLine{PostID: postID, Reply: reply})
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}
	if _, err := r.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write reply: %w", err)
	}
	return r.writer.Sync()
}

// Get scans the file for the newest record matching postID. Returns
// ErrNotFound (wrapped) if no line has that postID.
func (r *Replies) Get(_ context.Context, postID string) (domain.Reply, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return domain.Reply{}, fmt.Errorf("open %s: %w", r.path, err)
	}
	defer func() { _ = f.Close() }()
	var match domain.Reply
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line replyLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.PostID == postID {
			match = line.Reply
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return domain.Reply{}, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return domain.Reply{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}
