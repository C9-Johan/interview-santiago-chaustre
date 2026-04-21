package http_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

// --- fakes ---

type fakeWebhooks struct {
	mu       sync.Mutex
	appended []repository.WebhookRecord
}

func (f *fakeWebhooks) Append(_ context.Context, r repository.WebhookRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appended = append(f.appended, r)
	return nil
}

func (f *fakeWebhooks) Get(_ context.Context, _ string) (repository.WebhookRecord, error) {
	return repository.WebhookRecord{}, errors.New("not implemented")
}

func (f *fakeWebhooks) Since(_ context.Context, _ time.Duration) ([]repository.WebhookRecord, error) {
	return nil, nil
}

type fakeEscalations struct {
	mu      sync.Mutex
	records []domain.Escalation
}

func (f *fakeEscalations) Record(_ context.Context, e domain.Escalation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, e)
	return nil
}

func (f *fakeEscalations) List(_ context.Context, _ int) ([]domain.Escalation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Escalation, len(f.records))
	copy(out, f.records)
	return out, nil
}

type fakeIdempotency struct {
	mu       sync.Mutex
	seen     map[string]bool
	claimErr error
}

func (f *fakeIdempotency) SeenOrClaim(_ context.Context, k domain.ConversationKey, postID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return false, f.claimErr
	}
	key := string(k) + "|" + postID
	if f.seen[key] {
		return true, nil
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.seen[key] = true
	return false, nil
}

func (f *fakeIdempotency) Complete(_ context.Context, _ domain.ConversationKey, _ string) error {
	return nil
}

type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, c domain.Conversation) (domain.ConversationKey, error) {
	return domain.ConversationKey(c.RawID), nil
}

type fakeDebouncer struct {
	mu         sync.Mutex
	pushed     []domain.Message
	cancelled  int
	cancelRole domain.Role
}

func (f *fakeDebouncer) Push(_ context.Context, _ domain.ConversationKey, msg domain.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushed = append(f.pushed, msg)
}

func (f *fakeDebouncer) CancelIfHostReplied(_ domain.ConversationKey, role domain.Role) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled++
	f.cancelRole = role
}

func (*fakeDebouncer) Stop() {}

// --- helpers ---

