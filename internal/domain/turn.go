package domain

import (
	"fmt"
	"strings"
)

// Turn is the deduplicated, chronologically-ordered set of guest messages that
// belong to one logical turn — produced by the debouncer flushing its buffer.
type Turn struct {
	Key        ConversationKey
	Messages   []Message // oldest -> newest, dedup'd by PostID
	LastPostID string
}

// PriorContext carries non-current-turn signal into the classifier and generator:
// prior summary, accumulated per-conversation entities, the thread window, and
// the cross-conversation guest profile (Layer 4 memory).
type PriorContext struct {
	Summary       string
	KnownEntities ExtractedEntities
	Thread        []Message
	GuestProfile  string // compressed cross-conversation memory; empty if none
}

// RenderKnownEntities formats KnownEntities as a deterministic multi-line
// block ready to drop into a classifier/generator user message under a
// `known_from_prior_turns:` heading. Returns "" when nothing is populated so
// callers can no-op on empty. Dates use ISO-8601 for LLM-friendly parsing.
func (p PriorContext) RenderKnownEntities() string {
	var b strings.Builder
	e := p.KnownEntities
	if e.CheckIn != nil {
		fmt.Fprintf(&b, "  check_in: %s\n", e.CheckIn.UTC().Format("2006-01-02"))
	}
	if e.CheckOut != nil {
		fmt.Fprintf(&b, "  check_out: %s\n", e.CheckOut.UTC().Format("2006-01-02"))
	}
	if e.GuestCount != nil {
		fmt.Fprintf(&b, "  guest_count: %d\n", *e.GuestCount)
	}
	if e.Pets != nil {
		fmt.Fprintf(&b, "  pets: %t\n", *e.Pets)
	}
	if e.Vehicles != nil {
		fmt.Fprintf(&b, "  vehicles: %d\n", *e.Vehicles)
	}
	if e.ListingHint != nil && *e.ListingHint != "" {
		fmt.Fprintf(&b, "  listing_hint: %q\n", *e.ListingHint)
	}
	for i := range e.Additional {
		obs := e.Additional[i]
		if obs.Key == "" || obs.Value == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %q\n", obs.Key, obs.Value)
	}
	return b.String()
}
