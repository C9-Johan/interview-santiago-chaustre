package eventbus

import "time"

// EscalationRecordedEvent fires whenever the orchestrator records an
// escalation. Subscribers use it to drive Slack notifications, oncall
// pages, or external audit trails.
type EscalationRecordedEvent struct {
	ID              string    `json:"id"`
	TraceID         string    `json:"trace_id"`
	PostID          string    `json:"post_id"`
	ConversationKey string    `json:"conversation_key"`
	GuestID         string    `json:"guest_id"`
	Platform        string    `json:"platform"`
	Reason          string    `json:"reason"`
	Detail          []string  `json:"detail,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ConversionManagedEvent fires when the orchestrator auto-replies on a
// reservation-bearing conversation. Subscribers chain this with
// ConversionDoneEvent to compute per-channel conversion rate.
type ConversionManagedEvent struct {
	ReservationID   string    `json:"reservation_id"`
	ConversationKey string    `json:"conversation_key"`
	Platform        string    `json:"platform"`
	PrimaryCode     string    `json:"primary_code"`
	At              time.Time `json:"at"`
}

// ConversionDoneEvent fires when a Guesty reservation.updated webhook
// transitions a managed reservation to confirmed.
type ConversionDoneEvent struct {
	ReservationID string    `json:"reservation_id"`
	Platform      string    `json:"platform"`
	At            time.Time `json:"at"`
}

// ToggleFlippedEvent fires on every admin kill-switch change. Subscribers
// can mirror this to PagerDuty, Slack, or an audit log.
type ToggleFlippedEvent struct {
	Field string    `json:"field"`
	Prev  bool      `json:"prev"`
	Now   bool      `json:"now"`
	Actor string    `json:"actor"`
	At    time.Time `json:"at"`
}

// BackpressureDropEvent fires when the dispatch queue rejects a turn.
// Subscribers can alert operators when these exceed a ratio threshold.
type BackpressureDropEvent struct {
	ConversationKey string    `json:"conversation_key"`
	PostID          string    `json:"post_id"`
	Platform        string    `json:"platform"`
	At              time.Time `json:"at"`
}
