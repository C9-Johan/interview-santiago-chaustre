package domain

import "time"

// ConversationMemoryRecord is persisted per conversation and indexed by both
// ConversationKey (per-conversation lookup) and GuestID (cross-conversation
// lookup for the Layer-4 guest profile).
type ConversationMemoryRecord struct {
	ConversationKey    ConversationKey
	GuestID            string
	Platform           string
	LastSummary        string
	LastSummaryPostID  string
	KnownEntities      ExtractedEntities
	AdditionalSignals  []Observation
	LastClassification *Classification
	LastAutoSendAt     *time.Time
	LastEscalationAt   *time.Time
	EscalationReasons  []string
	UpdatedAt          time.Time
}
