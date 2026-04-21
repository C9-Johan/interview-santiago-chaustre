// Package guesty is the HTTP client that satisfies repository.GuestyClient.
// The BaseURL is injected (defaulting to Mockoon in dev). All responses are
// mapped into domain types via internal/domain/mappers before returning.
package guesty

import "time"

// Wire types mirror Guesty's real Open API shapes (open-api-docs.guesty.com).
// Only the fields this service actually consumes are decoded. Renames and
// shape-shifts are performed at the edge so internal code sees the stable
// GuestyListingDTO / GuestyAvailabilityDTO / GuestyMessageDTO contracts.

// wireListing is GET /v1/listings/{id}. Note: `accommodates` is Guesty's term
// for max guests; `houseRules` is a single string (we split on newlines);
// pricing and location nest under `prices` and `address`.
type wireListing struct {
	ID           string   `json:"_id"`
	Title        string   `json:"title"`
	Nickname     string   `json:"nickname"`
	Bedrooms     int      `json:"bedrooms"`
	Beds         int      `json:"beds"`
	Accommodates int      `json:"accommodates"`
	Amenities    []string `json:"amenities"`
	HouseRules   string   `json:"houseRules"`
	Prices       struct {
		BasePrice   float64 `json:"basePrice"`
		CleaningFee float64 `json:"cleaningFee"`
		Currency    string  `json:"currency"`
	} `json:"prices"`
	Address struct {
		Neighborhood string `json:"neighborhood"`
	} `json:"address"`
}

// wireCalendar is GET /v1/availability-pricing/api/calendar/listings/{id}.
// Each day carries its own price and status; the client aggregates into
// domain.Availability.
type wireCalendar struct {
	Status int `json:"status"`
	Data   struct {
		Days []wireCalendarDay `json:"days"`
	} `json:"data"`
	Message string `json:"message"`
}

type wireCalendarDay struct {
	Date     string  `json:"date"` // YYYY-MM-DD
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"` // "available" | "booked" | "unavailable" | ...
}

// wirePost is the JSON shape of a single message / post inside a Guesty
// conversation. Real API uses both `_id` and `postId` as aliases; we prefer
// `postId` when populated.
type wirePost struct {
	ID        string    `json:"_id"`
	PostID    string    `json:"postId"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
}

// effectivePostID returns postId when present, otherwise falls back to _id.
func (p wirePost) effectivePostID() string {
	if p.PostID != "" {
		return p.PostID
	}
	return p.ID
}

// wirePostsResponse is GET /v1/communication/conversations/{id}/posts.
// Guesty wraps list endpoints with {results, limit, skip, count}.
type wirePostsResponse struct {
	Results []wirePost `json:"results"`
	Limit   int        `json:"limit"`
	Skip    int        `json:"skip"`
	Count   int        `json:"count"`
}

// wireConversation is GET /v1/communication/conversations/{id}. The real
// endpoint does not embed the thread — posts live on a separate route.
type wireConversation struct {
	ID          string `json:"_id"`
	GuestID     string `json:"guestId"`
	Language    string `json:"language"`
	Integration struct {
		Platform string `json:"platform"`
	} `json:"integration"`
	Meta struct {
		GuestName    string                `json:"guestName"`
		Reservations []wireReservationMeta `json:"reservations"`
	} `json:"meta"`
}

type wireReservationMeta struct {
	ID               string    `json:"_id"`
	CheckIn          time.Time `json:"checkIn"`
	CheckOut         time.Time `json:"checkOut"`
	ConfirmationCode string    `json:"confirmationCode"`
	ListingID        string    `json:"listingId"`
}
