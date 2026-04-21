package filestore_test

import (
	"context"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
)

const guestAID = "guestA"

func TestConversationMemoryRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := filestore.NewConversationMemory(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	k := domain.ConversationKey("conv1")

	got, err := store.Get(ctx, k)
	if err != nil {
		t.Fatalf("get before update: %v", err)
	}
	if got.GuestID != "" {
		t.Fatalf("want zero record, got %+v", got)
	}

	now := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	if err := store.Update(ctx, k, func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = k
		r.GuestID = guestAID
		r.Platform = "airbnb2"
		r.LastSummary = "talked about parking"
		r.UpdatedAt = now
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err = store.Get(ctx, k)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.GuestID != guestAID || got.LastSummary != "talked about parking" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("updatedAt mismatch: want %v got %v", now, got.UpdatedAt)
	}
}

func TestConversationMemoryListByGuestRecentFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := filestore.NewConversationMemory(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	base := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)

	writeRecord := func(key string, at time.Time) {
		err := store.Update(ctx, domain.ConversationKey(key), func(r *domain.ConversationMemoryRecord) {
			r.ConversationKey = domain.ConversationKey(key)
			r.GuestID = guestAID
			r.UpdatedAt = at
		})
		if err != nil {
			t.Fatalf("update %s: %v", key, err)
		}
	}
	writeRecord("c1", base)
	writeRecord("c2", base.Add(time.Hour))
	writeRecord("c3", base.Add(2*time.Hour))
	// record for different guest must not appear
	err = store.Update(ctx, domain.ConversationKey("c4"), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = "c4"
		r.GuestID = "guestB"
		r.UpdatedAt = base.Add(3 * time.Hour)
	})
	if err != nil {
		t.Fatalf("update c4: %v", err)
	}

	list, err := store.ListByGuest(ctx, guestAID, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(list), list)
	}
	if list[0].ConversationKey != "c3" || list[1].ConversationKey != "c2" {
		t.Fatalf("want [c3 c2], got [%s %s]", list[0].ConversationKey, list[1].ConversationKey)
	}
}

func TestConversationMemoryHydrateAfterRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := filestore.NewConversationMemory(dir)
	if err != nil {
		t.Fatalf("new first: %v", err)
	}
	ctx := context.Background()
	k := domain.ConversationKey("convR")
	if err := first.Update(ctx, k, func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = k
		r.GuestID = "g1"
		r.LastSummary = "v1"
		r.UpdatedAt = time.Now().UTC()
	}); err != nil {
		t.Fatalf("update v1: %v", err)
	}
	if err := first.Update(ctx, k, func(r *domain.ConversationMemoryRecord) {
		r.LastSummary = "v2"
		r.UpdatedAt = time.Now().UTC()
	}); err != nil {
		t.Fatalf("update v2: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := filestore.NewConversationMemory(dir)
	if err != nil {
		t.Fatalf("new second: %v", err)
	}
	defer func() { _ = second.Close() }()
	got, err := second.Get(ctx, k)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.LastSummary != "v2" {
		t.Fatalf("want latest snapshot v2, got %q", got.LastSummary)
	}
	list, err := second.ListByGuest(ctx, "g1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ConversationKey != k {
		t.Fatalf("guest index not rehydrated: %+v", list)
	}
}
