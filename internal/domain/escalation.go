package domain

import "time"

// Escalation is the durable record of a turn routed to a human operator.
type Escalation struct {
	ID              string
	TraceID         string
	PostID          string
	ConversationKey ConversationKey
	GuestID         string
	GuestName       string
	Platform        string
	CreatedAt       time.Time
	Reason          string
	Detail          []string
	Classification  Classification
	Reply           *Reply
	MissingInfo     []string
	PartialFindings string
}
