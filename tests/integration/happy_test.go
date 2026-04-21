//go:build integration

package integration_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHappyPathAutoNote exercises the full vertical slice:
//
//  1. Signed webhook arrives at the transport layer.
//  2. Debouncer flushes after a short window.
//  3. Orchestrator classifies (Y6, confidence 0.90) → passes GATE 1.
//  4. Generator runs an agent loop over real Guesty (via Mockoon) tool calls
//     to get_listing and check_availability, then emits a final reply.
//  5. GATE 2 approves → PostNote fires.
//  6. Mockoon records a POST to /communication/conversations/*/send-message
//     with a note body that mentions the guest name and $480 total.
func TestHappyPathAutoNote(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)

	var step atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			return chatReplyJSON(happyClassificationJSON())
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("call_listing_1", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall(
				"call_avail_1", "check_availability",
				`{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`,
			)
		}
		return chatReplyJSON(happyReplyJSON())
	})

	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/happy_availability.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	body := waitForSendMessage(t, mock.logPath, 20*time.Second)
	if !strings.Contains(body, "Sarah") {
		t.Errorf("note body missing guest name: %q", body)
	}
	if !strings.Contains(body, "$480") {
		t.Errorf("note body missing total $480: %q", body)
	}
	if n := countSendMessage(t, mock.logPath); n != 1 {
		t.Errorf("expected exactly one send-message call, saw %d", n)
	}
	if llm.Calls() < 3 {
		t.Errorf("fake LLM expected ≥3 calls (classify + ≥2 generator turns), saw %d", llm.Calls())
	}
}

// happyClassificationJSON is a Y6 availability classification with the
// confidence / action combo that passes GATE 1.
func happyClassificationJSON() string {
	return `{
  "primary_code": "Y6",
  "secondary_code": null,
  "confidence": 0.92,
  "risk_flag": false,
  "risk_reason": "",
  "next_action": "generate_reply",
  "reasoning": "Guest is asking about availability and total for a specific date range and party size.",
  "extracted_entities": {
    "check_in": "2026-04-24",
    "check_out": "2026-04-26",
    "guest_count": 4,
    "pets": null,
    "vehicles": null,
    "listing_hint": "Soho 2BR",
    "additional": []
  }
}`
}

// happyReplyJSON is a C.L.O.S.E.R. reply referencing Sarah and the $480 total
// Mockoon returns. Confidence of 0.88 clears GeneratorMin (0.70).
func happyReplyJSON() string {
	return `{
  "body": "Hi Sarah — you're looking at Fri–Sun (Apr 24–26) for 4, a quick city weekend. Our Soho 2BR sleeps 4 with self-check-in and is right on Spring St. Those dates are open and the total is $480 for 2 nights, taxes included. The second bedroom off the courtyard is the quietest sleep most guests report in Manhattan. Want me to hold the dates for you while you decide?",
  "closer_beats": {
    "clarify": true,
    "label": true,
    "overview": true,
    "sell_certainty": true,
    "explain": true,
    "request": true
  },
  "confidence": 0.88
}`
}
