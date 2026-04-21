package domain

import "time"

// ManagedReservation tracks one reservation that the bot produced an
// auto-reply for. The conversion tracker matches inbound
// reservation.updated webhooks against these records to know when a
// bot-managed inquiry converts into a real booking.
type ManagedReservation struct {
	ReservationID   string
	ConversationKey ConversationKey
	GuestID         string
	Platform        string
	PrimaryCode     string
	ManagedAt       time.Time
	Status          string // "managed" until a terminal reservation.updated flips it.
	ConvertedAt     *time.Time
}
