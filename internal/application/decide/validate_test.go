package decide_test

import (
	"strings"
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestValidateReply(t *testing.T) {
	t.Parallel()
	fullBeats := domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true}
	fullTools := []domain.ToolCall{{Name: "check_availability"}, {Name: "get_listing"}}
	good := domain.Reply{Body: strings.Repeat("Hi Sarah, dates open, $480 total, hold it? ", 3), CloserBeats: fullBeats, UsedTools: fullTools}

	cases := []struct {
		name  string
		reply domain.Reply
		want  []string
	}{
		{"clean", good, nil},
		{"abort", domain.Reply{AbortReason: "max_turns"}, []string{"abort:max_turns"}},
		{"too_short", domain.Reply{Body: "yes", CloserBeats: fullBeats, UsedTools: fullTools}, []string{"length_out_of_range", "missing_core_beats"}},
		{"hedging", domain.Reply{Body: "I think the dates are probably fine, maybe $480, hopefully. " + good.Body, CloserBeats: fullBeats, UsedTools: fullTools}, []string{"hedging_language"}},
		{"no_clarify", domain.Reply{Body: good.Body, CloserBeats: domain.CloserBeats{Request: true, Overview: true, SellCertainty: true, Explain: true, Label: true}, UsedTools: fullTools}, []string{"missing_core_beats"}},
		{"sell_cert_no_avail", domain.Reply{Body: good.Body, CloserBeats: fullBeats, UsedTools: []domain.ToolCall{{Name: "get_listing"}}}, []string{"sell_certainty_without_availability"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decide.ValidateReply(tc.reply)
			if !sameIssues(got, tc.want) {
				t.Fatalf("issues: got %v, want %v", got, tc.want)
			}
		})
	}
}

func sameIssues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
