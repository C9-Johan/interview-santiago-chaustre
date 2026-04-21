package domain

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
