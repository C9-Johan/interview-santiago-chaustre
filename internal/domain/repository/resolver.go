package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// ConversationResolver canonicalizes a raw conversation into a stable
// ConversationKey so downstream components never see raw platform ids.
type ConversationResolver interface {
	Resolve(ctx context.Context, c domain.Conversation) (domain.ConversationKey, error)
}
