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

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Classifications persists the most recent classification per postID.
// The struct is opaque to BSON — we serialize the domain value as JSON bytes
// so BSON tag drift never reshapes the stored payload.
type Classifications struct {
	col *mongo.Collection
}

// NewClassifications returns a Classifications store with a unique index on
// post_id so upserts are atomic.
func NewClassifications(ctx context.Context, c *Client) (*Classifications, error) {
	col := c.db.Collection(c.cols.Classifications)
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "post_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index classifications: %w", err)
	}
	return &Classifications{col: col}, nil
}

type classificationDoc struct {
	PostID    string    `bson:"post_id"`
	Payload   []byte    `bson:"payload"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// Put upserts the latest classification for postID.
func (c *Classifications) Put(ctx context.Context, postID string, cls domain.Classification) error {
	payload, err := json.Marshal(cls)
	if err != nil {
		return fmt.Errorf("marshal classification: %w", err)
	}
	_, err = c.col.UpdateOne(ctx,
		bson.M{"post_id": postID},
		bson.M{"$set": bson.M{"post_id": postID, "payload": payload, "updated_at": time.Now().UTC()}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo upsert classification: %w", err)
	}
	return nil
}

// Get returns the classification for postID, or ErrNotFound.
func (c *Classifications) Get(ctx context.Context, postID string) (domain.Classification, error) {
	var doc classificationDoc
	err := c.col.FindOne(ctx, bson.M{"post_id": postID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Classification{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	if err != nil {
		return domain.Classification{}, fmt.Errorf("mongo find classification: %w", err)
	}
	var out domain.Classification
	if err := json.Unmarshal(doc.Payload, &out); err != nil {
		return domain.Classification{}, fmt.Errorf("unmarshal classification: %w", err)
	}
	return out, nil
}
