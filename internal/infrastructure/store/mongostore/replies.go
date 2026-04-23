package mongostore

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Replies persists the latest reply per postID as a thin adapter over
// payloadStore[domain.Reply].
type Replies struct {
	s *payloadStore[domain.Reply]
}

// NewReplies returns a Replies store with a unique index on post_id so
// upserts are atomic.
func NewReplies(ctx context.Context, c *Client) (*Replies, error) {
	s, err := newPayloadStore[domain.Reply](ctx, c.db.Collection(c.cols.Replies), "replies", "reply")
	if err != nil {
		return nil, err
	}
	return &Replies{s: s}, nil
}

// Put upserts the latest reply for postID.
func (r *Replies) Put(ctx context.Context, postID string, reply domain.Reply) error {
	return r.s.Put(ctx, postID, reply)
}

// Get returns the reply for postID, or ErrNotFound.
func (r *Replies) Get(ctx context.Context, postID string) (domain.Reply, error) {
	return r.s.Get(ctx, postID)
}
