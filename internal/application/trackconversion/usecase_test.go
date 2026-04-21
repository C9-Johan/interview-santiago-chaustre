package trackconversion_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/trackconversion"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type stubStore struct {
	mu       sync.Mutex
	managed  map[string]domain.ManagedReservation
	recorded map[string]string
}

func newStubStore() *stubStore {
	return &stubStore{
		managed:  make(map[string]domain.ManagedReservation),
		recorded: make(map[string]string),
	}
}

func (s *stubStore) MarkManaged(_ context.Context, r domain.ManagedReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managed[r.ReservationID] = r
	return nil
}

func (s *stubStore) GetManaged(_ context.Context, id string) (domain.ManagedReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.managed[id]
	if !ok {
		return domain.ManagedReservation{}, errors.New("record not found")
	}
	return r, nil
}

func (s *stubStore) RecordConversion(_ context.Context, id, status string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.managed[id]
	if !ok {
		return errors.New("record not found")
	}
	r.Status = status
	r.ConvertedAt = &at
	s.managed[id] = r
	s.recorded[id] = status
	return nil
}

func (s *stubStore) List(_ context.Context, _ int) ([]domain.ManagedReservation, error) {
	return nil, nil
}

type stubMetrics struct {
	managed   int
	converted int
}

func (m *stubMetrics) RecordManaged(_ context.Context, _, _ string)   { m.managed++ }
func (m *stubMetrics) RecordConverted(_ context.Context, _, _ string) { m.converted++ }

func TestMarkManagedAndConvert(t *testing.T) {
	store := newStubStore()
	metrics := &stubMetrics{}
	uc := trackconversion.New(store, metrics, nil)
	ctx := context.Background()

	uc.MarkManaged(ctx, trackconversion.ManagedInput{
		ReservationID: "r1",
		Platform:      "airbnb2",
		PrimaryCode:   domain.G1,
	})
	if metrics.managed != 1 {
		t.Fatalf("managed counter = %d, want 1", metrics.managed)
	}
	if _, ok := store.managed["r1"]; !ok {
		t.Fatal("r1 not persisted")
	}

	uc.ReservationUpdated(ctx, trackconversion.UpdatedInput{ReservationID: "r1", Status: "confirmed"})
	if metrics.converted != 1 {
		t.Fatalf("converted counter = %d, want 1", metrics.converted)
	}
	if store.recorded["r1"] != "confirmed" {
		t.Fatalf("status = %q, want confirmed", store.recorded["r1"])
	}

	uc.ReservationUpdated(ctx, trackconversion.UpdatedInput{ReservationID: "r1", Status: "confirmed"})
	if metrics.converted != 1 {
		t.Fatalf("double convert leaked; counter = %d", metrics.converted)
	}
}

func TestReservationUpdatedIgnoresUnknown(t *testing.T) {
	store := newStubStore()
	metrics := &stubMetrics{}
	uc := trackconversion.New(store, metrics, nil)

	uc.ReservationUpdated(context.Background(), trackconversion.UpdatedInput{
		ReservationID: "unknown", Status: "confirmed",
	})
	if metrics.converted != 0 {
		t.Fatalf("converted counter incremented for unknown reservation")
	}
}

func TestReservationUpdatedSkipsNonConfirmed(t *testing.T) {
	store := newStubStore()
	_ = store.MarkManaged(context.Background(), domain.ManagedReservation{
		ReservationID: "r1", ManagedAt: time.Now(),
	})
	metrics := &stubMetrics{}
	uc := trackconversion.New(store, metrics, nil)

	uc.ReservationUpdated(context.Background(), trackconversion.UpdatedInput{
		ReservationID: "r1", Status: "canceled",
	})
	if metrics.converted != 0 {
		t.Fatalf("canceled should not convert")
	}
}
