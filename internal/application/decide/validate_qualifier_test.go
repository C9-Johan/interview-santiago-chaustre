package decide

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestValidateQualifier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		reply domain.Reply
		want  []string
	}{
		{
			name:  "happy path: short, no hedging, no abort",
			reply: domain.Reply{Body: "Hi Sarah — when are you thinking of staying? And how many guests will be with you?", Confidence: 0.9},
			want:  nil,
		},
		{
			name:  "below floor",
			reply: domain.Reply{Body: "Hi!", Confidence: 0.9},
			want:  []string{"length_out_of_range"},
		},
		{
			name:  "above ceiling",
			reply: domain.Reply{Body: string(make([]byte, 281)) + "filler padding to exceed the qualifier ceiling with ascii content no unicode"},
			want:  []string{"length_out_of_range"},
		},
		{
			name:  "hedging triggers",
			reply: domain.Reply{Body: "Hi! I think we probably have something that might work — when are you thinking?"},
			want:  []string{"hedging_language"},
		},
		{
			name:  "abort short-circuits",
			reply: domain.Reply{AbortReason: "policy_decline"},
			want:  []string{"abort:policy_decline"},
		},
	}
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateQualifier(tc.reply)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for j := range got {
				if got[j] != tc.want[j] {
					t.Errorf("issue[%d] = %q, want %q", j, got[j], tc.want[j])
				}
			}
		})
	}
}

func TestDecideQualifier(t *testing.T) {
	t.Parallel()
	toggles := domain.Toggles{AutoResponseEnabled: true}
	good := domain.Reply{
		Body:       "Hi Sarah — when are you thinking of staying? And how many guests will be with you?",
		Confidence: 0.85,
	}

	cases := []struct {
		name       string
		reply      domain.Reply
		toggles    domain.Toggles
		minConf    float64
		wantAuto   bool
		wantReason string
	}{
		{"happy path auto-sends", good, toggles, 0.70, true, "ok"},
		{"kill switch disables", good, domain.Toggles{}, 0.70, false, "auto_disabled"},
		{"abort escalates", domain.Reply{AbortReason: "policy_decline"}, toggles, 0.70, false, "generator_aborted"},
		{"length fails validator", domain.Reply{Body: "Hi!", Confidence: 0.9}, toggles, 0.70, false, "reply_validation"},
		{"low confidence escalates", domain.Reply{Body: good.Body, Confidence: 0.5}, toggles, 0.70, false, "generator_low_confidence"},
		{"restricted content", domain.Reply{Body: "Hi! Please send a deposit via Zelle — what dates do you want?", Confidence: 0.9}, toggles, 0.70, false, "restricted_content"},
	}
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := DecideQualifier(tc.reply, tc.toggles, tc.minConf)
			if d.AutoSend != tc.wantAuto {
				t.Errorf("AutoSend = %v, want %v (reason=%q)", d.AutoSend, tc.wantAuto, d.Reason)
			}
			if d.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
