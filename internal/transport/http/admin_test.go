package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

// fakeTogglesSource records flip calls so tests can assert wiring without
// standing up the full togglesource.Source.
type fakeTogglesSource struct {
	mu      sync.Mutex
	state   domain.Toggles
	flips   int
	actor   string
	enabled bool
}

func (f *fakeTogglesSource) Current() domain.Toggles {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeTogglesSource) SetAutoResponse(_ context.Context, enabled bool, actor string) (prev, now bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev = f.state.AutoResponseEnabled
	f.state.AutoResponseEnabled = enabled
	f.flips++
	f.actor = actor
	f.enabled = enabled
	return prev, f.state.AutoResponseEnabled
}

const testToken = "admin-secret"

func TestGetAutoResponseReturnsCurrentFlag(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{state: domain.Toggles{AutoResponseEnabled: true}}
	h := &transporthttp.AdminHandler{Source: src, Token: testToken}

	req := httptest.NewRequest("GET", "/admin/auto-response", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.GetAutoResponse(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var body map[string]any
	mustDecode(t, w.Body, &body)
	if body["auto_response_enabled"] != true {
		t.Errorf("body: %+v", body)
	}
}

func TestSetAutoResponseFlipsAndReturnsPrev(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{state: domain.Toggles{AutoResponseEnabled: true}}
	h := &transporthttp.AdminHandler{Source: src, Token: testToken}

	req := httptest.NewRequest("POST", "/admin/auto-response",
		strings.NewReader(`{"auto_response_enabled":false,"actor":"oncall"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.SetAutoResponse(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	mustDecode(t, w.Body, &body)
	if body["previous"] != true || body["auto_response_enabled"] != false {
		t.Errorf("body: %+v", body)
	}
	if body["actor"] != "oncall" {
		t.Errorf("actor echoed: %+v", body)
	}
	if src.flips != 1 || src.actor != "oncall" || src.enabled {
		t.Errorf("source state: flips=%d actor=%q enabled=%t", src.flips, src.actor, src.enabled)
	}
}

func TestSetAutoResponseDefaultsActorToUnknown(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{state: domain.Toggles{AutoResponseEnabled: true}}
	h := &transporthttp.AdminHandler{Source: src, Token: testToken}

	req := httptest.NewRequest("POST", "/admin/auto-response",
		strings.NewReader(`{"auto_response_enabled":false}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.SetAutoResponse(w, req)

	if src.actor != "unknown" {
		t.Errorf("actor: got %q, want %q", src.actor, "unknown")
	}
}

func TestAdminRequiresBearerToken(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{}
	h := &transporthttp.AdminHandler{Source: src, Token: testToken}

	req := httptest.NewRequest("GET", "/admin/auto-response", nil)
	// no auth
	w := httptest.NewRecorder()
	h.GetAutoResponse(w, req)
	if w.Code != 401 {
		t.Errorf("no auth header -> want 401, got %d", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/admin/auto-response", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	w2 := httptest.NewRecorder()
	h.GetAutoResponse(w2, req2)
	if w2.Code != 401 {
		t.Errorf("bad token -> want 401, got %d", w2.Code)
	}
}

func TestAdminDisabledWhenTokenEmpty(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{}
	h := &transporthttp.AdminHandler{Source: src, Token: ""}

	req := httptest.NewRequest("POST", "/admin/auto-response", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	h.SetAutoResponse(w, req)
	if w.Code != 503 {
		t.Errorf("empty token should 503, got %d", w.Code)
	}
	if src.flips != 0 {
		t.Error("source must not be mutated when admin is disabled")
	}
}

func TestSetAutoResponseRejectsBadJSON(t *testing.T) {
	t.Parallel()
	src := &fakeTogglesSource{}
	h := &transporthttp.AdminHandler{Source: src, Token: testToken}

	req := httptest.NewRequest("POST", "/admin/auto-response", strings.NewReader(`not-json`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.SetAutoResponse(w, req)
	if w.Code != 400 {
		t.Errorf("bad body -> want 400, got %d", w.Code)
	}
}

func mustDecode(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
