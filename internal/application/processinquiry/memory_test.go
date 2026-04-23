package processinquiry_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/qualifyreply"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type fakeSummarizer struct {
	mu      sync.Mutex
	calls   []processinquiry.SummarizeInput
	output  string
	err     error
	invoked int
}

func (f *fakeSummarizer) Summarize(_ context.Context, in processinquiry.SummarizeInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	f.invoked++
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

func emptyDeps(t *testing.T) processinquiry.Deps {
	t.Helper()
	return processinquiry.Deps{
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Memory:          newFakeMemory(),
		Idempotency:     newFakeIdempotency(),
		Escalations:     &fakeEscalations{},
		Classifications: newFakeClassifications(),
		Guesty:          &fakeGuesty{},
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// runCloseTurn drives the exported closeTurn path by invoking Run with a
// pre-classified input shape that hits the pre-generate escalation — we only
// need memory updates to fire, not the full auto-send loop.
func runCloseTurnViaEscalation(t *testing.T, deps processinquiry.Deps, in processinquiry.Input) *domain.ConversationMemoryRecord {
	t.Helper()
	// Y2 is always-escalate so the run exits after gate1 without a reply, but
	// applyMemoryUpdate still runs via closeTurn — exactly the path we want
	// under test.
	clsJSON := `{"primary_code":"Y2","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"escalate_human","reasoning":"admin"}`
	deps.Classifier = mustClassifier(t, clsJSON)
	processinquiry.New(deps).Run(context.Background(), in)
	mem, _ := deps.Memory.Get(context.Background(), in.Turn.Key)
	return &mem
}

func TestApplyMemoryUpdatePersistsThreadAndEntities(t *testing.T) {
	t.Parallel()
	deps := emptyDeps(t)
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 50, Keep: 20}
	in := validInput()

	got := runCloseTurnViaEscalation(t, deps, in)

	if len(got.Thread) != 1 {
		t.Fatalf("expected 1 thread entry after 1 turn, got %d: %+v", len(got.Thread), got.Thread)
	}
	if got.Thread[0].Body != "Open Fri-Sun for 4 adults?" {
		t.Fatalf("thread body mismatch: %q", got.Thread[0].Body)
	}
	if got.KnownEntities.CheckIn == nil || got.KnownEntities.GuestCount == nil {
		t.Fatalf("entities not merged into memory: %+v", got.KnownEntities)
	}
	if *got.KnownEntities.GuestCount != 4 {
		t.Fatalf("guest count = %d", *got.KnownEntities.GuestCount)
	}
}

func TestApplyMemoryUpdateFoldsOverflowIntoSummary(t *testing.T) {
	t.Parallel()
	summ := &fakeSummarizer{output: "The guest has been asking about dates and guest count."}
	deps := emptyDeps(t)
	deps.Summarizer = summ
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 3, Keep: 2}

	// Pre-seed 3 entries so the next turn's single message pushes len to 4
	// and triggers a fold: 4 > cap(3); split = 4 - keep(2) = 2 older entries.
	pre := []domain.Message{
		{PostID: "p0a", Body: "first", Role: domain.RoleGuest, CreatedAt: time.Now()},
		{PostID: "p0b", Body: "second", Role: domain.RoleHost, CreatedAt: time.Now()},
		{PostID: "p0c", Body: "third", Role: domain.RoleGuest, CreatedAt: time.Now()},
	}
	_ = deps.Memory.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = testConvID
		r.Thread = append(r.Thread, pre...)
	})

	got := runCloseTurnViaEscalation(t, deps, validInput())

	if summ.invoked != 1 {
		t.Fatalf("expected summarizer to run exactly once, got %d", summ.invoked)
	}
	if len(got.Thread) != 2 {
		t.Fatalf("expected thread trimmed to keep=2, got %d: %+v", len(got.Thread), got.Thread)
	}
	if got.LastSummary == "" {
		t.Fatalf("expected LastSummary to be set after fold, got empty")
	}
	if got.LastSummaryPostID != "p0b" {
		t.Fatalf("expected LastSummaryPostID to be the last folded entry (p0b), got %q", got.LastSummaryPostID)
	}
	if len(summ.calls[0].OlderEntries) != 2 {
		t.Fatalf("summarizer should receive 2 older entries, got %d", len(summ.calls[0].OlderEntries))
	}
}

