package decide_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	okToggles := domain.Toggles{AutoResponseEnabled: true}
	goodCls := domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}
	goodReply := domain.Reply{
		Body:        "Hi Sarah — quick city weekend for 4. Soho 2BR sleeps 4 with self check-in. Those dates are open and the total is $480 all-in. Most guests say the courtyard bedroom is the quietest sleep in Manhattan. Want me to hold it while you decide?",
		Confidence:  0.85,
		CloserBeats: domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true},
		UsedTools:   []domain.ToolCall{{Name: "check_availability"}},
	}
	thresholds := decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70}
	cases := []struct {
		name       string
		cls        domain.Classification
		reply      domain.Reply
		issues     []string
		toggles    domain.Toggles
		wantOK     bool
		wantReason string
	}{
		{"auto_disabled", goodCls, goodReply, nil, domain.Toggles{}, false, "auto_disabled"},
		{"generator_aborted", goodCls, domain.Reply{AbortReason: "max_turns"}, nil, okToggles, false, "generator_aborted"},
		{"reclassified_requires_human", domain.Classification{PrimaryCode: domain.Y2, Confidence: 0.9}, goodReply, nil, okToggles, false, "code_requires_human"},
		{"generator_low_confidence", goodCls, domain.Reply{Body: goodReply.Body, Confidence: 0.5, CloserBeats: goodReply.CloserBeats, UsedTools: goodReply.UsedTools}, nil, okToggles, false, "generator_low_confidence"},
		{"reply_validation", goodCls, goodReply, []string{"hedging_language"}, okToggles, false, "reply_validation"},
		{"restricted_content", goodCls, domain.Reply{Body: "sure, venmo me", Confidence: 0.9, CloserBeats: goodReply.CloserBeats, UsedTools: goodReply.UsedTools}, nil, okToggles, false, "restricted_content"},
		{"ok", goodCls, goodReply, nil, okToggles, true, "ok"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := decide.Decide(tc.cls, tc.reply, tc.issues, tc.toggles, thresholds)
			if d.AutoSend != tc.wantOK {
				t.Fatalf("AutoSend: got %v, want %v (reason=%q)", d.AutoSend, tc.wantOK, d.Reason)
			}
			if d.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
