package generatereply

import (
	"fmt"
	"strings"

	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format(dateFormat))
	fmt.Fprintf(&b, "conversation_id: %s\n", in.ConversationID)
	fmt.Fprintf(&b, "listing_id: %s\n", in.ListingID)
	writeClassification(&b, in.Classification)
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
	if c.ExtractedEntities.CheckIn != nil && c.ExtractedEntities.CheckOut != nil {
		fmt.Fprintf(b, "extracted_dates: check_in=%s check_out=%s\n",
			c.ExtractedEntities.CheckIn.Format(dateFormat),
			c.ExtractedEntities.CheckOut.Format(dateFormat))
	}
	if c.ExtractedEntities.GuestCount != nil {
		fmt.Fprintf(b, "guest_count: %d\n", *c.ExtractedEntities.GuestCount)
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
