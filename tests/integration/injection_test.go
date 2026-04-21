//go:build integration

package integration_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
)

// TestPromptInjectionShortCircuitsPipeline exercises the input-side guardrail:
// a guest message carrying "ignore previous instructions" and a role marker
// must be recorded as prompt_injection_suspected, never reach the LLM, and
// never produce an outbound send-message. The fake LLM fails the test on any
// call — classify must not fire when the detector intercepts the turn.
func TestPromptInjectionShortCircuitsPipeline(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var llmCalls atomic.Int32
	llm := startFakeLLM(t, func(_ openAIRequest) openAIResponse {
		llmCalls.Add(1)
		// Still return something so a latent bug doesn't hang; the assertion
		// below will catch the unintended call.
		return chatReplyJSON(happyClassificationJSON())
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/injection_attempt.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one escalation, got %d: %+v", len(recs), recs)
	}
	if got := recs[0].Reason; got != promptsafety.ReasonPromptInjection {
		t.Errorf("reason: got %q, want %q", got, promptsafety.ReasonPromptInjection)
	}
	if len(recs[0].Detail) == 0 {
		t.Error("detail should carry the matched pattern name for audit")
	}
	if n := llmCalls.Load(); n != 0 {
		t.Errorf("detector must short-circuit before any LLM call; saw %d", n)
	}
	if n := countSendMessage(t, mock.logPath); n != 0 {
		t.Errorf("no outbound send-message on injection turn; saw %d", n)
	}
}
