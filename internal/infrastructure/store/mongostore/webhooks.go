package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Webhooks persists raw Guesty webhooks to a mongo collection indexed on
// post_id (latest wins) and received_at (time-range queries).
type Webhooks struct {
	col *mongo.Collection
}

// NewWebhooks returns a Webhooks store bound to the shared mongo database.
// It ensures the post_id and received_at indexes exist.
func NewWebhooks(ctx context.Context, c *Client) (*Webhooks, error) {
	col := c.db.Collection(c.cols.Webhooks)
	if err := ensureWebhookIndexes(ctx, col); err != nil {
		return nil, err
	}
	return &Webhooks{col: col}, nil
}

type webhookDoc struct {
	SvixID     string            `bson:"svix_id"`
	Headers    map[string]string `bson:"headers"`
	RawBody    []byte            `bson:"raw_body"`
	ReceivedAt time.Time         `bson:"received_at"`
	PostID     string            `bson:"post_id"`
	ConvRawID  string            `bson:"conv_raw_id"`
	TraceID    string            `bson:"trace_id"`
}

func webhookFromRecord(r repository.WebhookRecord) webhookDoc {
	return webhookDoc{
		SvixID:     r.SvixID,
		Headers:    r.Headers,
		RawBody:    r.RawBody,
		ReceivedAt: r.ReceivedAt,
		PostID:     r.PostID,
		ConvRawID:  r.ConvRawID,
		TraceID:    r.TraceID,
	}
}

func recordFromWebhook(d webhookDoc) repository.WebhookRecord {
	return repository.WebhookRecord{
		SvixID:     d.SvixID,
		Headers:    d.Headers,
		RawBody:    d.RawBody,
		ReceivedAt: d.ReceivedAt,
		PostID:     d.PostID,
		ConvRawID:  d.ConvRawID,
		TraceID:    d.TraceID,
	}
}

// Append inserts one webhook document. Duplicates (same post_id at the same
// received_at) are allowed — callers rely on IdempotencyStore for dedup.
func (w *Webhooks) Append(ctx context.Context, rec repository.WebhookRecord) error {
	if _, err := w.col.InsertOne(ctx, webhookFromRecord(rec)); err != nil {
		return fmt.Errorf("mongo insert webhook: %w", err)
	}
	return nil
}

// Get returns the newest document matching postID, or ErrNotFound.
func (w *Webhooks) Get(ctx context.Context, postID string) (repository.WebhookRecord, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "received_at", Value: -1}})
	var doc webhookDoc
	err := w.col.FindOne(ctx, bson.M{"post_id": postID}, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return repository.WebhookRecord{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	if err != nil {
		return repository.WebhookRecord{}, fmt.Errorf("mongo find webhook: %w", err)
	}
	return recordFromWebhook(doc), nil
}

// Since returns every document whose received_at is within d of now.
func (w *Webhooks) Since(ctx context.Context, d time.Duration) ([]repository.WebhookRecord, error) {
	cutoff := time.Now().Add(-d)
	cur, err := w.col.Find(ctx, bson.M{"received_at": bson.M{"$gt": cutoff}})
	if err != nil {
		return nil, fmt.Errorf("mongo find webhooks since: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []webhookDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo decode webhooks: %w", err)
	}
	out := make([]repository.WebhookRecord, 0, len(docs))
	for i := range docs {
		out = append(out, recordFromWebhook(docs[i]))
	}
	return out, nil
}

func ensureWebhookIndexes(ctx context.Context, col *mongo.Collection) error {
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "post_id", Value: 1}}},
		{Keys: bson.D{{Key: "received_at", Value: -1}}},
	})
	if err != nil {
		return fmt.Errorf("mongo index webhooks: %w", err)
	}
	return nil
}
