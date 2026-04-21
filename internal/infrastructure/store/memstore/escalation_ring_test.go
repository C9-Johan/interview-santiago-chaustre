package memstore_test

import (
	"context"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
)

type appenderCount struct{ n int }

func (a *appenderCount) Append(_ context.Context, _ domain.Escalation) error {
	a.n++
	return nil
}

func TestEscalationRing(t *testing.T) {
	t.Parallel()
	ap := &appenderCount{}
	r := memstore.NewEscalationRing(3, ap)
	for i := 0; i < 5; i++ {
		if err := r.Record(context.Background(), domain.Escalation{ID: string(rune('a' + i))}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	if ap.n != 5 {
		t.Fatalf("durable Append not called for each record: got %d", ap.n)
	}
	got, err := r.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 || got[0].ID != "e" || got[2].ID != "c" {
		t.Fatalf("ring state unexpected: %+v", got)
	}
}
