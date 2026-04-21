package generatereply

import (
	"fmt"
	"strings"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format(dateFormat))
	fmt.Fprintf(&b, "conversation_id: %s\n", in.ConversationID)
	fmt.Fprintf(&b, "listing_id: %s\n", in.ListingID)
	writeClassification(&b, in.Classification)
	writePrior(&b, in.Prior)
	b.WriteString("\n---\nguest_turn (respond to THIS):\n")
	for i := range in.Turn.Messages {
		fmt.Fprintf(&b, "%s\n", in.Turn.Messages[i].Body)
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

func writePrior(b *strings.Builder, p domain.PriorContext) {
	if p.GuestProfile != "" {
		fmt.Fprintf(b, "guest_profile (advisory): %q\n", p.GuestProfile)
	}
	if p.Summary != "" {
		fmt.Fprintf(b, "prior_thread_summary: %q\n", p.Summary)
	}
	if len(p.Thread) == 0 {
		return
	}
	fmt.Fprintf(b, "prior_thread (last %d):\n", len(p.Thread))
	for i := range p.Thread {
		m := p.Thread[i]
		fmt.Fprintf(b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
}
