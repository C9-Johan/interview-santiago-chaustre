package guesty_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
)

func TestClientGetListing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/L1" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"L1","title":"Soho 2BR","maxGuests":4,"amenities":["wifi"],"neighborhood":"Soho"}`))
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
	if got.Title != "Soho 2BR" {
		t.Fatalf("title mismatch: %q", got.Title)
	}
}

func TestClientCheckAvailability(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/availability" {
			t.Errorf("path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("listingId") != "L1" || q.Get("from") != "2026-04-24" || q.Get("to") != "2026-04-26" {
			t.Errorf("query %v", q)
		}
		_, _ = w.Write([]byte(`{"available":true,"nights":2,"total":480}`))
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

func TestClientPostNote(t *testing.T) {
	t.Parallel()
	var gotBody struct {
		Body string `json:"body"`
		Type string `json:"type"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/conversations/C1/messages" {
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

func TestClientGetConversation(t *testing.T) {
	t.Parallel()
	const payload = `{
		"_id":"C1","guestId":"G1","language":"en",
		"integration":{"platform":"airbnb2"},
		"meta":{"guestName":"Sarah","reservations":[
			{"_id":"R1","checkIn":"2026-04-24T22:00:00Z","checkOut":"2026-04-26T16:00:00Z","confirmationCode":"CODE1"}
		]},
		"thread":[{"postId":"P1","body":"hi","createdAt":"2026-04-20T14:31:09Z","type":"fromGuest","module":"airbnb2"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
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
	if len(got.Thread) != 1 || got.Thread[0].PostID != "P1" {
		t.Fatalf("thread %+v", got.Thread)
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
		_, _ = w.Write([]byte(`{"id":"L1"}`))
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
