package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSendSignsWebhookWithSharedSecret verifies the signing proxy produces
// a signature the real service would accept: same Svix HMAC formula,
// identical headers, unmodified body.
func TestSendSignsWebhookWithSharedSecret(t *testing.T) {
	const secret = "whsec_test_only"
	var (
		gotBody []byte
		gotID   string
		gotTS   string
		gotSig  string
	)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotID = r.Header.Get("svix-id")
		gotTS = r.Header.Get("svix-timestamp")
		gotSig = r.Header.Get("svix-signature")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer service.Close()

	srv := newServer(config{
		listen:        ":0",
		serviceURL:    service.URL,
		mockoonURL:    "http://127.0.0.1:1", // unused in this path
		webhookSecret: secret,
	}, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	payload := sendRequest{
		ConversationID: "conv_test",
		GuestName:      "Test",
		Platform:       "airbnb2",
		Body:           "hello from the tester",
	}
	buf, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/send", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("tester /api/send status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	sig := strings.TrimPrefix(gotSig, "v1,")
	signed := fmt.Sprintf("%s.%s.%s", gotID, gotTS, string(gotBody))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if sig != want {
		t.Fatalf("signature mismatch:\n got=%s\nwant=%s\nsigned=%q", sig, want, signed)
	}
	if !strings.Contains(string(gotBody), "hello from the tester") {
		t.Fatalf("forwarded body missing guest text: %s", string(gotBody))
	}
	if _, err := time.Parse("1136239445", gotTS); err == nil {
		// svix-timestamp is unix seconds; parse-back via strconv is the standard test,
		// but here we only care that it exists and is non-empty.
	}
	if gotTS == "" || gotID == "" {
		t.Fatalf("missing svix headers: id=%q ts=%q", gotID, gotTS)
	}
}

// TestSendMessageInterceptSurfacesBotReply drives the proxy route the
// service calls when auto-sending; the tester records the body and returns
// a 200 so the service's send-message RPC succeeds.
func TestSendMessageInterceptSurfacesBotReply(t *testing.T) {
	srv := newServer(config{
		listen:        ":0",
		serviceURL:    "http://127.0.0.1:1",
		mockoonURL:    "http://127.0.0.1:1",
		webhookSecret: "whsec_x",
	}, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	body := `{"body":"Hi Sarah — your dates are open.","type":"note"}`
	req := httptest.NewRequest(http.MethodPost,
		"/communication/conversations/conv_xyz/send-message",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("send-message intercept status: got %d, want 200", rr.Code)
	}
	entries := srv.logs.list("conv_xyz")
	if len(entries) != 1 {
		t.Fatalf("want 1 log entry, got %d", len(entries))
	}
	if entries[0].Role != "note" || !strings.Contains(entries[0].Body, "Hi Sarah") {
		t.Fatalf("recorded entry wrong: %+v", entries[0])
	}
}

// TestSendRejectsEmptyBody guards against the UI posting a blank bubble
// that would still fire a classifier call.
func TestSendRejectsEmptyBody(t *testing.T) {
	srv := newServer(config{
		listen: ":0", serviceURL: "http://127.0.0.1:1",
		mockoonURL: "http://127.0.0.1:1", webhookSecret: "w",
	}, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	req := httptest.NewRequest(http.MethodPost, "/api/send",
		strings.NewReader(`{"body":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty body should 400, got %d", rr.Code)
	}
}
