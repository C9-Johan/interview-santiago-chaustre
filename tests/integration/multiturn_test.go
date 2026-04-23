//go:build integration

package integration_test

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMultiTurnYesPleaseDoesNotEscalateAfterSuccessfulHold(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var step atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			if requestHas(req, "yes please") {
				return chatReplyJSON(classificationGenerateJSON("Y1", 0.86, "follow-up question"))
			}
			return chatReplyJSON(classificationGenerateJSON("Y6", 0.92, "availability ask"))
		}
		if requestHas(req, "yes please") {
			return chatReplyJSON(followupReplyJSON("Great, your dates are already on hold. What else should I clarify before you book?"))
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("mt_listing_ok", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall("mt_avail_ok", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`)
		case 3:
			return chatToolCall("mt_hold_ok", "hold_reservation", `{"listing_id":"L1","check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4,"status":"inquiry"}`)
		default:
			return chatReplyJSON(offerHoldReplyJSON())
		}
	})

	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_yes_turn1.json")
	waitForSendMessageCount(t, mock.logPath, 1, 20*time.Second)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_yes_turn2.json")
	waitForSendMessageCount(t, mock.logPath, 2, 20*time.Second)
	assertNoEscalation(t, svc, 1500*time.Millisecond)
}

func TestMultiTurnLockRequestEscalatesAfterSuccessfulHold(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var step atomic.Int32
	var classifyCalls atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			classifyCalls.Add(1)
			if requestHas(req, "yes please") {
				return chatReplyJSON(classificationGenerateJSON("G1", 0.85, "follow-up acknowledgment"))
			}
			if requestHas(req, "lock them in") {
				t.Fatalf("turn 3 should short-circuit to escalation before classifier")
			}
			return chatReplyJSON(classificationGenerateJSON("Y6", 0.92, "availability ask"))
		}
		if requestHas(req, "yes please") {
			return chatReplyJSON(followupReplyJSON("Great, your dates are already on hold. If you want to lock them in, I can route this to an agent now."))
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("mt_listing_lock", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall("mt_avail_lock", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`)
		case 3:
			return chatToolCall("mt_hold_lock", "hold_reservation", `{"listing_id":"L1","check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4,"status":"inquiry"}`)
		default:
			return chatReplyJSON(offerHoldReplyJSON())
		}
	})

	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_yes_turn1.json")
	waitForSendMessageCount(t, mock.logPath, 1, 20*time.Second)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_yes_turn2.json")
	waitForSendMessageCount(t, mock.logPath, 2, 20*time.Second)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_yes_turn3_lock.json")
	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 || recs[0].Reason != "commitment_needs_human" {
		t.Fatalf("lock request must escalate, got %+v", recs)
	}
	if got := countSendMessage(t, mock.logPath); got != 2 {
		t.Fatalf("lock request must not post another note, got %d notes", got)
	}
	if got := classifyCalls.Load(); got != 2 {
		t.Fatalf("classifier should run for first two turns only, got %d", got)
	}
}

func TestMultiTurnDeferReservationStaysInAutoReplyFlow(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var step atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			if requestHas(req, "for now answer first") {
				return chatReplyJSON(classificationGenerateJSON("Y1", 0.84, "guest wants details first"))
			}
			return chatReplyJSON(classificationGenerateJSON("Y6", 0.91, "availability ask"))
		}
		if requestHas(req, "for now answer first") {
			return chatReplyJSON(followupReplyJSON("Absolutely. Ask anything first, and I will only reserve when you explicitly confirm."))
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("mt_listing_defer", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall("mt_avail_defer", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`)
		case 3:
			return chatToolCall("mt_hold_defer", "hold_reservation", `{"listing_id":"L1","check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4,"status":"inquiry"}`)
		default:
			return chatReplyJSON(offerHoldReplyJSON())
		}
	})

	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_defer_turn1.json")
	waitForSendMessageCount(t, mock.logPath, 1, 20*time.Second)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_defer_turn2.json")
	waitForSendMessageCount(t, mock.logPath, 2, 20*time.Second)
	assertNoEscalation(t, svc, 1500*time.Millisecond)
}

func TestMultiTurnYesPleaseEscalatesWhenPriorHoldFailed(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var step atomic.Int32
	var classifyCalls atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			classifyCalls.Add(1)
			return chatReplyJSON(classificationGenerateJSON("Y6", 0.90, "availability ask"))
		}
		n := step.Add(1)
		switch n {
		case 1:
			return chatToolCall("mt_listing_fail", "get_listing", `{"listing_id":"L1"}`)
		case 2:
			return chatToolCall("mt_avail_fail", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`)
		case 3:
			return chatToolCall("mt_hold_fail", "hold_reservation", `{"listing_id":"res_test_001","check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4,"status":"inquiry"}`)
		default:
			return chatReplyJSON(offerHoldReplyJSON())
		}
	})

	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_holdfail_turn1.json")
	waitForSendMessageCount(t, mock.logPath, 1, 20*time.Second)
	if got := classifyCalls.Load(); got != 1 {
		t.Fatalf("turn 1 should classify once, got %d", got)
	}

	postWebhook202(t, svc.url, "fixtures/webhooks/multiturn_holdfail_turn2.json")
	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 {
		t.Fatalf("expected one escalation, got %+v", recs)
	}
	if recs[0].Reason != "commitment_needs_human" {
		t.Fatalf("reason: got %q, want commitment_needs_human", recs[0].Reason)
	}
	if got := countSendMessage(t, mock.logPath); got != 1 {
		t.Fatalf("second turn must not auto-send after failed hold, got %d notes", got)
	}
	if got := classifyCalls.Load(); got != 1 {
		t.Fatalf("turn 2 should short-circuit before classifier, classify calls=%d", got)
	}
}

func postWebhook202(t *testing.T, svcURL, fixture string) {
	t.Helper()
	resp := postSignedWebhook(t, svcURL, fixture)
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook %s status: got %d, want 202", fixture, resp.StatusCode)
	}
}

func assertNoEscalation(t *testing.T, svc *bootedService, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		recs, err := svc.escalations.List(context.Background(), 5)
		if err == nil && len(recs) > 0 {
			t.Fatalf("unexpected escalation(s): %+v", recs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func requestHas(req openAIRequest, needle string) bool {
	lower := strings.ToLower(needle)
	for i := range req.Messages {
		if strings.Contains(strings.ToLower(req.Messages[i].Content), lower) {
			return true
		}
	}
	return false
}

func classificationGenerateJSON(code string, conf float64, reason string) string {
	return `{
  "primary_code": "` + code + `",
  "secondary_code": null,
  "confidence": ` + strconv.FormatFloat(conf, 'f', 2, 64) + `,
  "risk_flag": false,
  "risk_reason": "",
  "next_action": "generate_reply",
  "reasoning": "` + reason + `",
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

func offerHoldReplyJSON() string {
	return `{
  "body": "Yes, the Soho 2BR is open Apr 24-26 for 4 guests. The total is $480 for two nights and self check-in is available. Want me to hold the dates while you decide?",
  "closer_beats": {
    "clarify": true,
    "label": true,
    "overview": true,
    "sell_certainty": true,
    "explain": true,
    "request": true
  },
  "confidence": 0.91
}`
}

func followupReplyJSON(body string) string {
	return `{
  "body": "` + body + `",
  "closer_beats": {
    "clarify": true,
    "label": true,
    "overview": true,
    "sell_certainty": false,
    "explain": true,
    "request": true
  },
  "confidence": 0.9
}`
}
