// Package commitment is the safety net behind the generator's prompt: if the
// generator slips and promises an action the system cannot execute (holding
// dates, booking, reserving), then the guest replies with an affirmative, the
// orchestrator cannot safely auto-generate another reply. This detector runs
// pre-classification on every turn. When it matches it short-circuits to an
// escalation so a human finishes the handoff.
package commitment

import (
	"strings"
	"unicode"
)

// Result is what Detect returns. Ok=false means no commitment flow detected.
// When Ok=true the caller escalates and surfaces MatchedOffer + MatchedReply
// in the escalation detail so operators see why the bot stopped.
type Result struct {
	Ok            bool
	MatchedOffer  string
	MatchedReply  string
	EscalationTag string
}

// MaxAffirmativeLen caps the guest body length we treat as a bare "yes" style
// confirmation. A long message with "yes" in it is usually qualifying another
// question, not committing — let the classifier handle that case normally.
const MaxAffirmativeLen = 80

// offerPhrases are substrings the bot-said previously that indicate it offered
// to take an action. Extend freely — detector accuracy is value additive.
var offerPhrases = []string{
	"hold the dates",
	"hold the date",
	"hold these dates",
	"hold it for you",
	"put a hold",
	"put it on hold",
	"place a hold",
	"block the calendar",
	"lock it in",
	"i'll hold",
	"i can hold",
	"let me hold",
	"i'll reserve",
	"i can reserve",
	"let me reserve",
	"i'll book",
	"i can book",
	"let me book",
	"i'll confirm",
	"let me confirm the booking",
}

// affirmativePhrases are short-form confirmations that — combined with a prior
// offer — mean the guest is accepting. A bare "yes" also counts when the
// message is short enough to be a confirmation and nothing else.
var affirmativePhrases = []string{
	"yes please",
	"yes, please",
	"yes do",
	"yes pls",
	"please do",
	"please go ahead",
	"go ahead",
	"sounds good",
	"book it",
	"do it",
	"sure",
	"sure thing",
	"confirm it",
	"let's do it",
	"lets do it",
	"that works",
}

// Detect returns Ok=true when priorHostBody contains any offer phrase AND
// guestBody is a short affirmative. Matching is case-insensitive and
// whitespace-normalized. Returns the matched offer and reply verbatim so the
// escalation detail carries audit evidence without a second scan.
func Detect(priorHostBody, guestBody string) Result {
	normalizedGuest := normalize(guestBody)
	if len(normalizedGuest) == 0 || len(normalizedGuest) > MaxAffirmativeLen {
		return Result{}
	}
	normalizedHost := normalize(priorHostBody)
	offer := matchFirst(normalizedHost, offerPhrases)
	if offer == "" {
		return Result{}
	}
	affirm := matchAffirmative(normalizedGuest)
	if affirm == "" {
		return Result{}
	}
	return Result{
		Ok:            true,
		MatchedOffer:  offer,
		MatchedReply:  affirm,
		EscalationTag: "commitment_needs_human",
	}
}

// matchAffirmative returns the matched phrase, or the input itself when it is
// a bare "yes" / "yeah" / "ok" style confirmation. We treat the short-bare
// case as a match because otherwise the "yes please do that for me" on the
// Soho scenario fails a substring check against "yes please".
func matchAffirmative(normalizedGuest string) string {
	if phrase := matchFirst(normalizedGuest, affirmativePhrases); phrase != "" {
		return phrase
	}
	switch normalizedGuest {
	case "yes", "yeah", "yep", "yup", "ok", "okay", "k":
		return normalizedGuest
	}
	return ""
}

// matchFirst returns the first phrase in phrases that is a substring of hay,
// or "" when none match. Phrases are assumed already normalized.
func matchFirst(hay string, phrases []string) string {
	for i := range phrases {
		if strings.Contains(hay, phrases[i]) {
			return phrases[i]
		}
	}
	return ""
}

// normalize lowercases, collapses runs of whitespace to a single space, and
// strips leading/trailing whitespace. No punctuation stripping — the phrase
// table already includes punctuation-free forms.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}
