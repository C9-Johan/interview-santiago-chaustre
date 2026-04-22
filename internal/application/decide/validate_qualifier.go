package decide

import (
	"unicode/utf8"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Qualifier replies are intentionally short. We still enforce a lower
// floor so the model can't ship an empty string, and an upper ceiling
// so it can't run away into a full-paragraph sales pitch.
const (
	minQualifierRunes = 30
	maxQualifierRunes = 280
)

// ValidateQualifier is the qualifier-reply counterpart to ValidateReply. The
// rules are relaxed versus the CLOSER validator: no beat expectations, no
// sell-certainty/tool coupling, shorter length window, but the same
// hedging regex applies — a qualifier that hedges is not safe to auto-send.
func ValidateQualifier(r domain.Reply) []string {
	if r.AbortReason != "" {
		return []string{"abort:" + r.AbortReason}
	}
	issues := make([]string, 0, 3)
	n := utf8.RuneCountInString(r.Body)
	if n < minQualifierRunes || n > maxQualifierRunes {
		issues = append(issues, "length_out_of_range")
	}
	if hedgingPattern.MatchString(r.Body) {
		issues = append(issues, "hedging_language")
	}
	if len(issues) == 0 {
		return nil
	}
	return issues
}

// DecideQualifier is GATE 2 for the X1 qualifier path. Auto-send requires
// auto_response toggle on, no abort, validator clean, confidence floor met,
// and no restricted-content hits. Kept separate from Decide so the CLOSER
// rules and the qualifier rules do not drift into each other.
func DecideQualifier(reply domain.Reply, t domain.Toggles, minConf float64) domain.Decision {
	if !t.AutoResponseEnabled {
		return domain.Decision{Reason: "auto_disabled"}
	}
	if reply.AbortReason != "" {
		return domain.Decision{Reason: "generator_aborted", Detail: []string{reply.AbortReason}}
	}
	if issues := ValidateQualifier(reply); len(issues) > 0 {
		return domain.Decision{Reason: "reply_validation", Detail: issues}
	}
	if reply.Confidence < minConf {
		return domain.Decision{Reason: "generator_low_confidence"}
	}
	if hits := RestrictedContentHits(reply.Body); len(hits) > 0 {
		return domain.Decision{Reason: "restricted_content", Detail: hits}
	}
	return domain.Decision{AutoSend: true, Reason: "ok"}
}
