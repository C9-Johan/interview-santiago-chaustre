package rediscache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Idempotency stores (ConversationKey, postID) claims with a TTL. Two-phase:
// SeenOrClaim writes the claim with NX (atomic first-claimer); Complete
// refreshes the TTL so the claim survives retries for the expected window.
type Idempotency struct {
	rdb    *redis.Client
	prefix string
	ttl    time.Duration
}

// NewIdempotency returns an Idempotency store using the provided client.
// ttl should comfortably exceed the longest retry window (Svix retries for
// ~24h; we recommend at least 48h in production).
func NewIdempotency(c *Client, prefix string, ttl time.Duration) *Idempotency {
	if prefix == "" {
		prefix = "inquiryiq:idem"
	}
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	return &Idempotency{rdb: c.rdb, prefix: prefix, ttl: ttl}
}

// SeenOrClaim atomically claims (k, postID). The first caller sees already=false
// and the claim is persisted with the configured TTL. Subsequent callers see
// already=true until the key expires.
func (i *Idempotency) SeenOrClaim(ctx context.Context, k domain.ConversationKey, postID string) (bool, error) {
	key := i.key(k, postID)
	prev, err := i.rdb.SetArgs(ctx, key, "inflight", redis.SetArgs{Mode: "NX", TTL: i.ttl, Get: true}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis set nx: %w", err)
	}
	return prev != "", nil
}

// Complete marks an inflight claim as done. It refreshes the TTL so the
// record stays visible for replay tooling through the full retry window.
func (i *Idempotency) Complete(ctx context.Context, k domain.ConversationKey, postID string) error {
	key := i.key(k, postID)
	if err := i.rdb.Set(ctx, key, "complete", i.ttl).Err(); err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("redis set complete: %w", err)
	}
	return nil
}

func (i *Idempotency) key(k domain.ConversationKey, postID string) string {
	return fmt.Sprintf("%s:%s:%s", i.prefix, string(k), postID)
}
