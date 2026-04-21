//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/trackconversion"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

// TestConversionFlowMarksAndConverts exercises the G9 conversion pipeline:
//
//  1. Message webhook arrives, orchestrator auto-sends → tracker.MarkManaged
//     fires for the reservation and the managed counter ticks.
//  2. reservation.updated webhook arrives with status=confirmed →
//     tracker.ReservationUpdated fires, RecordConversion runs, and the
//     converted counter ticks.
//
// The store and metrics are in-process (no mongo/redis) so the test stays
// hermetic; the contract they satisfy is identical to the production path.
func TestConversionFlowMarksAndConverts(t *testing.T) {
	skipIfNoMockoon(t)

	mock := startMockoon(t)

	var step atomic.Int32
	fakeLLM := startFakeLLM(t, func(req openAIRequest) openAIResponse {
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

	store := newMemConversions()
	metrics := &countingMetrics{}

	svc := bootConversionService(t, mock.baseURL, fakeLLM.URL(), store, metrics)

	resp := postSignedWebhook(t, svc.url, "fixtures/webhooks/happy_availability.json")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("message webhook status: got %d, want 202", resp.StatusCode)
	}
	waitForSendMessage(t, mock.logPath, 20*time.Second)

	if got := metrics.managed.Load(); got != 1 {
		t.Fatalf("managed counter = %d, want 1", got)
	}
	if _, ok := store.get("res_test_001"); !ok {
		t.Fatalf("reservation res_test_001 not marked managed")
	}

	rresp := postReservationUpdated(t, svc.url, "res_test_001", "confirmed")
	_ = rresp.Body.Close()
	if rresp.StatusCode != 202 {
		t.Fatalf("reservation.updated status: got %d, want 202", rresp.StatusCode)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if metrics.converted.Load() == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := metrics.converted.Load(); got != 1 {
		t.Fatalf("converted counter = %d, want 1", got)
	}
	rec, _ := store.get("res_test_001")
	if rec.ConvertedAt == nil {
		t.Fatalf("converted_at not set on reservation record")
	}
	if rec.Status != trackconversion.StatusConfirmed {
		t.Fatalf("status = %q, want %q", rec.Status, trackconversion.StatusConfirmed)
	}
}

// TestReservationUpdatedIgnoresUntrackedReservation confirms the webhook is
// a no-op when the reservation was never bot-managed.
func TestReservationUpdatedIgnoresUntrackedReservation(t *testing.T) {
	store := newMemConversions()
	metrics := &countingMetrics{}
	svc := bootConversionService(t, "http://127.0.0.1:65535", "http://127.0.0.1:65535", store, metrics)

	resp := postReservationUpdated(t, svc.url, "unknown_reservation", "confirmed")
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if got := metrics.converted.Load(); got != 0 {
		t.Fatalf("converted counter incremented for untracked reservation: %d", got)
	}
}

type conversionBoot struct {
	url string
}

func bootConversionService(
	t *testing.T,
	guestyBaseURL, llmBaseURL string,
	store *memConversions,
	metrics *countingMetrics,
) *conversionBoot {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	gclient := guesty.NewClient(guestyBaseURL, "dev-token", 5*time.Second, 1)
	llmClient := llm.NewClient(llmBaseURL, "fake-key")

	classifier, err := classify.New(llmClient, "fake-model", 10*time.Second)
	if err != nil {
		t.Fatalf("classify.New: %v", err)
	}
	generator := generatereply.New(llmClient, gclient, "fake-model", 10*time.Second, 4)

	tracker := trackconversion.New(store, metrics, log)
	idempotency := memstore.NewIdempotency()
	memory := memstore.NewConversationMemory()
	classes := &nopClassifications{}
	escRing := memstore.NewEscalationRing(100, nil)

	orch := processinquiry.New(processinquiry.Deps{
		Classifier:      classifier,
		Generator:       generator,
		Guesty:          gclient,
		Idempotency:     idempotency,
		Escalations:     escRing,
		Memory:          memory,
		Classifications: classes,
		Conversions:     tracker,
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Log:             log,
	})

	clk := clock.NewReal()
	flush := func(ctx context.Context, turn domain.Turn) {
		conv, err := gclient.GetConversation(ctx, string(turn.Key))
		if err != nil {
			t.Logf("flush get conversation: %v", err)
			return
		}
		orch.Run(ctx, processinquiry.Input{
			Turn:         turn,
			Conversation: conv,
			ListingID:    defaultListingID,
			Now:          time.Now().UTC(),
		})
	}
	deb := debouncer.NewTimed(50*time.Millisecond, 200*time.Millisecond, clk, flush)

	handler := transporthttp.NewHandler(transporthttp.Handler{
		Webhooks:         nopWebhooks{},
		EscalationsStore: escRing,
		Idempotency:      idempotency,
		Resolver:         rawIDResolver{},
		Debouncer:        deb,
		SvixSecret:       webhookSecret,
		SvixMaxDrift:     5 * time.Minute,
		Log:              log,
	})
	rh := &transporthttp.ReservationHandler{
		Tracker:      tracker,
		SvixSecret:   webhookSecret,
		SvixMaxDrift: 5 * time.Minute,
		Log:          log,
		Now:          func() time.Time { return time.Now().UTC() },
	}
	router := transporthttp.NewRouter(handler, rh, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	t.Cleanup(deb.Stop)
	return &conversionBoot{url: srv.URL}
}

func postReservationUpdated(t *testing.T, svcURL, reservationID, status string) *nethttp.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"event":         "reservation.updated",
		"reservationId": reservationID,
		"status":        status,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	id := "res-" + ts + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sig := reservationSign(webhookSecret, id, ts, body)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := nethttp.NewRequestWithContext(
		ctx, nethttp.MethodPost,
		svcURL+"/webhooks/guesty/reservation-updated", bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST reservation: %v", err)
	}
	return resp
}

func reservationSign(secret, id, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// memConversions is a tiny in-process ConversionStore used only by this file.
type memConversions struct {
	mu      sync.Mutex
	records map[string]domain.ManagedReservation
}

func newMemConversions() *memConversions {
	return &memConversions{records: make(map[string]domain.ManagedReservation)}
}

func (m *memConversions) MarkManaged(_ context.Context, r domain.ManagedReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[r.ReservationID] = r
	return nil
}

func (m *memConversions) GetManaged(_ context.Context, id string) (domain.ManagedReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return domain.ManagedReservation{}, memConversionsNotFound
	}
	return r, nil
}

func (m *memConversions) RecordConversion(_ context.Context, id, status string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return memConversionsNotFound
	}
	r.Status = status
	r.ConvertedAt = &at
	m.records[id] = r
	return nil
}

func (m *memConversions) List(_ context.Context, _ int) ([]domain.ManagedReservation, error) {
	return nil, nil
}

func (m *memConversions) get(id string) (domain.ManagedReservation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	return r, ok
}

var _ repository.ConversionStore = (*memConversions)(nil)

var memConversionsNotFound = memConversionsErr("record not found")

type memConversionsErr string

func (e memConversionsErr) Error() string { return string(e) }

type countingMetrics struct {
	managed   atomic.Int32
	converted atomic.Int32
}

func (c *countingMetrics) RecordManaged(_ context.Context, _, _ string)   { c.managed.Add(1) }
func (c *countingMetrics) RecordConverted(_ context.Context, _, _ string) { c.converted.Add(1) }
