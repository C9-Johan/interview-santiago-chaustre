package mongostore

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Classifications persists the most recent classification per postID as a
// thin adapter over payloadStore[domain.Classification].
type Classifications struct {
	s *payloadStore[domain.Classification]
}

// NewClassifications returns a Classifications store with a unique index on
// post_id so upserts are atomic.
func NewClassifications(ctx context.Context, c *Client) (*Classifications, error) {
	s, err := newPayloadStore[domain.Classification](ctx, c.db.Collection(c.cols.Classifications), "classifications", "classification")
	if err != nil {
		return nil, err
	}
	return &Classifications{s: s}, nil
}

// Put upserts the latest classification for postID.
func (c *Classifications) Put(ctx context.Context, postID string, cls domain.Classification) error {
	return c.s.Put(ctx, postID, cls)
}

// Get returns the classification for postID, or ErrNotFound.
func (c *Classifications) Get(ctx context.Context, postID string) (domain.Classification, error) {
	return c.s.Get(ctx, postID)
}
