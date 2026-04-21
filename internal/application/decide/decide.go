package decide

import "github.com/chaustre/inquiryiq/internal/domain"

// Thresholds bundles the numeric confidence cutoffs for the two gates.
// Sourced from Config at wiring time so tests can vary them.
type Thresholds struct {
	ClassifierMin float64 // default 0.65
	GeneratorMin  float64 // default 0.70
}

// Decide runs GATE 2 — the final auto-send decision after generation. Returns
// AutoSend=true only when every prior check passes AND the reply text passes
// the restricted-content regex. Every denial carries Reason (machine-readable)
// plus Detail (specifics) so the escalation record reads clearly.
func Decide(cls domain.Classification, reply domain.Reply, validationIssues []string, t domain.Toggles, thr Thresholds) domain.Decision {
	if !t.AutoResponseEnabled {
		return domain.Decision{Reason: "auto_disabled"}
	}
	if reply.AbortReason != "" {
		return domain.Decision{Reason: "generator_aborted", Detail: []string{reply.AbortReason}}
	}
	if d := classifierVerdict(cls, t, thr.ClassifierMin); d.Reason != "ok_to_generate" {
		return d
	}
	if reply.Confidence < thr.GeneratorMin {
		return domain.Decision{Reason: "generator_low_confidence"}
	}
	if len(validationIssues) > 0 {
		return domain.Decision{Reason: "reply_validation", Detail: validationIssues}
	}
	if hits := RestrictedContentHits(reply.Body); len(hits) > 0 {
		return domain.Decision{Reason: "restricted_content", Detail: hits}
	}
	return domain.Decision{AutoSend: true, Reason: "ok"}
}

// classifierVerdict extracts the GATE 1 subset so it can be reused inside
// Decide without the two gates drifting out of sync.
func classifierVerdict(cls domain.Classification, t domain.Toggles, minConf float64) domain.Decision {
	return PreGenerate(cls, t, minConf)
}
