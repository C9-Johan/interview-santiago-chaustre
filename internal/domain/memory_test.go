package domain_test

import (
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestAppendMessageDedupsByPostID(t *testing.T) {
	r := &domain.ConversationMemoryRecord{}
	r.AppendMessage(domain.Message{PostID: "p1", Body: "hi", Role: domain.RoleGuest})
	r.AppendMessage(domain.Message{PostID: "p1", Body: "hi again", Role: domain.RoleGuest})
	r.AppendMessage(domain.Message{PostID: "p2", Body: "next", Role: domain.RoleGuest})
	if len(r.Thread) != 2 {
		t.Fatalf("expected 2 entries after dedup, got %d: %+v", len(r.Thread), r.Thread)
	}
	if r.Thread[0].Body != "hi" {
		t.Fatalf("first entry should be original 'hi', got %q", r.Thread[0].Body)
	}
}

func TestAppendMessageKeepsEmptyPostIDDuplicates(t *testing.T) {
	r := &domain.ConversationMemoryRecord{}
	r.AppendMessage(domain.Message{Body: "synthesized", Role: domain.RoleHost})
	r.AppendMessage(domain.Message{Body: "synthesized", Role: domain.RoleHost})
	if len(r.Thread) != 2 {
		t.Fatalf("empty-PostID entries must not be deduped, got %d", len(r.Thread))
	}
}

func TestMergeEntitiesNewerNonNilWins(t *testing.T) {
	earlier := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	four := 4
	five := 5
	base := domain.ExtractedEntities{CheckIn: &earlier, GuestCount: &four}
	incoming := domain.ExtractedEntities{CheckIn: &later, GuestCount: &five}

	out := domain.MergeEntities(base, incoming)
	if !out.CheckIn.Equal(later) {
		t.Fatalf("CheckIn should have moved to later, got %v", out.CheckIn)
	}
	if *out.GuestCount != 5 {
		t.Fatalf("GuestCount should have moved to 5, got %d", *out.GuestCount)
	}
}

func TestMergeEntitiesNilIncomingPreservesBase(t *testing.T) {
	checkIn := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	four := 4
	base := domain.ExtractedEntities{CheckIn: &checkIn, GuestCount: &four}

	out := domain.MergeEntities(base, domain.ExtractedEntities{})
	if out.CheckIn == nil || !out.CheckIn.Equal(checkIn) {
		t.Fatalf("nil incoming must not clobber existing CheckIn")
	}
	if out.GuestCount == nil || *out.GuestCount != 4 {
		t.Fatalf("nil incoming must not clobber existing GuestCount")
	}
}

func TestMergeEntitiesAdditionalDedupByKey(t *testing.T) {
	base := domain.ExtractedEntities{Additional: []domain.Observation{
		{Key: "trip_occasion", Value: "wedding", ValueType: "string", Confidence: 0.7},
		{Key: "noise_sensitivity", Value: "true", ValueType: "bool", Confidence: 0.6},
	}}
	incoming := domain.ExtractedEntities{Additional: []domain.Observation{
		{Key: "trip_occasion", Value: "honeymoon", ValueType: "string", Confidence: 0.9},
		{Key: "group_type", Value: "couple", ValueType: "string", Confidence: 0.8},
	}}

	out := domain.MergeEntities(base, incoming)
	if len(out.Additional) != 3 {
		t.Fatalf("expected 3 observations after merge, got %d: %+v", len(out.Additional), out.Additional)
	}
	if out.Additional[0].Key != "trip_occasion" || out.Additional[0].Value != "honeymoon" {
		t.Fatalf("trip_occasion should be overwritten with newer value, got %+v", out.Additional[0])
	}
	if out.Additional[1].Key != "noise_sensitivity" {
		t.Fatalf("untouched observation should stay in place, got %+v", out.Additional[1])
	}
	if out.Additional[2].Key != "group_type" {
		t.Fatalf("new observation should be appended last, got %+v", out.Additional[2])
	}
}

func TestMergeEntitiesAdditionalSkipsEmptyKey(t *testing.T) {
	base := domain.ExtractedEntities{}
	incoming := domain.ExtractedEntities{Additional: []domain.Observation{
		{Key: "", Value: "ignored", ValueType: "string"},
		{Key: "ok", Value: "kept", ValueType: "string"},
	}}
	out := domain.MergeEntities(base, incoming)
	if len(out.Additional) != 1 || out.Additional[0].Key != "ok" {
		t.Fatalf("empty-key observation must be skipped, got %+v", out.Additional)
	}
}
