package mongostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// ConversationMemory persists per-conversation memory in mongo, indexed by
// conversation_key (unique) and guest_id (for ListByGuest). The in-struct
// mutex serializes Update's read-modify-write so two concurrent updates for
// the same key cannot overwrite each other's mutations.
type ConversationMemory struct {
	mu  sync.Mutex
	col *mongo.Collection
}

// NewConversationMemory ensures the two indexes exist and returns a ready
// store. conversation_key is unique so upserts are atomic.
func NewConversationMemory(ctx context.Context, c *Client) (*ConversationMemory, error) {
	col := c.db.Collection(c.cols.ConversationMemory)
	_, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "conversation_key", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{Keys: bson.D{{Key: "guest_id", Value: 1}}},
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index conversation_memory: %w", err)
	}
	return &ConversationMemory{col: col}, nil
}

type memoryDoc struct {
	ConversationKey string    `bson:"conversation_key"`
	GuestID         string    `bson:"guest_id"`
	UpdatedAt       time.Time `bson:"updated_at"`
	Payload         []byte    `bson:"payload"`
}

// Get returns the record for k, or a zero-valued record when absent.
func (m *ConversationMemory) Get(ctx context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error) {
	return m.loadOrZero(ctx, k)
}

// Update applies mut to the record under k and upserts the result.
// Reads and writes are serialized via the mutex so concurrent mutations
// converge without clobbering each other.
func (m *ConversationMemory) Update(ctx context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.loadOrZero(ctx, k)
	if err != nil {
		return err
	}
	if r.ConversationKey == "" {
		r.ConversationKey = k
	}
	mut(&r)
	r.UpdatedAt = time.Now().UTC()
	return m.upsert(ctx, r)
}

// ListByGuest returns up to limit most-recent records for guestID.
func (m *ConversationMemory) ListByGuest(ctx context.Context, guestID string, limit int) ([]domain.ConversationMemoryRecord, error) {
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}})
	if limit > 0 {
		opts = opts.SetLimit(int64(limit))
	}
	cur, err := m.col.Find(ctx, bson.M{"guest_id": guestID}, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo find conversation_memory: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []memoryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo decode conversation_memory: %w", err)
	}
	out := make([]domain.ConversationMemoryRecord, 0, len(docs))
	for i := range docs {
		var rec domain.ConversationMemoryRecord
		if err := json.Unmarshal(docs[i].Payload, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal conversation_memory: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func (m *ConversationMemory) loadOrZero(ctx context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error) {
	var doc memoryDoc
	err := m.col.FindOne(ctx, bson.M{"conversation_key": string(k)}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.ConversationMemoryRecord{}, nil
	}
	if err != nil {
		return domain.ConversationMemoryRecord{}, fmt.Errorf("mongo find conversation_memory: %w", err)
	}
	var rec domain.ConversationMemoryRecord
	if err := json.Unmarshal(doc.Payload, &rec); err != nil {
		return domain.ConversationMemoryRecord{}, fmt.Errorf("unmarshal conversation_memory: %w", err)
	}
	return rec, nil
}

func (m *ConversationMemory) upsert(ctx context.Context, r domain.ConversationMemoryRecord) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal conversation_memory: %w", err)
	}
	_, err = m.col.UpdateOne(ctx,
		bson.M{"conversation_key": string(r.ConversationKey)},
		bson.M{"$set": bson.M{
			"conversation_key": string(r.ConversationKey),
			"guest_id":         r.GuestID,
			"updated_at":       r.UpdatedAt,
			"payload":          payload,
		}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo upsert conversation_memory: %w", err)
	}
	return nil
}
