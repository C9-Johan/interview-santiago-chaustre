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

// BudgetExceededEvent fires when the daily LLM spend cap is reached and
// the watcher trips the auto-response kill-switch. Subscribers can alert
// finance or page an on-call engineer to either raise the cap or let the
// bot stay in escalate-only mode until UTC midnight rollover.
type BudgetExceededEvent struct {
	Day      string    `json:"day"`
	SpentUSD float64   `json:"spent_usd"`
	CapUSD   float64   `json:"cap_usd"`
	Model    string    `json:"model"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
}

// BackpressureDropEvent fires when the dispatch queue rejects a turn.
// Subscribers can alert operators when these exceed a ratio threshold.
type BackpressureDropEvent struct {
	ConversationKey string    `json:"conversation_key"`
	PostID          string    `json:"post_id"`
	Platform        string    `json:"platform"`
	At              time.Time `json:"at"`
}
