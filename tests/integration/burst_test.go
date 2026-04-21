//go:build integration

package integration_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBurstDebounce posts three back-to-back guest webhooks inside a single
// debounce window and asserts the service processed them as ONE turn:
//
//  1. The classifier (fake LLM, no tools) is called exactly once.
//  2. Mockoon observes exactly one POST to /send-message.
//
// This is the burst-messages behavior from GUESTY_WEBHOOK_CONTRACT.md §2:
// debounce ~window per conversation so the LLM sees the full turn, not three
// fragments.
func TestBurstDebounce(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)

	var classifyCalls atomic.Int32
	var step atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			classifyCalls.Add(1)
			return chatReplyJSON(burstClassificationJSON())
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("call_listing_burst", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall(
				"call_avail_burst", "check_availability",
				`{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`,
			)
		}
		return chatReplyJSON(burstReplyJSON())
	})

	// Debounce window of 500ms is long enough for all three bursts to land
	// before the first flush fires.
	svc := bootService(t, mock.baseURL, llm.URL(), 500*time.Millisecond)

	for _, fx := range []string{
		"fixtures/webhooks/burst_hi.json",
		"fixtures/webhooks/burst_dates.json",
		"fixtures/webhooks/burst_party.json",
	} {
		resp := postSignedWebhook(t, svc.url, fx)
		_ = resp.Body.Close()
		if resp.StatusCode != 202 {
			t.Fatalf("webhook %s status: got %d, want 202", fx, resp.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}

	body := waitForSendMessage(t, mock.logPath, 20*time.Second)
	if !strings.Contains(body, "Sarah") {
		t.Errorf("note body missing guest name: %q", body)
	}

	// Give Mockoon a moment in case a second flush was scheduled by mistake.
	time.Sleep(800 * time.Millisecond)

	if n := countSendMessage(t, mock.logPath); n != 1 {
		t.Errorf("expected exactly one send-message call (burst merged into one turn), saw %d", n)
	}
	if got := classifyCalls.Load(); got != 1 {
		t.Errorf("expected classifier called exactly once, saw %d", got)
	}
}

// burstClassificationJSON treats the merged turn as Y6 (availability).
func burstClassificationJSON() string {
	return `{
  "primary_code": "Y6",
  "secondary_code": null,
  "confidence": 0.88,
  "risk_flag": false,
  "risk_reason": "",
  "next_action": "generate_reply",
  "reasoning": "Guest is building up a party-size + dates + total question over multiple short messages.",
  "extracted_entities": {
    "check_in": "2026-04-24",
    "check_out": "2026-04-26",
    "guest_count": 4,
    "pets": null,
    "vehicles": null,
    "listing_hint": null,
    "additional": []
  }
}`
}

func burstReplyJSON() string {
	return `{
  "body": "Hi Sarah — you're asking about Apr 24–26 for 4 adults, a quick weekend. Our Soho 2BR sleeps 4 comfortably with self-check-in. Those dates are open and the total is $480 for 2 nights, taxes included. The courtyard-side bedroom is the quietest sleep most guests report in Manhattan. Want me to hold the dates while you decide?",
  "closer_beats": {
    "clarify": true,
    "label": true,
    "overview": true,
    "sell_certainty": true,
    "explain": true,
    "request": true
  },
  "confidence": 0.86
}`
}
