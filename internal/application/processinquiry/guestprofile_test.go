package processinquiry_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestBuildGuestProfileEmpty(t *testing.T) {
	t.Parallel()
	if processinquiry.BuildGuestProfile(nil) != "" {
		t.Fatal("empty records must produce empty profile")
	}
	if processinquiry.BuildGuestProfile([]domain.ConversationMemoryRecord{}) != "" {
		t.Fatal("empty slice must produce empty profile")
	}
}

func TestBuildGuestProfileCountsAndReasons(t *testing.T) {
	t.Parallel()
	now := time.Now()
	records := []domain.ConversationMemoryRecord{
		{LastAutoSendAt: &now, EscalationReasons: []string{"code_requires_human"}},
		{LastEscalationAt: &now, EscalationReasons: []string{"code_requires_human", "risk_flag"}},
	}
	got := processinquiry.BuildGuestProfile(records)
	if !strings.Contains(got, "2 prior conversations") {
		t.Fatalf("missing count: %q", got)
	}
	if !strings.Contains(got, "1 auto-sent") || !strings.Contains(got, "1 escalated") {
		t.Fatalf("missing outcomes: %q", got)
	}
	if !strings.Contains(got, "code_requires_human") {
		t.Fatalf("missing top reason: %q", got)
	}
}

func TestBuildGuestProfilePetsCarriedForward(t *testing.T) {
	t.Parallel()
	hasPets := true
	records := []domain.ConversationMemoryRecord{
		{KnownEntities: domain.ExtractedEntities{Pets: &hasPets}},
	}
	got := processinquiry.BuildGuestProfile(records)
	if !strings.Contains(got, "pets=true") {
		t.Fatalf("expected pets carry-forward, got %q", got)
	}
}

func TestBuildGuestProfileTruncatedAtMax(t *testing.T) {
	t.Parallel()
	records := make([]domain.ConversationMemoryRecord, 0, 8)
	for i := range 8 {
		records = append(records, domain.ConversationMemoryRecord{
			EscalationReasons: []string{"reason_" + string(rune('a'+i)) + "_with_long_tail_to_force_growth"},
		})
	}
	got := processinquiry.BuildGuestProfile(records)
	if len(got) > 300 {
		t.Fatalf("profile exceeded 300 chars: %d", len(got))
	}
}
