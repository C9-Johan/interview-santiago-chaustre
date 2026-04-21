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

// Conversions persists bot-managed reservations and their terminal status.
// reservation_id is unique so MarkManaged is idempotent across duplicate
// reservation.new webhooks.
type Conversions struct {
	col *mongo.Collection
}

// NewConversions returns a Conversions store with a unique index on
// reservation_id.
func NewConversions(ctx context.Context, c *Client) (*Conversions, error) {
	col := c.db.Collection(c.cols.Conversions)
	_, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "reservation_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return nil, fmt.Errorf("mongo index conversions: %w", err)
	}
	return &Conversions{col: col}, nil
}

type conversionDoc struct {
	ReservationID   string     `bson:"reservation_id"`
	ConversationKey string     `bson:"conversation_key"`
	GuestID         string     `bson:"guest_id"`
	Platform        string     `bson:"platform"`
	PrimaryCode     string     `bson:"primary_code"`
	Status          string     `bson:"status"`
	ManagedAt       time.Time  `bson:"managed_at"`
	ConvertedAt     *time.Time `bson:"converted_at,omitempty"`
	Payload         []byte     `bson:"payload"`
}

// MarkManaged upserts a new managed record. If the reservation is already
// tracked the ManagedAt stays unchanged; only status and payload refresh.
func (c *Conversions) MarkManaged(ctx context.Context, r domain.ManagedReservation) error {
	if r.Status == "" {
		r.Status = "managed"
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal conversion: %w", err)
	}
	_, err = c.col.UpdateOne(ctx,
		bson.M{"reservation_id": r.ReservationID},
		bson.M{
			"$setOnInsert": bson.M{"managed_at": r.ManagedAt},
			"$set": bson.M{
				"reservation_id":   r.ReservationID,
				"conversation_key": string(r.ConversationKey),
				"guest_id":         r.GuestID,
				"platform":         r.Platform,
				"primary_code":     r.PrimaryCode,
				"status":           r.Status,
				"payload":          payload,
			},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo upsert conversion: %w", err)
	}
	return nil
}

// GetManaged returns the record for reservationID or ErrNotFound.
func (c *Conversions) GetManaged(ctx context.Context, reservationID string) (domain.ManagedReservation, error) {
	var doc conversionDoc
	err := c.col.FindOne(ctx, bson.M{"reservation_id": reservationID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.ManagedReservation{}, fmt.Errorf("%w: reservationID=%s", ErrNotFound, reservationID)
	}
	if err != nil {
		return domain.ManagedReservation{}, fmt.Errorf("mongo find conversion: %w", err)
	}
	var out domain.ManagedReservation
	if err := json.Unmarshal(doc.Payload, &out); err != nil {
		return domain.ManagedReservation{}, fmt.Errorf("unmarshal conversion: %w", err)
	}
	out.Status = doc.Status
	out.ConvertedAt = doc.ConvertedAt
	return out, nil
}

// RecordConversion flips the status and sets converted_at.
func (c *Conversions) RecordConversion(ctx context.Context, reservationID, status string, at time.Time) error {
	res, err := c.col.UpdateOne(ctx,
		bson.M{"reservation_id": reservationID},
		bson.M{"$set": bson.M{"status": status, "converted_at": at}},
	)
	if err != nil {
		return fmt.Errorf("mongo update conversion: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%w: reservationID=%s", ErrNotFound, reservationID)
	}
	return nil
}

// List returns the newest limit records ordered by managed_at desc.
func (c *Conversions) List(ctx context.Context, limit int) ([]domain.ManagedReservation, error) {
	opts := options.Find().SetSort(bson.D{{Key: "managed_at", Value: -1}})
	if limit > 0 {
		opts = opts.SetLimit(int64(limit))
	}
	cur, err := c.col.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo find conversions: %w", err)
	}
	defer func() { _ = cur.Close(ctx) }()
	var docs []conversionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo decode conversions: %w", err)
	}
	out := make([]domain.ManagedReservation, 0, len(docs))
	for i := range docs {
		var r domain.ManagedReservation
		if err := json.Unmarshal(docs[i].Payload, &r); err != nil {
			return nil, fmt.Errorf("unmarshal conversion: %w", err)
		}
		r.Status = docs[i].Status
		r.ConvertedAt = docs[i].ConvertedAt
		out = append(out, r)
	}
	return out, nil
}
