//go:build integration

package integration_test

import (
	"testing"
	"time"
)

// TestPriorityArbiterSwapsAndEscalates exercises the §6 multi-signal
// priority rule end-to-end. The guest body mentions both discount and
// parking. The fake LLM returns primary=Y1 (parking) with secondary=R1
// (discount) — inverted relative to the spec. processinquiry must swap
// the pair (R1 outranks Y1) and, because R1 is in AlwaysEscalateCodes,
// never auto-send. A human gets the turn even though the LLM wanted to
// auto-reply on Y1.
func TestPriorityArbiterSwapsAndEscalates(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	llm := startFakeLLM(t, func(_ openAIRequest) openAIResponse {
		return chatReplyJSON(priorityInvertedClassification())
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/priority_discount_parking.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one escalation, got %d: %+v", len(recs), recs)
	}
	got := recs[0]
	if got.Reason != "code_requires_human" {
		t.Fatalf("reason = %q, want %q (detail=%v)", got.Reason, "code_requires_human", got.Detail)
	}
	if !detailContains(got.Detail, "R1") {
		t.Fatalf("detail missing swapped primary R1: %v", got.Detail)
	}
	if n := countSendMessage(t, mock.logPath); n != 0 {
		t.Fatalf("expected zero send-message calls (priority-swapped R1), saw %d", n)
	}
}

// priorityInvertedClassification returns classifier JSON with primary=Y1,
// secondary=R1 — the spec says R1 outranks Y1 so the orchestrator must swap
// them before GATE 1.
func priorityInvertedClassification() string {
	return `{
  "primary_code": "Y1",
  "secondary_code": "R1",
  "confidence": 0.92,
  "risk_flag": false,
  "next_action": "generate_reply",
  "reasoning": "Guest asks about parking (Y1) with a discount hint (R1).",
  "extracted_entities": {
    "check_in": null,
    "check_out": null,
    "guest_count": null,
    "pets": null,
    "vehicles": null,
    "listing_hint": null,
    "additional": []
  }
}`
}
