package filestore

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Replies persists each successful reply (classifier verdict + generator body
// + tool calls) as a JSONL record keyed on postID. It is a thin adapter over
// jsonlStore[domain.Reply] so the tester UI can replay tool usage for any
// turn by post_id.
type Replies struct {
	s *jsonlStore[domain.Reply]
}

// NewReplies ensures dir exists and opens data/replies.jsonl for append.
func NewReplies(dir string) (*Replies, error) {
	s, err := newJSONLStore[domain.Reply](dir, "replies.jsonl", "reply")
	if err != nil {
		return nil, err
	}
	return &Replies{s: s}, nil
}

// Close flushes and closes the underlying file.
func (r *Replies) Close() error { return r.s.Close() }

// Put serializes one reply record as a single JSONL line and fsyncs.
func (r *Replies) Put(ctx context.Context, postID string, reply domain.Reply) error {
	return r.s.Put(ctx, postID, reply)
}

// Get scans the file for the newest record matching postID.
func (r *Replies) Get(ctx context.Context, postID string) (domain.Reply, error) {
	return r.s.Get(ctx, postID)
}
