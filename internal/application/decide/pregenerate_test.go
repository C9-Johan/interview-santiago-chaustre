package decide_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestPreGenerate(t *testing.T) {
	t.Parallel()
	ok := domain.Toggles{AutoResponseEnabled: true}
	cases := []struct {
		name       string
		cls        domain.Classification
		toggles    domain.Toggles
		wantOK     bool
		wantReason string
	}{
		{"auto_disabled", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}, domain.Toggles{}, false, "auto_disabled"},
		{"risk_flag", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9, RiskFlag: true, RiskReason: "venmo"}, ok, false, "risk_flag"},
		{"always_escalate_y2", domain.Classification{PrimaryCode: domain.Y2, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_y5", domain.Classification{PrimaryCode: domain.Y5, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_r1", domain.Classification{PrimaryCode: domain.R1, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_r2", domain.Classification{PrimaryCode: domain.R2, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"not_low_risk_x1", domain.Classification{PrimaryCode: domain.X1, Confidence: 0.9}, ok, false, "code_not_in_low_risk"},
		{"low_confidence", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.5}, ok, false, "classifier_low_confidence"},
		{"ok_g1", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}, ok, true, "ok_to_generate"},
		{"ok_y4", domain.Classification{PrimaryCode: domain.Y4, Confidence: 0.75}, ok, true, "ok_to_generate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := decide.PreGenerate(tc.cls, tc.toggles, 0.65)
			if d.AutoSend != tc.wantOK {
				t.Fatalf("AutoSend: got %v, want %v", d.AutoSend, tc.wantOK)
			}
			if d.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
