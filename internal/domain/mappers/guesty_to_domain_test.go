package mappers_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
)

func TestListingFromGuesty(t *testing.T) {
	t.Parallel()
	got := mappers.ListingFromGuesty(mappers.GuestyListingDTO{
		ID: "L1", Title: "Soho 2BR", Bedrooms: 2, Beds: 3, MaxGuests: 4,
		Amenities: []string{"wifi"}, BasePrice: 200, Neighborhood: "Soho",
	})
	want := domain.Listing{
		ID: "L1", Title: "Soho 2BR", Bedrooms: 2, Beds: 3, MaxGuests: 4,
		Amenities: []string{"wifi"}, BasePrice: 200, Neighborhood: "Soho",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listing mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestMessageFromGuestyRoles(t *testing.T) {
	t.Parallel()
	cases := map[string]domain.Role{
		"fromGuest": domain.RoleGuest,
		"toHost":    domain.RoleGuest,
		"fromHost":  domain.RoleHost,
		"toGuest":   domain.RoleHost,
		"system":    domain.RoleSystem,
		"":          domain.RoleSystem,
		"weird":     domain.RoleSystem,
	}
	now := time.Now()
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			m := mappers.MessageFromGuesty(mappers.GuestyMessageDTO{PostID: "p", Body: "b", CreatedAt: now, Type: in, Module: "airbnb2"})
			if m.Role != want {
				t.Fatalf("role for %q: got %q, want %q", in, m.Role, want)
			}
		})
	}
}

func TestNoteFromDomain(t *testing.T) {
	t.Parallel()
	n := mappers.NoteFromDomain("hello")
	if n.Body != "hello" || n.Type != "note" {
		t.Fatalf("got %+v", n)
	}
}
