package qualifyreply

import (
	"fmt"
	"strings"

	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
	"github.com/chaustre/inquiryiq/internal/domain"
)

const dateFormat = "2006-01-02"

// missingSlots inspects extracted_entities and returns the ordered list of
// qualifying questions the LLM should consider. The orchestrator also uses
// this list to pre-fill the user message so the model doesn't waste a token
// re-deriving it.
func missingSlots(c domain.Classification, priorEmpty bool) []string {
	out := make([]string, 0, 4)
	if c.ExtractedEntities.CheckIn == nil || c.ExtractedEntities.CheckOut == nil {
		out = append(out, "dates")
	}
	if c.ExtractedEntities.GuestCount == nil {
		out = append(out, "guests")
	}
	if c.ExtractedEntities.ListingHint == nil && priorEmpty {
		out = append(out, "listing")
	}
	return out
}

func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format(dateFormat))
	fmt.Fprintf(&b, "conversation_id: %s\n", in.ConversationID)
	if in.ListingID != "" {
		fmt.Fprintf(&b, "listing_id: %s\n", in.ListingID)
	}
	if in.GuestName != "" {
		fmt.Fprintf(&b, "guest_name: %s\n", in.GuestName)
	}
	writeClassification(&b, in.Classification)
	priorEmpty := len(in.Prior.Thread) == 0 && in.Prior.Summary == ""
	if slots := missingSlots(in.Classification, priorEmpty); len(slots) > 0 {
		fmt.Fprintf(&b, "missing_slots: %s\n", strings.Join(slots, ","))
	}
	writePriorMeta(&b, in.Prior)
	b.WriteString("\n")
	b.WriteString(promptsafety.Wrap("prior_thread", priorThreadText(in.Prior)))
	b.WriteString("\n\n")
	b.WriteString(promptsafety.Wrap("guest_turn", guestTurnText(in.Turn)))
	b.WriteString("\n")
	return b.String()
}

func priorThreadText(p domain.PriorContext) string {
	if len(p.Thread) == 0 {
		return "(empty)"
	}
	var b strings.Builder
	for i := range p.Thread {
		m := p.Thread[i]
		fmt.Fprintf(&b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
	return b.String()
}

func guestTurnText(t domain.Turn) string {
	var b strings.Builder
	for i := range t.Messages {
		fmt.Fprintf(&b, "%s\n", t.Messages[i].Body)
	}
	return b.String()
}

func writeClassification(b *strings.Builder, c domain.Classification) {
	fmt.Fprintf(b, "classification: primary=%s confidence=%.2f next_action=%s reasoning=%q\n",
		c.PrimaryCode, c.Confidence, c.NextAction, c.Reasoning)
	if c.ExtractedEntities.ListingHint != nil {
		fmt.Fprintf(b, "listing_hint: %q\n", *c.ExtractedEntities.ListingHint)
	}
}

func writePriorMeta(b *strings.Builder, p domain.PriorContext) {
	if p.GuestProfile != "" {
		fmt.Fprintf(b, "guest_profile (advisory): %q\n", p.GuestProfile)
	}
	if p.Summary != "" {
		fmt.Fprintf(b, "prior_thread_summary: %q\n", p.Summary)
	}
}