func signBody(t *testing.T, secret, id, ts string, body []byte) string {
	t.Helper()
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

type harness struct {
	h      *transporthttp.Handler
	wh     *fakeWebhooks
	esc    *fakeEscalations
	idem   *fakeIdempotency
	deb    *fakeDebouncer
	now    time.Time
	secret string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	h := &harness{
		wh:     &fakeWebhooks{},
		esc:    &fakeEscalations{},
		idem:   &fakeIdempotency{seen: map[string]bool{}},
		deb:    &fakeDebouncer{},
		now:    now,
		secret: "whsec",
	}
	h.h = transporthttp.NewHandler(transporthttp.Handler{
		Webhooks:         h.wh,
		EscalationsStore: h.esc,
		Idempotency:      h.idem,
		Resolver:         fakeResolver{},
		Debouncer:        h.deb,
		SvixSecret:       h.secret,
		SvixMaxDrift:     5 * time.Minute,
		Log:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:              func() time.Time { return now },
	})
	return h
}

func (h *harness) postWebhook(t *testing.T, body []byte, opts ...func(*nethttp.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(nethttp.MethodPost, "/webhooks/guesty/message-received", bytes.NewReader(body))
	ts := strconv.FormatInt(h.now.Unix(), 10)
	r.Header.Set("svix-id", "svix_1")
	r.Header.Set("svix-timestamp", ts)
	r.Header.Set("svix-signature", signBody(t, h.secret, "svix_1", ts, body))
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.h.Webhook(w, r)
	return w
}

const guestFixture = `{
  "event":"reservation.messageReceived",
  "reservationId":"r1",
  "message":{"postId":"p1","body":"open Fri-Sun for 4?","createdAt":"2026-04-20T14:31:09Z","type":"fromGuest","module":"airbnb2"},
  "conversation":{"_id":"c1","guestId":"g1","language":"en","status":"OPEN","integration":{"platform":"airbnb2"},"meta":{"guestName":"Sarah","reservations":[{"_id":"r1"}]},"thread":[]},
  "meta":{"eventId":"e1","messageId":"m1"}
}`

const hostFixture = `{
  "event":"reservation.messageReceived",
  "reservationId":"r1",
  "message":{"postId":"p2","body":"Hi Sarah","createdAt":"2026-04-20T14:32:09Z","type":"fromHost","module":"airbnb2"},
  "conversation":{"_id":"c1","guestId":"g1","language":"en","status":"OPEN","integration":{"platform":"airbnb2"},"meta":{"guestName":"Sarah","reservations":[{"_id":"r1"}]},"thread":[]},
  "meta":{"eventId":"e2","messageId":"m2"}
}`

const emptyFixture = `{
  "event":"reservation.messageReceived",
  "reservationId":"r1",
  "message":{"postId":"p3","body":"   ","createdAt":"2026-04-20T14:33:09Z","type":"fromGuest","module":"airbnb2"},
  "conversation":{"_id":"c1","guestId":"g1","language":"en","status":"OPEN","integration":{"platform":"airbnb2"},"meta":{"guestName":"Sarah"},"thread":[]},
  "meta":{"eventId":"e3","messageId":"m3"}
}`

// --- tests ---

func TestWebhookHappyPath202AndPush(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := h.postWebhook(t, []byte(guestFixture))
	if w.Code != nethttp.StatusAccepted {
		t.Fatalf("status: %d, body=%s", w.Code, w.Body.String())
	}
	if len(h.deb.pushed) != 1 || h.deb.pushed[0].PostID != "p1" {
		t.Fatalf("debouncer push: %+v", h.deb.pushed)
	}
	if len(h.wh.appended) != 1 || h.wh.appended[0].PostID != "p1" {
		t.Fatalf("webhook store append: %+v", h.wh.appended)
	}
	if len(h.esc.records) != 0 {
		t.Fatalf("no escalation expected, got %+v", h.esc.records)
	}
}

func TestWebhookInvalidSignatureReturns401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := []byte(guestFixture)
	r := httptest.NewRequest(nethttp.MethodPost, "/webhooks/guesty/message-received", bytes.NewReader(body))
	ts := strconv.FormatInt(h.now.Unix(), 10)
	r.Header.Set("svix-id", "svix_1")
	r.Header.Set("svix-timestamp", ts)
	r.Header.Set("svix-signature", "v1,AAAA")
	w := httptest.NewRecorder()
	h.h.Webhook(w, r)
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
	if len(h.deb.pushed) != 0 {
		t.Fatal("must not push on bad signature")
	}
	if len(h.wh.appended) != 0 {
		t.Fatal("must not persist on bad signature")
	}
}

func TestWebhookMissingHeadersReturns401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := httptest.NewRequest(nethttp.MethodPost, "/webhooks/guesty/message-received", bytes.NewReader([]byte(guestFixture)))
	// no svix headers
	w := httptest.NewRecorder()
	h.h.Webhook(w, r)
	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestWebhookBadJSONReturns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := h.postWebhook(t, []byte(`not json`))
	if w.Code != nethttp.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestWebhookHostSenderCancelsDebounce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := h.postWebhook(t, []byte(hostFixture))
	if w.Code != nethttp.StatusAccepted {
		t.Fatalf("got %d", w.Code)
	}
	if len(h.deb.pushed) != 0 {
		t.Fatal("host message must NOT be pushed to debouncer")
	}
	if h.deb.cancelled != 1 {
		t.Fatalf("debouncer cancel count: %d", h.deb.cancelled)
	}
	if h.deb.cancelRole != domain.RoleHost {
		t.Fatalf("role: %q", h.deb.cancelRole)
	}
	// Webhook still persisted for audit.
	if len(h.wh.appended) != 1 {
		t.Fatalf("append expected: %+v", h.wh.appended)
	}
}

func TestWebhookEmptyBodyEscalates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := h.postWebhook(t, []byte(emptyFixture))
	if w.Code != nethttp.StatusAccepted {
		t.Fatalf("got %d", w.Code)
	}
	if len(h.esc.records) != 1 {
		t.Fatalf("want 1 empty-body escalation, got %+v", h.esc.records)
	}
	if h.esc.records[0].Reason != "empty_body" {
		t.Fatalf("reason: %q", h.esc.records[0].Reason)
	}
	if len(h.deb.pushed) != 0 {
		t.Fatal("empty-body must not be pushed")
	}
}

func TestWebhookDuplicatePostIDReturns200(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// First post claims idempotency.
	w1 := h.postWebhook(t, []byte(guestFixture))
	if w1.Code != nethttp.StatusAccepted {
		t.Fatalf("first post: %d", w1.Code)
	}
	// Second post (same postID) should be a duplicate.
	w2 := h.postWebhook(t, []byte(guestFixture))
	if w2.Code != nethttp.StatusOK {
		t.Fatalf("second post: %d want 200", w2.Code)
	}
	if len(h.deb.pushed) != 1 {
		t.Fatalf("debouncer pushed %d times, want 1", len(h.deb.pushed))
	}
}

func TestHealthAnswersOK(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := httptest.NewRequest(nethttp.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.h.Health(w, r)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
}

func TestEscalationsEndpointReturnsList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_ = h.esc.Record(context.Background(), domain.Escalation{ID: "e1", Reason: "code_requires_human"})
	r := httptest.NewRequest(nethttp.MethodGet, "/escalations", nil)
	w := httptest.NewRecorder()
	h.h.Escalations(w, r)
	if w.Code != nethttp.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("code_requires_human")) {
		t.Fatalf("body: %s", w.Body.String())
	}
}
