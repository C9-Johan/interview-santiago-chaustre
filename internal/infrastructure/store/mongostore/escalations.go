package mongostore

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Escalations is the durable EscalationStore backed by a mongo collection.
// The domain payload is stored as JSON bytes alongside queryable columns so
// operator dashboards can filter without reading the blob.
type Escalations struct {
	col *mongo.Collection
}

// NewEscalations returns a store bound to the escalations collection with an
// index on created_at desc for fast List.
func NewEscalations(ctx context.Context, c *Client) (*Escalations, error) {
	col := c.db.Collection(c.cols.Escalations)
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "created_at", Value: -1}},
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index escalations: %w", err)
	}
	return &Escalations{col: col}, nil
}

type escalationDoc struct {
	ID              string `bson:"id"`
	TraceID         string `bson:"trace_id"`
	PostID          string `bson:"post_id"`
	ConversationKey string `bson:"conversation_key"`
	GuestID         string `bson:"guest_id"`
	Platform        string `bson:"platform"`
	Reason          string `bson:"reason"`
	CreatedAt       any    `bson:"created_at"`
	Payload         []byte `bson:"payload"`
}

// Record inserts one escalation document.
func (e *Escalations) Record(ctx context.Context, es domain.Escalation) error {
	payload, err := json.Marshal(es)
	if err != nil {
		return fmt.Errorf("marshal escalation: %w", err)
	}
	doc := escalationDoc{
		ID:              es.ID,
		TraceID:         es.TraceID,
		PostID:          es.PostID,
		ConversationKey: string(es.ConversationKey),
		GuestID:         es.GuestID,
		Platform:        es.Platform,
		Reason:          es.Reason,
		CreatedAt:       es.CreatedAt,
		Payload:         payload,
	}
	if _, err := e.col.InsertOne(ctx, doc); err != nil {
		return fmt.Errorf("mongo insert escalation: %w", err)
	}
	return nil
}

// List returns the newest limit escalations by created_at.
func (e *Escalations) List(ctx context.Context, limit int) ([]domain.Escalation, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if limit > 0 {
		opts = opts.SetLimit(int64(limit))
	}
	cur, err := e.col.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo find escalations: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []escalationDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo decode escalations: %w", err)
	}
	out := make([]domain.Escalation, 0, len(docs))
	for i := range docs {
		var es domain.Escalation
		if err := json.Unmarshal(docs[i].Payload, &es); err != nil {
			return nil, fmt.Errorf("unmarshal escalation: %w", err)
		}
		out = append(out, es)
	}
	return out, nil
}