func TestApplyMemoryUpdateTruncatesWhenNoSummarizer(t *testing.T) {
	t.Parallel()
	deps := emptyDeps(t)
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 3, Keep: 2}

	pre := []domain.Message{
		{PostID: "p0a", Body: "first", Role: domain.RoleGuest, CreatedAt: time.Now()},
		{PostID: "p0b", Body: "second", Role: domain.RoleHost, CreatedAt: time.Now()},
		{PostID: "p0c", Body: "third", Role: domain.RoleGuest, CreatedAt: time.Now()},
	}
	_ = deps.Memory.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = testConvID
		r.Thread = append(r.Thread, pre...)
	})

	got := runCloseTurnViaEscalation(t, deps, validInput())

	if len(got.Thread) != 2 {
		t.Fatalf("expected truncation to keep=2, got %d", len(got.Thread))
	}
	if got.LastSummary != "" {
		t.Fatal("no summarizer wired — LastSummary must stay empty")
	}
}

func TestApplyMemoryUpdateSummarizerFailureTruncatesGracefully(t *testing.T) {
	t.Parallel()
	summ := &fakeSummarizer{err: errors.New("boom")}
	deps := emptyDeps(t)
	deps.Summarizer = summ
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 3, Keep: 2}

	pre := []domain.Message{
		{PostID: "p0a", Body: "a", Role: domain.RoleGuest, CreatedAt: time.Now()},
		{PostID: "p0b", Body: "b", Role: domain.RoleHost, CreatedAt: time.Now()},
		{PostID: "p0c", Body: "c", Role: domain.RoleGuest, CreatedAt: time.Now()},
	}
	_ = deps.Memory.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = testConvID
		r.Thread = append(r.Thread, pre...)
	})

	got := runCloseTurnViaEscalation(t, deps, validInput())

	if got.LastSummary != "" {
		t.Fatalf("summarizer failed — LastSummary must stay empty, got %q", got.LastSummary)
	}
	if len(got.Thread) != 2 {
		t.Fatalf("expected fallback truncation to keep=2, got %d", len(got.Thread))
	}
}

func TestSaturationGuardEscalatesOnRepeatX1(t *testing.T) {
	t.Parallel()
	deps := emptyDeps(t)
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 50, Keep: 20}
	clsJSON := `{"primary_code":"X1","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"qualify_question","reasoning":"vague"}`
	deps.Classifier = mustClassifier(t, clsJSON)
	// Qualifier that would otherwise run — the guard must bypass it.
	deps.Qualifier = &fakeQualifier{t: t}

	// Pre-seed memory with all three saturation-critical entities.
	checkIn := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	checkOut := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	guests := 4
	_ = deps.Memory.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = testConvID
		r.KnownEntities = domain.ExtractedEntities{CheckIn: &checkIn, CheckOut: &checkOut, GuestCount: &guests}
	})

	in := validInput()
	in.Turn.Messages = []domain.Message{{PostID: "post_2", Body: "yes please", Role: domain.RoleGuest, CreatedAt: time.Now()}}
	in.Turn.LastPostID = "post_2"
	processinquiry.New(deps).Run(context.Background(), in)

	esc := deps.Escalations.(*fakeEscalations)
	if len(esc.records) != 1 {
		t.Fatalf("expected 1 escalation from saturation guard, got %d", len(esc.records))
	}
	if !strings.Contains(esc.records[0].Reason, "qualifier_saturated") {
		t.Fatalf("expected qualifier_saturated reason, got %q", esc.records[0].Reason)
	}
}

// fakeQualifier asserts Generate is never called — the saturation guard must
// short-circuit before the qualifier LLM roundtrip.
type fakeQualifier struct{ t *testing.T }

func (f *fakeQualifier) Generate(_ context.Context, _ qualifyreply.Input) (domain.Reply, error) {
	f.t.Fatal("qualifier must not be invoked when saturation guard fires")
	return domain.Reply{}, nil
}
