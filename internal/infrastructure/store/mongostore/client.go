// Package mongostore provides MongoDB-backed implementations of the durable
// repository contracts. Each store targets one collection in a shared
// database; collection names are stable so external tooling (mongo-express,
// dashboards) can explore them directly.
package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrNotFound is returned by Get-style lookups when no document matches.
var ErrNotFound = errors.New("record not found")

// Collections groups the four collection names the service writes to.
type Collections struct {
	Webhooks           string
	Classifications    string
	Replies            string
	Escalations        string
	ConversationMemory string
	Conversions        string
}

// DefaultCollections returns the stable collection names used in compose.
func DefaultCollections() Collections {
	return Collections{
		Webhooks:           "webhooks",
		Classifications:    "classifications",
		Replies:            "replies",
		Escalations:        "escalations",
		ConversationMemory: "conversation_memory",
		Conversions:        "conversions",
	}
}

// Client owns a *mongo.Client scoped to one database and exposes factories
// that construct the per-collection stores. Close disconnects cleanly.
type Client struct {
	client *mongo.Client
	db     *mongo.Database
	cols   Collections
}

// Connect dials uri, pings the primary, and selects database. Callers should
// Close the returned client on shutdown.
func Connect(ctx context.Context, uri, database string) (*Client, error) {
	if uri == "" {
		return nil, fmt.Errorf("mongostore: empty uri")
	}
	if database == "" {
		return nil, fmt.Errorf("mongostore: empty database")
	}
	cli, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := cli.Ping(ctx, nil); err != nil {
		_ = cli.Disconnect(context.Background()) // ping failed; best-effort cleanup.
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &Client{client: cli, db: cli.Database(database), cols: DefaultCollections()}, nil
}

// Close disconnects from the server. Safe to call after a failed Connect.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.client == nil {
		return nil
	}
	if err := c.client.Disconnect(ctx); err != nil {
		return fmt.Errorf("mongo disconnect: %w", err)
	}
	return nil
}

// Database returns the underlying *mongo.Database for ad-hoc reads.
func (c *Client) Database() *mongo.Database { return c.db }

// Collections returns the names used by the stores so callers can ensure
// indexes or run inspection queries.
func (c *Client) Collections() Collections { return c.cols }
