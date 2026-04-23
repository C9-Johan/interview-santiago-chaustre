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

// ReservationHoldStatus enumerates the Guesty reservation states used by the
// hold path. "inquiry" is a soft hold (no dates reserved on the calendar);
// "reserved" is a booking request that blocks the calendar pending host
// confirmation. We never auto-send status="confirmed" — that is a human
// commitment that stays out of the bot loop by design.
type ReservationHoldStatus string

const (
	// ReservationInquiry is a soft pre-booking hold without calendar reservation.
	ReservationInquiry ReservationHoldStatus = "inquiry"
	// ReservationReserved is a booking request that blocks the dates pending
	// host confirmation.
	ReservationReserved ReservationHoldStatus = "reserved"
)

// ReservationHoldInput captures the minimum Guesty POST /reservations accepts
// when creating an inquiry/reserved hold from bot context. Guest identity is
// optional — Guesty creates a guest record from GuestName/GuestEmail when
// neither a Guesty GuestID nor a platform guest id is known yet.
type ReservationHoldInput struct {
	ListingID  string
	CheckIn    time.Time
	CheckOut   time.Time
	GuestCount int
	Status     ReservationHoldStatus
	GuestID    string // existing Guesty guest id, optional
	GuestName  string // only used when GuestID is empty
	GuestEmail string // only used when GuestID is empty
}

// ReservationHoldResult is what the client returns after a successful hold.
// ID + ConfirmationCode surface in the generator tool output so the reply
// can cite a real hold id without fabrication.
type ReservationHoldResult struct {
	ID               string
	Status           ReservationHoldStatus
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
