// Package guesty is the HTTP client that satisfies repository.GuestyClient.
// The BaseURL is injected (defaulting to Mockoon in dev). All responses are
// mapped into domain types via internal/domain/mappers before returning.
package guesty

import "time"

// wireListing is the JSON shape returned by GET /listings/{id}.
type wireListing struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Bedrooms     int      `json:"bedrooms"`
	Beds         int      `json:"beds"`
	MaxGuests    int      `json:"maxGuests"`
	Amenities    []string `json:"amenities"`
	HouseRules   []string `json:"houseRules"`
	BasePrice    float64  `json:"basePrice"`
	Neighborhood string   `json:"neighborhood"`
}

// wireAvailability is the JSON shape returned by GET /availability.
type wireAvailability struct {
	Available bool    `json:"available"`
	Nights    int     `json:"nights"`
	Total     float64 `json:"total"`
}

// wireMessage is the JSON shape of a single message inside a Guesty thread.
type wireMessage struct {
	PostID    string    `json:"postId"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
}

// wireConversationResponse is the JSON shape returned by GET /conversations/{id}.
// Only the fields the pipeline actually consumes are decoded.
type wireConversationResponse struct {
	ID          string `json:"_id"`
	GuestID     string `json:"guestId"`
	Language    string `json:"language"`
	Integration struct {
		Platform string `json:"platform"`
	} `json:"integration"`
	Meta struct {
		GuestName    string `json:"guestName"`
		Reservations []struct {
			ID               string    `json:"_id"`
			CheckIn          time.Time `json:"checkIn"`
			CheckOut         time.Time `json:"checkOut"`
			ConfirmationCode string    `json:"confirmationCode"`
		} `json:"reservations"`
	} `json:"meta"`
	Thread []wireMessage `json:"thread"`
}
