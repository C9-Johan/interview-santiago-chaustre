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
		{"too_short", domain.Reply{Body: "yes", CloserBeats: fullBeats, UsedTools: fullTools}, []string{"length_out_of_range"}},
		{"hedging", domain.Reply{Body: "I think the dates are probably fine, maybe $480, hopefully. " + good.Body, CloserBeats: fullBeats, UsedTools: fullTools}, []string{"hedging_language"}},
		{
			// LLM self-graded clarify=false on a reply that does restate the guest's
			// question — empirically common with small models. The validator now
			// trusts only objective signals; the critic's intent_alignment blocker
			// catches genuinely off-topic replies.
			"clarify_false_no_longer_blocks",
			domain.Reply{Body: good.Body, CloserBeats: domain.CloserBeats{Request: true, Overview: true, SellCertainty: true, Explain: true, Label: true}, UsedTools: fullTools},
			nil,
		},
		{"sell_cert_no_avail", domain.Reply{Body: good.Body, CloserBeats: fullBeats, UsedTools: []domain.ToolCall{{Name: "get_listing"}}}, []string{"sell_certainty_without_availability"}},
		{
			// "I'll send the platform booking link" — no booking-link tool exists, so this
			// is an uncoverable promise the bot can never fulfill. Flag deterministically.
			"uncovered_send_commitment",
			domain.Reply{
				Body:        "Yes the Soho 2BR is open for those dates at $480 total. Self check-in, quiet courtyard. Ready to book? I'll send the platform booking link.",
				CloserBeats: fullBeats,
				UsedTools:   fullTools,
			},
			[]string{"uncovered_commitment_channel"},
		},
		{
			// "I'll hold" without a successful hold_reservation tool call is fabrication.
			"uncovered_hold_commitment",
			domain.Reply{
				Body:        "Yes the Soho 2BR is open for those dates at $480 total. Quiet courtyard, self check-in. I'll hold the dates for you.",
				CloserBeats: fullBeats,
				UsedTools:   fullTools,
			},
			[]string{"uncovered_commitment_hold"},
		},
		{
			// Same hold language, but THIS turn called hold_reservation — the commitment
			// is now covered by real Guesty state and must NOT be flagged.
			"hold_commitment_with_tool",
			domain.Reply{
				Body:        "Yes the Soho 2BR is open for those dates at $480 total. Quiet courtyard, self check-in. I'll hold the dates while you decide.",
				CloserBeats: fullBeats,
				UsedTools: []domain.ToolCall{
					{Name: "check_availability"}, {Name: "get_listing"}, {Name: "hold_reservation"},
				},
			},
			nil,
		},
	}
	for _, tc := range cases {
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
