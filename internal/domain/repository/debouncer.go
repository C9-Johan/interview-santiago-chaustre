package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Debouncer buffers inbound messages per ConversationKey and emits a single
// Turn after the configured quiet-window elapses, bounded by a hard cap.
// Implementations must dedup on Message.PostID within the buffer.
type Debouncer interface {
	// Push records msg and (re)arms the buffer's flush timer for k.
	Push(ctx context.Context, k domain.ConversationKey, msg domain.Message)
	// CancelIfHostReplied drops k's active buffer when the role is not a guest.
	CancelIfHostReplied(k domain.ConversationKey, role domain.Role)
	// Stop shuts down internal timers cleanly.
	Stop()
}
