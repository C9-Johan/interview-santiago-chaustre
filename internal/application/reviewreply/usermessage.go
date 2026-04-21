package reviewreply

import (
	"fmt"
	"strings"

	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
)

// buildUserMessage serializes Input into a single user message. Every
// guest-derived or LLM-derived content block is wrapped in promptsafety tags
// so the critic cannot be tricked into "approving" a reply that itself
// contains an injection attempt. The wrapper labels match the system prompt's
// convention (reply_under_review, guest_message, tool_observation).
func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "classification_primary: %s\n", in.Classification.PrimaryCode)
	fmt.Fprintf(&b, "classification_confidence: %.2f\n", in.Classification.Confidence)
	b.WriteString("\n")
	b.WriteString(promptsafety.Wrap("guest_message", in.GuestBody))
	b.WriteString("\n\n")
	b.WriteString(promptsafety.Wrap("reply_under_review", in.Reply.Body))
	if len(in.ToolOutputs) > 0 {
		b.WriteString("\n\ntool_observations (authoritative facts):\n")
		for i := range in.ToolOutputs {
			o := in.ToolOutputs[i]
			fmt.Fprintf(&b, "- %s request: %s\n  response: %s\n", o.Name, o.Request, o.Response)
		}
	}
	b.WriteString("\n\nReturn the JSON verdict now.")
	return b.String()
}
