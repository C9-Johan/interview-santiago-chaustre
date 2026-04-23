package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// payloadStore is a generic upsert-by-postID Mongo store that serializes the
// domain value as JSON bytes, so BSON tag drift never reshapes the stored
// payload. Shared by Classifications and Replies.
type payloadStore[T any] struct {
	col   *mongo.Collection
	label string // "classification" / "reply" — used in error messages
}

type payloadDoc struct {
	PostID    string    `bson:"post_id"`
	Payload   []byte    `bson:"payload"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// newPayloadStore ensures a unique index on post_id and returns a generic
// store wrapping col. indexName is reported in wrapped errors.
func newPayloadStore[T any](ctx context.Context, col *mongo.Collection, indexName, label string) (*payloadStore[T], error) {
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "post_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index %s: %w", indexName, err)
	}
	return &payloadStore[T]{col: col, label: label}, nil
}

// Put upserts the latest payload for postID.
func (s *payloadStore[T]) Put(ctx context.Context, postID string, v T) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", s.label, err)
	}
	_, err = s.col.UpdateOne(ctx,
		bson.M{"post_id": postID},
		bson.M{"$set": bson.M{"post_id": postID, "payload": payload, "updated_at": time.Now().UTC()}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo upsert %s: %w", s.label, err)
	}
	return nil
}

// Get returns the decoded payload for postID, or ErrNotFound.
func (s *payloadStore[T]) Get(ctx context.Context, postID string) (T, error) {
	var zero T
	var doc payloadDoc
	err := s.col.FindOne(ctx, bson.M{"post_id": postID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return zero, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	if err != nil {
		return zero, fmt.Errorf("mongo find %s: %w", s.label, err)
	}
	var out T
	if err := json.Unmarshal(doc.Payload, &out); err != nil {
		return zero, fmt.Errorf("unmarshal %s: %w", s.label, err)
	}
	return out, nil
}
