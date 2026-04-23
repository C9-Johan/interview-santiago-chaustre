package repository

import (
	"context"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// GuestyClient is the contract for Guesty API interactions used by the pipeline.
// Implementations wrap the real Guesty HTTP API (via a configurable BaseURL,
// which is why the same impl points at Mockoon in dev).
type GuestyClient interface {
	// GetListing fetches full listing facts. Returns domain.Listing and wraps
	// transport errors with %w.
	GetListing(ctx context.Context, id string) (domain.Listing, error)

	// CheckAvailability reports availability and total price for a date range.
	CheckAvailability(ctx context.Context, listingID string, from, to time.Time) (domain.Availability, error)

	// GetConversationHistory returns up to limit messages older than beforePostID
	// (or the oldest page when beforePostID is empty). Results are oldest->newest.
	GetConversationHistory(ctx context.Context, convID string, limit int, beforePostID string) ([]domain.Message, error)

	// GetConversation returns the current conversation snapshot — used by the
	// orchestrator to recheck whether a host has already replied.
	GetConversation(ctx context.Context, convID string) (domain.Conversation, error)

	// PostNote posts an internal note (type="note") to the conversation. Never
	// reaches the guest.
	PostNote(ctx context.Context, conversationID, body string) error

	// CreateReservation POSTs /reservations with the given hold input. Returns
	// the created reservation's id + confirmation code so the generator can
	// cite a real hold. Implementations must map transport errors with %w.
	CreateReservation(ctx context.Context, in domain.ReservationHoldInput) (domain.ReservationHoldResult, error)
}
