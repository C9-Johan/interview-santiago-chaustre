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

// Replies persists the latest reply per postID. Mirrors Classifications: the
// domain payload is stored as opaque JSON bytes so BSON tag drift never
// reshapes the stored value.
type Replies struct {
	col *mongo.Collection
}

// NewReplies returns a Replies store with a unique index on post_id so
// upserts are atomic.
func NewReplies(ctx context.Context, c *Client) (*Replies, error) {
	col := c.db.Collection(c.cols.Replies)
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "post_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index replies: %w", err)
	}
	return &Replies{col: col}, nil
}

type replyDoc struct {
	PostID    string    `bson:"post_id"`
	Payload   []byte    `bson:"payload"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// Put upserts the latest reply for postID.
func (r *Replies) Put(ctx context.Context, postID string, reply domain.Reply) error {
	payload, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}
	_, err = r.col.UpdateOne(ctx,
		bson.M{"post_id": postID},
		bson.M{"$set": bson.M{"post_id": postID, "payload": payload, "updated_at": time.Now().UTC()}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo upsert reply: %w", err)
	}
	return nil
}

// Get returns the reply for postID, or ErrNotFound.
func (r *Replies) Get(ctx context.Context, postID string) (domain.Reply, error) {
	var doc replyDoc
	err := r.col.FindOne(ctx, bson.M{"post_id": postID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Reply{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	if err != nil {
		return domain.Reply{}, fmt.Errorf("mongo find reply: %w", err)
	}
	var out domain.Reply
	if err := json.Unmarshal(doc.Payload, &out); err != nil {
		return domain.Reply{}, fmt.Errorf("unmarshal reply: %w", err)
	}
	return out, nil
}
