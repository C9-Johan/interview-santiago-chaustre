// Package mappers holds pure conversion functions between domain types and
// boundary DTOs (Guesty API shapes, transport-layer webhook shapes). No I/O.
package mappers

import "time"

// GuestyListingDTO is the minimal projection of the Guesty listing response
// the classifier/generator care about. The infrastructure layer populates it
// from the raw API payload; the mapper converts it to domain.Listing.
type GuestyListingDTO struct {
	ID           string
	Title        string
	Bedrooms     int
	Beds         int
	MaxGuests    int
	Amenities    []string
	HouseRules   []string
	BasePrice    float64
	Neighborhood string
}

// GuestyAvailabilityDTO mirrors the Guesty availability response.
type GuestyAvailabilityDTO struct {
	Available bool
	Nights    int
	TotalUSD  float64
}

// GuestyMessageDTO is the normalized shape of a single conversation message.
type GuestyMessageDTO struct {
	PostID    string
	Body      string
	CreatedAt time.Time
	Type      string // "fromGuest" | "fromHost" | "toGuest" | "toHost" | "system"
	Module    string
}
