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

// holdCommitmentPattern matches first-person promises to take a hold-style
// action on the reservation. These are only legal when the generator just
// called hold_reservation in the same turn — see the matching gate below.
var holdCommitmentPattern = regexp.MustCompile(`(?i)\bi(?:'ll| will| can| am going to)\s+(?:hold|reserve|book|lock|block|put a hold)\b`)

// channelCommitmentPattern matches first-person promises to deliver content
// out-of-band (booking link, document, payment link, etc.). These are NEVER
// fulfillable — the bot's only output is an internal note for the host to
// action, so no tool call can back this up. Always escalates.
var channelCommitmentPattern = regexp.MustCompile(`(?i)\bi(?:'ll| will| can| am going to)\s+(?:send|forward|share|email|text|message|drop|deliver)\b`)

// ValidateReply inspects a generator output and returns the list of issues
// (nil when the reply is clean). Detecting an abort reason short-circuits —
// the orchestrator will escalate regardless of further checks.
func ValidateReply(r domain.Reply) []string {
	if r.AbortReason != "" {
		return []string{"abort:" + r.AbortReason}
	}
	issues := make([]string, 0, 4)
	n := utf8.RuneCountInString(r.Body)
	if n < minReplyRunes || n > maxReplyRunes {
		issues = append(issues, "length_out_of_range")
	}
	if hedgingPattern.MatchString(r.Body) {
		issues = append(issues, "hedging_language")
	}
	// closer_beats is the LLM's self-grade and is empirically noisy — small
	// models routinely mark clarify=false on replies that obviously restate
	// the guest's question. Treating it as a hard-fail signal forces good
	// replies into escalation. Beat quality is now handled by the critic
	// (advisory missing_beat_* tags) and intent_alignment (hard blocker);
	// the validator only enforces the objective sell_certainty/tool pairing
	// because that's grounded in actual UsedTools, not self-report.
	if r.CloserBeats.SellCertainty && !usedSuccessfulTool(r.UsedTools, "check_availability") {
		issues = append(issues, "sell_certainty_without_availability")
	}
	if holdCommitmentPattern.MatchString(r.Body) && !usedSuccessfulTool(r.UsedTools, "hold_reservation") {
		issues = append(issues, "uncovered_commitment_hold")
	}
	if channelCommitmentPattern.MatchString(r.Body) {
		issues = append(issues, "uncovered_commitment_channel")
	}
	if len(issues) == 0 {
		return nil
	}
	return issues
}

func usedSuccessfulTool(calls []domain.ToolCall, name string) bool {
	for i := range calls {
		if calls[i].Name == name && calls[i].Error == "" {
			return true
		}
	}
	return false
}
