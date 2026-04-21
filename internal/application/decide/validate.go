// Package decide contains the application-layer rules that gate whether a
// generated reply is safe to auto-send or must be escalated to a human.
package decide

import (
	"regexp"
	"unicode/utf8"

	"github.com/chaustre/inquiryiq/internal/domain"
)

const (
	minReplyRunes = 60
	maxReplyRunes = 900
)

var hedgingPattern = regexp.MustCompile(`(?i)\b(i think|maybe|it should|hopefully|i believe|perhaps|kind of|sort of|possibly|might be|probably)\b`)

// ValidateReply inspects a generator output and returns the list of issues
// (nil when the reply is clean). Detecting an abort reason short-circuits —
// the orchestrator will escalate regardless of further checks.
func ValidateReply(r domain.Reply) []string {
	if r.AbortReason != "" {
		return []string{"abort:" + r.AbortReason}
	}
	issues := make([]string, 0, 4)
	n := utf8.RuneCountInString(r.Body)
	lengthFail := n < minReplyRunes || n > maxReplyRunes
	if lengthFail {
		issues = append(issues, "length_out_of_range")
	}
	if hedgingPattern.MatchString(r.Body) {
		issues = append(issues, "hedging_language")
	}
	if !r.CloserBeats.Clarify || !r.CloserBeats.Request || lengthFail {
		issues = append(issues, "missing_core_beats")
	}
	if r.CloserBeats.SellCertainty && !usedTool(r.UsedTools, "check_availability") {
		issues = append(issues, "sell_certainty_without_availability")
	}
	if len(issues) == 0 {
		return nil
	}
	return issues
}

func usedTool(calls []domain.ToolCall, name string) bool {
	for i := range calls {
		if calls[i].Name == name {
			return true
		}
	}
	return false
}
