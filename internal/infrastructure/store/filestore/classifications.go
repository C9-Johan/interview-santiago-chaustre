package filestore

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Classifications persists each completed classification as a JSONL record
// keyed on postID. It is a thin adapter over jsonlStore[domain.Classification]
// so the implementation stays in one place.
type Classifications struct {
	s *jsonlStore[domain.Classification]
}

// NewClassifications ensures dir exists and opens data/classifications.jsonl
// for append.
func NewClassifications(dir string) (*Classifications, error) {
	s, err := newJSONLStore[domain.Classification](dir, "classifications.jsonl", "classification")
	if err != nil {
		return nil, err
	}
	return &Classifications{s: s}, nil
}

// Close flushes and closes the underlying file.
func (c *Classifications) Close() error { return c.s.Close() }

// Put serializes one classification record as a single JSONL line and fsyncs.
func (c *Classifications) Put(ctx context.Context, postID string, cls domain.Classification) error {
	return c.s.Put(ctx, postID, cls)
}

// Get scans the file for the newest record with matching postID.
func (c *Classifications) Get(ctx context.Context, postID string) (domain.Classification, error) {
	return c.s.Get(ctx, postID)
}
