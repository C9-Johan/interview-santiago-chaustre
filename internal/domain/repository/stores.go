package repository

import (
	"context"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// WebhookRecord is the durable raw-body record for replay and audit.
type WebhookRecord struct {
	SvixID     string
	Headers    map[string]string
	RawBody    []byte
	ReceivedAt time.Time
	PostID     string
	ConvRawID  string
	TraceID    string
}

// WebhookStore persists every raw webhook (even duplicates) for replay.
type WebhookStore interface {
	Append(ctx context.Context, rec WebhookRecord) error
	Get(ctx context.Context, postID string) (WebhookRecord, error)
	Since(ctx context.Context, d time.Duration) ([]WebhookRecord, error)
}

// IdempotencyStore prevents double-processing a webhook.
type IdempotencyStore interface {
	SeenOrClaim(ctx context.Context, k domain.ConversationKey, postID string) (already bool, err error)
	Complete(ctx context.Context, k domain.ConversationKey, postID string) error
}

// ClassificationStore persists each completed classification (per postID).
type ClassificationStore interface {
	Put(ctx context.Context, postID string, c domain.Classification) error
	Get(ctx context.Context, postID string) (domain.Classification, error)
}

// EscalationStore persists every escalation for operator review.
type EscalationStore interface {
	Record(ctx context.Context, e domain.Escalation) error
	List(ctx context.Context, limit int) ([]domain.Escalation, error)
}

// ConversationMemoryStore persists per-conversation memory and also supports
// cross-conversation lookup by GuestID (Layer 4 of the memory model).
type ConversationMemoryStore interface {
	Get(ctx context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error)
	Update(ctx context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error
	ListByGuest(ctx context.Context, guestID string, limit int) ([]domain.ConversationMemoryRecord, error)
}

// ConversationAliasStore supports merging conversations under one canonical
// ConversationKey. v1 wires a nil impl (identity resolver); v2 drops in a
// real store without changing callers.
type ConversationAliasStore interface {
	Lookup(ctx context.Context, rawID string) (domain.ConversationKey, bool, error)
	Link(ctx context.Context, rawIDs []string, canonical domain.ConversationKey) error
}
