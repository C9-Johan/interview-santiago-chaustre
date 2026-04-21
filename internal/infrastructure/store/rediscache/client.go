// Package rediscache provides Redis/Valkey-backed implementations of
// short-lived caches and claim stores. The package targets go-redis/v9,
// which speaks both the Redis and Valkey RESP protocols.
package rediscache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Client wraps a go-redis client so the service holds a single connection
// pool across every Redis-backed store.
type Client struct {
	rdb *redis.Client
}

// Connect dials addr (e.g. "redis:6379"), authenticates if password is set,
// and pings the server. Callers should Close the returned client on shutdown.
func Connect(ctx context.Context, addr, password string) (*Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("rediscache: empty addr")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close() // ping failed; best-effort cleanup.
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close releases the underlying connection pool.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	if err := c.rdb.Close(); err != nil {
		return fmt.Errorf("redis close: %w", err)
	}
	return nil
}

// Redis returns the underlying client for subpackages to build stores on.
func (c *Client) Redis() *redis.Client { return c.rdb }
