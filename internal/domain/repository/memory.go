package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// ConversationMemory summarizes older messages in a conversation into a short
// paragraph, cached per (key, lastSummarizedPostID). It is separate from the
// persistent ConversationMemoryStore — this one is a derived view only.
type ConversationMemory interface {
	Summary(ctx context.Context, k domain.ConversationKey, thread []domain.Message, window int) (string, error)
}
