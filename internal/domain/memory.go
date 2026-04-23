package domain

import "time"

// ConversationMemoryRecord is persisted per conversation and indexed by both
// ConversationKey (per-conversation lookup) and GuestID (cross-conversation
// lookup for the Layer-4 guest profile). Thread is the authoritative turn
// ledger the orchestrator appends to on every turn; LastSummary captures
// older entries that have been rolled up to keep Thread bounded.
type ConversationMemoryRecord struct {
	ConversationKey    ConversationKey
	GuestID            string
	Platform           string
	Thread             []Message
	LastSummary        string
	LastSummaryPostID  string
	KnownEntities      ExtractedEntities
	AdditionalSignals  []Observation
	LastClassification *Classification
	LastAutoSendAt     *time.Time
	LastEscalationAt   *time.Time
	EscalationReasons  []string
	UpdatedAt          time.Time
}

// AppendMessage appends m to Thread when its PostID is not already present.
// Messages without a PostID are always appended — the debouncer never mints
// empty ids for guest turns, so empty here means synthetic (bot reply before
// Guesty returns a post id).
func (r *ConversationMemoryRecord) AppendMessage(m Message) {
	if m.PostID != "" {
		for i := range r.Thread {
			if r.Thread[i].PostID == m.PostID {
				return
			}
		}
	}
	r.Thread = append(r.Thread, m)
}

// MergeEntities folds new into r.KnownEntities using "newer non-nil wins" for
// every typed field and dedup-by-Key for the Additional observation bag
// (newer wins, order preserved). Returns the merged value so callers can
// chain without a second lookup.
func MergeEntities(base, incoming ExtractedEntities) ExtractedEntities {
	if incoming.CheckIn != nil {
		base.CheckIn = incoming.CheckIn
	}
	if incoming.CheckOut != nil {
		base.CheckOut = incoming.CheckOut
	}
	if incoming.GuestCount != nil {
		base.GuestCount = incoming.GuestCount
	}
	if incoming.Pets != nil {
		base.Pets = incoming.Pets
	}
	if incoming.Vehicles != nil {
		base.Vehicles = incoming.Vehicles
	}
	if incoming.ListingHint != nil {
		base.ListingHint = incoming.ListingHint
	}
	base.Additional = mergeAdditional(base.Additional, incoming.Additional)
	return base
}

func mergeAdditional(base, incoming []Observation) []Observation {
	if len(incoming) == 0 {
		return base
	}
	index := make(map[string]int, len(base))
	for i := range base {
		index[base[i].Key] = i
	}
	out := make([]Observation, len(base))
	copy(out, base)
	for i := range incoming {
		obs := incoming[i]
		if obs.Key == "" {
			continue
		}
		if pos, ok := index[obs.Key]; ok {
			out[pos] = obs
			continue
		}
		index[obs.Key] = len(out)
		out = append(out, obs)
	}
	return out
}
