// Package decide implements the two deterministic auto-send gates (GATE 1
// pre-generation and GATE 2 post-generation) plus the reply validator and
// restricted-content filter. Every function in this package is pure.
package decide

import "github.com/chaustre/inquiryiq/internal/domain"

// PreGenerate runs GATE 1 — the cheap classification-only check that decides
// whether it is worth spending generator tokens at all. Returns a Decision
// whose AutoSend field is advisory only (true means "proceed to generation",
// not "send to the guest"). GATE 2 is the final send decision.
func PreGenerate(cls domain.Classification, t domain.Toggles, classifierMin float64) domain.Decision {
	if !t.AutoResponseEnabled {
		return domain.Decision{Reason: "auto_disabled"}
	}
	if cls.RiskFlag {
		return domain.Decision{Reason: "risk_flag", Detail: []string{cls.RiskReason}}
	}
	if _, always := domain.AlwaysEscalateCodes[cls.PrimaryCode]; always {
		return domain.Decision{Reason: "code_requires_human", Detail: []string{string(cls.PrimaryCode)}}
	}
	if _, ok := domain.LowRiskCodes[cls.PrimaryCode]; !ok {
		return domain.Decision{Reason: "code_not_in_low_risk", Detail: []string{string(cls.PrimaryCode)}}
	}
	if cls.Confidence < classifierMin {
		return domain.Decision{Reason: "classifier_low_confidence"}
	}
	return domain.Decision{AutoSend: true, Reason: "ok_to_generate"}
}
