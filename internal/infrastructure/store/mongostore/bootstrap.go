package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Bootstrap applies JSON-schema validators and indexes idempotently for every
// collection the service writes to. Running it at startup guarantees
// server-side invariants (required fields, BSON types) hold even if a future
// code change drops a field from the Go struct.
//
// Safe to re-run on every boot:
//   - Validators are installed via collMod when the collection already exists,
//     or CreateCollection on first boot.
//   - Indexes are created via CreateMany which is a no-op when identical
//     indexes already exist.
//
// Returns the first error encountered; partial application is possible when
// Mongo rejects one spec but not others, so operators should inspect the log
// and re-run after fixing the offending document.
func Bootstrap(ctx context.Context, c *Client) error {
	if c == nil || c.db == nil {
		return errors.New("mongostore: nil client")
	}
	for _, spec := range collectionSpecs(c.cols) {
		if err := ensureValidator(ctx, c.db, spec); err != nil {
			return fmt.Errorf("bootstrap %s validator: %w", spec.name, err)
		}
		if len(spec.indexes) == 0 {
			continue
		}
		col := c.db.Collection(spec.name)
		if _, err := col.Indexes().CreateMany(ctx, spec.indexes); err != nil {
			return fmt.Errorf("bootstrap %s indexes: %w", spec.name, err)
		}
	}
	return nil
}

// collectionSpec is one collection's bootstrap contract: its name, the BSON
// JSON-schema validator mongo should enforce on every write, and the indexes
// the application relies on.
type collectionSpec struct {
	name      string
	validator bson.M
	indexes   []mongo.IndexModel
}

func collectionSpecs(c Collections) []collectionSpec {
	return []collectionSpec{
		{
			name:      c.Webhooks,
			validator: webhooksValidator(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "post_id", Value: 1}}},
				{Keys: bson.D{{Key: "received_at", Value: -1}}},
			},
		},
		{
			name:      c.Classifications,
			validator: classificationsValidator(),
			indexes: []mongo.IndexModel{
				{
					Keys:    bson.D{{Key: "post_id", Value: 1}},
					Options: uniqueIndex(),
				},
			},
		},
		{
			name:      c.Escalations,
			validator: escalationsValidator(),
			indexes: []mongo.IndexModel{
				{Keys: bson.D{{Key: "created_at", Value: -1}}},
			},
		},
		{
			name:      c.ConversationMemory,
			validator: conversationMemoryValidator(),
			indexes: []mongo.IndexModel{
				{
					Keys:    bson.D{{Key: "conversation_key", Value: 1}},
					Options: uniqueIndex(),
				},
				{Keys: bson.D{{Key: "guest_id", Value: 1}}},
			},
		},
		{
			name:      c.Conversions,
			validator: conversionsValidator(),
			indexes: []mongo.IndexModel{
				{
					Keys:    bson.D{{Key: "reservation_id", Value: 1}},
					Options: uniqueIndex(),
				},
			},
		},
	}
}

// ensureValidator installs the JSON-schema validator. On first boot the
// collection is created with the validator attached; on subsequent boots
// collMod updates the validator to the current spec. validationLevel=strict
// + validationAction=error make violations fail the write rather than warn.
func ensureValidator(ctx context.Context, db *mongo.Database, spec collectionSpec) error {
	names, err := db.ListCollectionNames(ctx, bson.M{"name": spec.name})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	cmd := bson.D{
		{Key: "validator", Value: spec.validator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}
	if len(names) == 0 {
		create := append(bson.D{{Key: "create", Value: spec.name}}, cmd...)
		if err := db.RunCommand(ctx, create).Err(); err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
		return nil
	}
	mod := append(bson.D{{Key: "collMod", Value: spec.name}}, cmd...)
	if err := db.RunCommand(ctx, mod).Err(); err != nil {
		return fmt.Errorf("collMod: %w", err)
	}
	return nil
}

// uniqueIndex is a small helper to keep the collectionSpec list readable —
// the v2 driver's options builder reads poorly inline.
func uniqueIndex() *options.IndexOptionsBuilder {
	return options.Index().SetUnique(true)
}
