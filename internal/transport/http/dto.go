// Package http implements the inbound webhook transport: Svix-signed request
// verification, raw-body DTO unmarshalling, mapping to domain types, and the
// chi router + handlers. The package is the only transport-layer package; it
// depends on application and domain inward, never the reverse.
package http

import "time"

// WebhookRequestDTO mirrors the fields of the Guesty `reservation.messageReceived`
// webhook body that the pipeline uses. Fields the pipeline does not need are
// silently ignored so schema drift in Guesty does not break ingestion.
//
// Transport-local — no downstream package imports this DTO.
type WebhookRequestDTO struct {
	Event         string          `json:"event"`
	ReservationID string          `json:"reservationId"`
	Message       WebhookMessage  `json:"message"`
	Conversation  WebhookConv     `json:"conversation"`
	Meta          WebhookMetaBody `json:"meta"`
}

// WebhookMessage is the message that triggered the webhook.
type WebhookMessage struct {
	PostID    string    `json:"postId"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
}

// WebhookConv is the full conversation snapshot at webhook time.
type WebhookConv struct {
	ID          string             `json:"_id"`
	GuestID     string             `json:"guestId"`
	Language    string             `json:"language"`
	Status      string             `json:"status"`
	Integration WebhookIntegration `json:"integration"`
	Meta        WebhookConvMeta    `json:"meta"`
	Thread      []WebhookMessage   `json:"thread"`
}

// WebhookIntegration is the platform block nested inside the conversation.
type WebhookIntegration struct {
	Platform string `json:"platform"`
}

// WebhookConvMeta is the conversation.meta block.
type WebhookConvMeta struct {
	GuestName    string                   `json:"guestName"`
	Reservations []WebhookReservationMeta `json:"reservations"`
}

// WebhookReservationMeta is one entry in conversation.meta.reservations.
type WebhookReservationMeta struct {
	ID               string    `json:"_id"`
	CheckIn          time.Time `json:"checkIn"`
	CheckOut         time.Time `json:"checkOut"`
	ConfirmationCode string    `json:"confirmationCode"`
}

// WebhookMetaBody is the top-level meta block with event + message ids.
type WebhookMetaBody struct {
	EventID   string `json:"eventId"`
	MessageID string `json:"messageId"`
}
