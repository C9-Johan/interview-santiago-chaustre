package domain

// Listing is the mapped domain view of a Guesty listing.
type Listing struct {
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

// Availability is the mapped result of a Guesty availability check.
type Availability struct {
	Available bool
	Nights    int
	TotalUSD  float64
}
