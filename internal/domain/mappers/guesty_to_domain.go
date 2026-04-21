package mappers

import "github.com/chaustre/inquiryiq/internal/domain"

// ListingFromGuesty maps the Guesty listing projection into domain terms.
func ListingFromGuesty(d GuestyListingDTO) domain.Listing {
	return domain.Listing{
		ID:           d.ID,
		Title:        d.Title,
		Bedrooms:     d.Bedrooms,
		Beds:         d.Beds,
		MaxGuests:    d.MaxGuests,
		Amenities:    d.Amenities,
		HouseRules:   d.HouseRules,
		BasePrice:    d.BasePrice,
		Neighborhood: d.Neighborhood,
	}
}

// AvailabilityFromGuesty maps the Guesty availability projection.
func AvailabilityFromGuesty(d GuestyAvailabilityDTO) domain.Availability {
	return domain.Availability{Available: d.Available, Nights: d.Nights, TotalUSD: d.TotalUSD}
}

// MessageFromGuesty normalizes the Guesty per-module "type" string into a
// canonical domain.Role. Unknown types become RoleSystem so the pipeline
// will not auto-process them.
func MessageFromGuesty(d GuestyMessageDTO) domain.Message {
	return domain.Message{
		PostID:    d.PostID,
		Body:      d.Body,
		CreatedAt: d.CreatedAt,
		Role:      roleFromGuestyType(d.Type),
		Module:    d.Module,
	}
}

func roleFromGuestyType(t string) domain.Role {
	switch t {
	case "fromGuest", "toHost":
		return domain.RoleGuest
	case "fromHost", "toGuest":
		return domain.RoleHost
	default:
		return domain.RoleSystem
	}
}
