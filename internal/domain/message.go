// Package domain holds entities, value objects, and sentinel errors for the
// InquiryIQ inquiry-to-reply pipeline. It imports nothing from this project.
package domain

import "time"

// Role identifies the sender of a Message mapped to a canonical role regardless
// of Guesty's per-module type strings ("fromGuest", "toHost", etc.).
type Role string

const (
	RoleGuest  Role = "guest"
	RoleHost   Role = "host"
	RoleSystem Role = "system"
)

// Message is a single chat message in a Guesty conversation, already mapped
// from the raw webhook DTO into domain terms.
type Message struct {
	PostID    string
	Body      string
	CreatedAt time.Time
	Role      Role
	Module    string // "airbnb2" | "booking" | "vrbo" | "direct"
}
