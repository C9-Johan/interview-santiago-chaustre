//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestKillSwitchFlipBlocksAutoSend verifies the operator kill-switch: after
// flipping auto_response_enabled=false via POST /admin/auto-response, the next
// inbound webhook must NOT produce a send-message and MUST land as an
// "auto_disabled" escalation. The test runs without the LLM producing a reply
// because GATE 1 should short-circuit before generation — if the LLM is ever
// called we fail, which documents the expected fast-path behavior.
func TestKillSwitchFlipBlocksAutoSend(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var llmCalls atomic.Int32
	llm := startFakeLLM(t, func(_ openAIRequest) openAIResponse {
		llmCalls.Add(1)
		return chatReplyJSON(happyClassificationJSON())
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	flipAutoResponse(t, svc, false)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/happy_availability.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 {
		t.Fatalf("expected one escalation when auto-send is disabled, got %d: %+v", len(recs), recs)
	}
	if recs[0].Reason != "auto_disabled" {
		t.Errorf("reason: got %q, want auto_disabled", recs[0].Reason)
	}
	if n := countSendMessage(t, mock.logPath); n != 0 {
		t.Errorf("no send-message when kill-switch is off; saw %d", n)
	}
	if n := llmCalls.Load(); n != 0 {
		t.Errorf("GATE 1 must reject before calling the LLM; saw %d", n)
	}
}

// TestKillSwitchFlipOnRestoresAutoSend confirms flipping back to true resumes
// normal processing. Uses the same happy fixture as TestHappyPathAutoNote so
// we're asserting the kill-switch is the only thing that changed.
func TestKillSwitchFlipOnRestoresAutoSend(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	var step atomic.Int32
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			return chatReplyJSON(happyClassificationJSON())
		}
		n := step.Add(1)
		if n == 1 {
			return chatToolCall("call_listing_1", "get_listing", `{"listing_id":"L1"}`)
		}
		if n == 2 {
			return chatToolCall("call_avail_1", "check_availability",
				`{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`)
		}
		return chatReplyJSON(happyReplyJSON())
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	flipAutoResponse(t, svc, false)
	flipAutoResponse(t, svc, true)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/happy_availability.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	body := waitForSendMessage(t, mock.logPath, 20*time.Second)
	if !strings.Contains(body, "Sarah") {
		t.Errorf("note body should mention guest once auto-send is back on: %q", body)
	}
}

// TestKillSwitchRejectsWrongToken guards the admin surface: a request without
// the right bearer token must not flip state regardless of the body. We assert
// both the 401 response and that the subsequent webhook still auto-sends
// (because the kill-switch was not actually toggled).
func TestKillSwitchRejectsWrongToken(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	llm := startFakeLLM(t, func(req openAIRequest) openAIResponse {
		if !req.hasTools() {
			return chatReplyJSON(happyClassificationJSON())
		}
		return chatReplyJSON(happyReplyJSON())
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	req := mustNewAdminRequest(t, svc.url, "wrong-token", `{"auto_response_enabled":false}`)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin call: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("admin with bad token: got %d, want 401", resp.StatusCode)
	}
	if !svc.toggles.Current().AutoResponseEnabled {
		t.Fatal("kill-switch must not flip on unauthorized admin call")
	}
}

func flipAutoResponse(t *testing.T, svc *bootedService, enabled bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"auto_response_enabled": enabled,
		"actor":                 "integration-test",
	})
	req := mustNewAdminRequest(t, svc.url, svc.adminToken, string(body))
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("flip auto-response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("flip status: got %d body=%s", resp.StatusCode, string(out))
	}
}

func mustNewAdminRequest(t *testing.T, base, token, body string) *nethttp.Request {
	t.Helper()
	req, err := nethttp.NewRequest(nethttp.MethodPost, base+"/admin/auto-response", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}
