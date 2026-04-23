package commitment_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/commitment"
)

func TestDetectMatchesSohoScenario(t *testing.T) {
	host := "Yes, the Soho 2BR is available for April 24-26. Want me to hold these dates while you confirm?"
	guest := "Yes please do that for me"

	got := commitment.Detect(host, guest)
	if !got.Ok {
		t.Fatalf("Soho 'hold these dates' + 'yes please do that' must match, got %+v", got)
	}
	if got.EscalationTag != "commitment_needs_human" {
		t.Fatalf("escalation tag mismatch: %q", got.EscalationTag)
	}
	if got.MatchedOffer == "" || got.MatchedReply == "" {
		t.Fatalf("matched phrases must be populated: %+v", got)
	}
}

func TestDetectMatchMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		host  string
		guest string
		ok    bool
	}{
		{"hold offer + yes please", "I can hold the dates for you.", "yes please", true},
		{"reserve offer + please do", "Let me reserve those dates for the weekend.", "please do", true},
		{"book offer + go ahead", "I'll book it for you now if you want.", "go ahead", true},
		{"lock it in + sounds good", "Want me to lock it in for you?", "sounds good", true},
		{"bare yes is enough", "Want me to hold the dates?", "yes", true},
		{"bare ok is enough", "I'll book it right now.", "ok", true},
		{"capitalized and spaced", "I'LL  HOLD THE DATES", "  Yes  Please  ", true},
		{"short yes please comma form", "Want me to place a hold on the dates?", "yes, please", true},
		{"no prior offer — yes alone", "Here's our availability: Fri-Sun is open.", "yes please", false},
		{"prior offer but guest is long question", "I can hold these dates.", "yes please but also what about parking and pets and would the host be ok with late check-in after 11pm", false},
		{"unrelated host message", "The total is $480 for 2 nights.", "yes please", false},
		{"no affirmative — guest asks instead", "I'll hold it if you'd like.", "what about pets?", false},
		{"empty guest", "I'll hold it.", "", false},
		{"empty host", "", "yes please", false},
	}

	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got := commitment.Detect(tc.host, tc.guest)
			if got.Ok != tc.ok {
				t.Fatalf("Detect(%q, %q) ok = %v, want %v (match=%+v)",
					tc.host, tc.guest, got.Ok, tc.ok, got)
			}
		})
	}
}

func TestDetectMaxAffirmativeLenIsBoundary(t *testing.T) {
	host := "I'll hold the dates."
	// 80 chars including the trailing 'yes please' — should match.
	guest := "yes please " + stringOfLen("x", 80-len("yes please "))
	if len(guest) != 80 {
		t.Fatalf("setup error: guest len %d", len(guest))
	}
	if !commitment.Detect(host, guest).Ok {
		t.Fatalf("80-char guest with affirmative prefix must match at boundary")
	}
	if commitment.Detect(host, guest+"y").Ok {
		t.Fatalf("81-char guest must not match (exceeds MaxAffirmativeLen)")
	}
}

func stringOfLen(base string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = base[0]
	}
	return string(out)
}
