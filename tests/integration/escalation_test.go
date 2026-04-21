//go:build integration

package integration_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// TestEscalationY2Refund confirms a Y2 (refund/trust) classification routes
// the turn to a human via GATE 1 and never fires an outbound send-message.
func TestEscalationY2Refund(t *testing.T) {
	runEscalationCase(t, escalationCase{
		fixture:           "fixtures/webhooks/esc_refund.json",
		classificationRaw: classificationJSON("Y2", 0.90, false, ""),
		wantReason:        "code_requires_human",
		wantDetail:        "Y2",
	})
}

// TestEscalationR1Discount confirms an R1 (haggle) classification routes to
// a human — Red price language dominates and must never auto-reply.
func TestEscalationR1Discount(t *testing.T) {
	runEscalationCase(t, escalationCase{
		fixture:           "fixtures/webhooks/esc_discount.json",
		classificationRaw: classificationJSON("R1", 0.85, false, ""),
		wantReason:        "code_requires_human",
		wantDetail:        "R1",
	})
}

// TestEscalationRiskFlagVenmo confirms a classifier-set risk_flag (off-platform
// payment request) routes to human even when the primary code is otherwise
// auto-eligible.
func TestEscalationRiskFlagVenmo(t *testing.T) {
	runEscalationCase(t, escalationCase{
		fixture:           "fixtures/webhooks/esc_venmo.json",
		classificationRaw: classificationJSON("G1", 0.88, true, "off_platform_payment"),
		wantReason:        "risk_flag",
		wantDetail:        "off_platform_payment",
	})
}

type escalationCase struct {
	fixture           string
	classificationRaw string
	wantReason        string
	wantDetail        string
}

func runEscalationCase(t *testing.T, c escalationCase) {
	t.Helper()
	skipIfNoMockoon(t)

	mock := startMockoon(t)
	llm := startFakeLLM(t, func(_ openAIRequest) openAIResponse {
		return chatReplyJSON(c.classificationRaw)
	})
	svc := bootService(t, mock.baseURL, llm.URL(), 50*time.Millisecond)

	resp := postSignedWebhook(t, svc.url, c.fixture)
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("webhook status: got %d, want 202", resp.StatusCode)
	}

	recs := waitForEscalation(t, svc.escalations, 5*time.Second)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one escalation, got %d: %+v", len(recs), recs)
	}
	got := recs[0]
	if got.Reason != c.wantReason {
		t.Errorf("escalation reason: got %q, want %q (detail=%v)", got.Reason, c.wantReason, got.Detail)
	}
	if !detailContains(got.Detail, c.wantDetail) {
		t.Errorf("escalation detail missing %q: detail=%v", c.wantDetail, got.Detail)
	}
	if n := countSendMessage(t, mock.logPath); n != 0 {
		t.Errorf("expected zero send-message calls on escalation, saw %d", n)
	}
}

// waitForEscalation polls the escalation store until at least one record is
// visible, then returns the current list. Fails on timeout.
func waitForEscalation(t *testing.T, store repository.EscalationStore, timeout time.Duration) []escalationRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := store.List(context.Background(), 10)
		if err == nil && len(raw) > 0 {
			out := make([]escalationRecord, 0, len(raw))
			for i := range raw {
				out = append(out, escalationRecord{
					Reason: raw[i].Reason,
					Detail: raw[i].Detail,
				})
			}
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no escalation recorded within %s", timeout)
	return nil
}

// escalationRecord narrows domain.Escalation to the two fields these tests
// actually assert on, keeping the test table readable.
type escalationRecord struct {
	Reason string
	Detail []string
}

func detailContains(slice []string, s string) bool {
	for i := range slice {
		if slice[i] == s {
			return true
		}
	}
	return false
}

// classificationJSON produces a classifier JSON payload with a given code,
// confidence, and risk annotations. Entities are left null on purpose — GATE
// 1 never reads them.
func classificationJSON(code string, conf float64, riskFlag bool, riskReason string) string {
	risk := "false"
	if riskFlag {
		risk = "true"
	}
	reasonField := ""
	if riskReason != "" {
		reasonField = `"risk_reason": "` + riskReason + `",` + "\n  "
	}
	return `{
  "primary_code": "` + code + `",
  "secondary_code": null,
  "confidence": ` + strconv.FormatFloat(conf, 'f', 2, 64) + `,
  "risk_flag": ` + risk + `,
  ` + reasonField + `"next_action": "escalate_human",
  "reasoning": "Escalation path integration fixture.",
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
