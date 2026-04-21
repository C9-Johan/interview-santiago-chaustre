package decide_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
)

func TestRestrictedContentHits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"clean", "Those dates are open and the total is $480.", nil},
		{"venmo", "sure, you can Venmo me later", []string{"off_platform_payment"}},
		{"whatsapp", "text me on WhatsApp at 555-1212", []string{"contact_bypass"}},
		{"whatsapp_only", "contact me on WhatsApp any time", []string{"contact_bypass"}},
		{"address", "the unit is at 123 Spring Street", []string{"address_leak"}},
		{"guarantee", "we guarantee no noise whatsoever", []string{"guarantee_language"}},
		{"discount", "I'll give you a 10% discount", []string{"discount_offer"}},
		{"multiple", "message me on telegram and I'll give you a special rate", []string{"contact_bypass", "discount_offer"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decide.RestrictedContentHits(tc.body)
			sort.Strings(got)
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}
