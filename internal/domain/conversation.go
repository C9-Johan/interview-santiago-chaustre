package domain

import "time"

// ConversationKey is an opaque canonical identifier for a conversation.
// Transport and infrastructure resolve the raw Guesty conversation._id into
// this type via ConversationResolver; downstream code never handles raw ids.
type ConversationKey string

// Integration captures platform-specific metadata from Guesty.
type Integration struct {
	Platform string // "airbnb2" | "bookingCom" | "vrbo" | "manual" | "direct"
}

// Reservation is the minimal view of a Guesty reservation the pipeline uses.
type Reservation struct {
	ID               string
	CheckIn          time.Time
	CheckOut         time.Time
	ConfirmationCode string
}

// Conversation is the mapped domain view of a Guesty conversation snapshot.
type Conversation struct {
	RawID        string // raw conversation._id; use ConversationKey downstream
	GuestID      string
	GuestName    string
	Language     string
	Integration  Integration
	Reservations []Reservation
	Thread       []Message
}
