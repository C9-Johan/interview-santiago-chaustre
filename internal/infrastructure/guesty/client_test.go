package guesty_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
)

const listingBody = `{
  "_id":"L1",
  "title":"Soho 2BR",
  "bedrooms":2,"beds":3,"accommodates":4,
  "amenities":["wifi","kitchen"],
  "houseRules":"No parties.\nQuiet hours 10pm-8am.",
  "prices":{"basePrice":220,"cleaningFee":40,"currency":"USD"},
  "address":{"neighborhood":"Soho"}
}`

func TestClientGetListing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/L1" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(listingBody))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	got, err := c.GetListing(context.Background(), "L1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "L1" || got.MaxGuests != 4 || got.Neighborhood != "Soho" {
		t.Fatalf("got %+v", got)
	}
	if got.Title != "Soho 2BR" || got.BasePrice != 220 {
		t.Fatalf("title/price mismatch: %+v", got)
	}
	if len(got.HouseRules) != 2 || got.HouseRules[0] != "No parties." {
		t.Fatalf("house rules split mismatch: %+v", got.HouseRules)
	}
}

func TestClientCheckAvailability(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const wantPath = "/availability-pricing/api/calendar/listings/L1"
		if r.URL.Path != wantPath {
			t.Errorf("path %s, want %s", r.URL.Path, wantPath)
		}
		q := r.URL.Query()
		// endDate inclusive: checkOut 2026-04-26 → endDate 2026-04-25.
		if q.Get("startDate") != "2026-04-24" || q.Get("endDate") != "2026-04-25" {
			t.Errorf("query %v", q)
		}
		_, _ = w.Write([]byte(`{"status":200,"data":{"days":[
			{"date":"2026-04-24","price":240,"currency":"USD","status":"available"},
			{"date":"2026-04-25","price":240,"currency":"USD","status":"available"}
		]},"message":"OK"}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	from := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	got, err := c.CheckAvailability(context.Background(), "L1", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available || got.Nights != 2 || got.TotalUSD != 480 {
		t.Fatalf("got %+v", got)
	}
}

func TestClientCheckAvailabilityBlocked(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":200,"data":{"days":[
			{"date":"2026-05-01","price":240,"currency":"USD","status":"available"},
			{"date":"2026-05-02","price":0,"currency":"USD","status":"booked"}
		]}}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	got, err := c.CheckAvailability(context.Background(), "L1", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Available {
		t.Fatalf("should be unavailable when any day is not 'available': %+v", got)
	}
}

func TestClientCreateReservationHold(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reservations" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"_id":"res_1","status":"reserved","checkIn":"2026-04-24T15:00:00Z","checkOut":"2026-04-26T11:00:00Z","confirmationCode":"HOLD1"}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	res, err := c.CreateReservation(context.Background(), domain.ReservationHoldInput{
		ListingID:  "L1",
		CheckIn:    time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC),
		GuestCount: 4,
		Status:     domain.ReservationReserved,
		GuestName:  "Sarah",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "res_1" || res.ConfirmationCode != "HOLD1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if gotBody["listingId"] != "L1" {
		t.Fatalf("listingId missing or wrong: %+v", gotBody)
	}
	if gotBody["status"] != "reserved" {
		t.Fatalf("status not propagated: %+v", gotBody)
	}
	if gotBody["checkInDateLocalized"] != "2026-04-24" {
		t.Fatalf("check-in date wrong format: %+v", gotBody)
	}
	guest, _ := gotBody["guest"].(map[string]any)
	if guest == nil || guest["fullName"] != "Sarah" {
		t.Fatalf("guest object not populated when no GuestID: %+v", gotBody)
	}
}

func TestClientCreateReservationRejectsMissingFields(t *testing.T) {
	t.Parallel()
	c := guesty.NewClient("http://unused", "dev", time.Second, 0)
	if _, err := c.CreateReservation(context.Background(), domain.ReservationHoldInput{}); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := c.CreateReservation(context.Background(), domain.ReservationHoldInput{
		ListingID: "L1",
	}); err == nil {
		t.Fatal("expected error for missing check-in/out")
	}
}

func TestClientPostNote(t *testing.T) {
	t.Parallel()
	var gotBody struct {
		Body string `json:"body"`
		Type string `json:"type"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const wantPath = "/communication/conversations/C1/send-message"
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	if err := c.PostNote(context.Background(), "C1", "hello"); err != nil {
		t.Fatal(err)
	}
	if gotBody.Body != "hello" || gotBody.Type != "note" {
		t.Fatalf("payload %+v", gotBody)
	}
}

const convBody = `{
  "_id":"C1","guestId":"G1","language":"en",
  "integration":{"platform":"airbnb2"},
  "meta":{"guestName":"Sarah","reservations":[
    {"_id":"R1","checkIn":"2026-04-24T22:00:00Z","checkOut":"2026-04-26T16:00:00Z","confirmationCode":"CODE1","listingId":"L1"}
  ]}
}`

const postsBody = `{
  "results":[
    {"_id":"P1","postId":"P1","body":"hi","createdAt":"2026-04-20T14:31:09Z","type":"fromGuest","module":"airbnb2"}
  ],
  "limit":25,"skip":0,"count":1
}`

func TestClientGetConversation(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/communication/conversations/C1":
			_, _ = w.Write([]byte(convBody))
		case "/communication/conversations/C1/posts":
			_, _ = w.Write([]byte(postsBody))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	got, err := c.GetConversation(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RawID != "C1" || got.GuestName != "Sarah" || got.Integration.Platform != "airbnb2" {
		t.Fatalf("conversation %+v", got)
	}
	if len(got.Reservations) != 1 || got.Reservations[0].ConfirmationCode != "CODE1" {
		t.Fatalf("reservations %+v", got.Reservations)
	}
	if got.Reservations[0].ListingID != "L1" {
		t.Fatalf("reservation listing id %+v", got.Reservations)
	}
	if len(got.Thread) != 1 || got.Thread[0].PostID != "P1" {
		t.Fatalf("thread %+v", got.Thread)
	}
}

func TestClientGetConversationHistory(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const wantPath = "/communication/conversations/C1/posts"
		if r.URL.Path != wantPath {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit query %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(postsBody))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	got, err := c.GetConversationHistory(context.Background(), "C1", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PostID != "P1" {
		t.Fatalf("history %+v", got)
	}
}

func TestClientRetriesOn5xx(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"_id":"L1"}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 3)
	_, err := c.GetListing(context.Background(), "L1")
	if err != nil {
		t.Fatalf("should succeed after retries: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls: got %d, want 3", calls.Load())
	}
}

func TestClientTransportErrorWrappedThroughExhaustion(t *testing.T) {
	t.Parallel()
	// Port 1 is reserved (tcpmux) and unbound on every normal host; dialing
	// it fails at the transport layer with ECONNREFUSED, which is exactly
	// the condition we want to surface through retry exhaustion.
	c := guesty.NewClient("http://127.0.0.1:1", "dev", 200*time.Millisecond, 1)
	_, err := c.GetListing(context.Background(), "L1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, guesty.ErrRetriesExhausted) {
		t.Fatalf("errors.Is ErrRetriesExhausted = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") &&
		!strings.Contains(err.Error(), "connect: ") {
		t.Fatalf("error does not surface transport cause: %v", err)
	}
}

func TestClientZeroRetriesReturnsImmediately(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	start := time.Now()
	_, err := c.GetListing(context.Background(), "L1")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, guesty.ErrRetriesExhausted) {
		t.Fatalf("errors.Is ErrRetriesExhausted = false; err = %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("retries=0 should not sleep; elapsed = %v", elapsed)
	}
	if !strings.Contains(err.Error(), "connection refused") &&
		!strings.Contains(err.Error(), "connect: ") {
		t.Fatalf("transport cause not wrapped: %v", err)
	}
}

func TestClientNoRetryOn4xx(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 3)
	_, err := c.GetListing(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error on 404")
	}
	if calls.Load() != 1 {
		t.Fatalf("404 should not retry; got %d calls", calls.Load())
	}
}

// TestCircuitBreakerTripsOnRepeatedFailure confirms the breaker opens after
// the configured failure threshold and subsequent calls fail fast with
// ErrCircuitOpen without touching the network.
func TestCircuitBreakerTripsOnRepeatedFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	br := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "guesty-test",
		MaxRequests: 1,
		Interval:    time.Minute,
		Timeout:     time.Minute,
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= 2 },
	})
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0, guesty.WithCircuitBreaker(br))

	// First two calls exhaust retries (retries=0) and surface ErrRetriesExhausted.
	for range 2 {
		_, err := c.GetListing(context.Background(), "L1")
		if err == nil {
			t.Fatal("expected error while breaker still closed")
		}
		if errors.Is(err, guesty.ErrCircuitOpen) {
			t.Fatalf("breaker opened too early: %v", err)
		}
	}
	hitsBefore := calls.Load()

	// Third call should short-circuit — breaker is open, no HTTP request fires.
	_, err := c.GetListing(context.Background(), "L1")
	if !errors.Is(err, guesty.ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if hits := calls.Load(); hits != hitsBefore {
		t.Fatalf("breaker did not prevent downstream call: hits %d -> %d", hitsBefore, hits)
	}
}

// TestCircuitBreakerNilAllowsUnboundedFailures confirms passing
// WithCircuitBreaker(nil) disables fail-fast — used by tests that need to
// exercise retry exhaustion repeatedly without breaker interference.
func TestCircuitBreakerNilAllowsUnboundedFailures(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := guesty.NewClient(srv.URL, "dev", time.Second, 0, guesty.WithCircuitBreaker(nil))
	for range 10 {
		_, err := c.GetListing(context.Background(), "L1")
		if errors.Is(err, guesty.ErrCircuitOpen) {
			t.Fatalf("nil breaker must never short-circuit: %v", err)
		}
	}
	if got := calls.Load(); got != 10 {
		t.Fatalf("want 10 downstream calls with breaker disabled, got %d", got)
	}
}
