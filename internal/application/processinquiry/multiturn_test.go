package processinquiry_test

import (
	"context"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// TestSohoMultiTurnScenario walks the exact flow the user flagged:
//   - Turn 1: guest asks about availability. Bot classifies Y6, generates a
//     reply that says "Want me to hold it?" and auto-sends.
//   - Turn 2: guest says "Yes please do that for me". Memory carries the bot's
//     prior host message forward, the commitment guard detects the fabricated
//     commitment, and the orchestrator escalates instead of asking the guest
//     to repeat dates + guest count (the pre-fix regression).
func TestSohoMultiTurnScenarioEscalatesCommitment(t *testing.T) {
	t.Parallel()
	// Shared fakes — state persists across both turns exactly like a real
	// conversation would.
	g := &fakeGuesty{}
	esc := &fakeEscalations{}
	mem := newFakeMemory()
	idem := newFakeIdempotency()
	baseDeps := func() processinquiry.Deps {
		return processinquiry.Deps{
			Guesty:          g,
			Idempotency:     idem,
			Escalations:     esc,
			Memory:          mem,
			Classifications: newFakeClassifications(),
			Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
			MemoryLimits:    processinquiry.MemoryLimits{Cap: 50, Keep: 20},
			Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
			Log:             discardLogger(),
		}
	}

	// Turn 1: dates question → Y6 → generate reply with hold offer → auto-send.
	turn1JSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	replyWithHoldOffer := domain.Reply{
		Body:        "Yes, the Soho 2BR is available for April 24-26. Total is $480 for 2 nights. The courtyard bedroom is the quietest sleep in Manhattan. Want me to hold the dates while you decide?",
		Confidence:  0.85,
		CloserBeats: closerAll(),
	}
	d1 := baseDeps()
	d1.Classifier = mustClassifier(t, turn1JSON)
	d1.Generator = mustGenerator(t, replyWithHoldOffer, true)
	in1 := validInput()
	in1.Turn.LastPostID = "post_1"
	in1.Turn.Messages = []domain.Message{
		{PostID: "post_1", Body: "Is the Soho 2BR available April 24-26 for 4 adults?", Role: domain.RoleGuest, CreatedAt: time.Now().Add(-5 * time.Minute)},
	}
	processinquiry.New(d1).Run(context.Background(), in1)

	if len(g.postedNotes) != 1 {
		t.Fatalf("turn 1 should auto-send, got %d notes", len(g.postedNotes))
	}
	if len(esc.records) != 0 {
		t.Fatalf("turn 1 should not escalate, got %+v", esc.records)
	}

	// Memory should now hold the guest question + the bot's host reply,
	// and the extracted entities should be cached.
	memRec, _ := mem.Get(context.Background(), testConvID)
	if len(memRec.Thread) != 2 {
		t.Fatalf("memory thread should have 2 entries after turn 1, got %d: %+v", len(memRec.Thread), memRec.Thread)
	}
	if memRec.Thread[1].Role != domain.RoleHost {
		t.Fatalf("second memory entry should be the bot reply, got role %s", memRec.Thread[1].Role)
	}
	if memRec.KnownEntities.CheckIn == nil || memRec.KnownEntities.GuestCount == nil {
		t.Fatalf("turn 1 must populate KnownEntities, got %+v", memRec.KnownEntities)
	}

	// Turn 2: bare affirmative accepts the bot's hold offer. Because the
	// commitment guard fires pre-classification, we do NOT need a working
	// classifier/generator on this turn — the test also proves the guard
	// short-circuits before any LLM call.
	d2 := baseDeps()
	d2.Classifier = mustClassifierErr(t) // would fail if invoked
	d2.Generator = mustGenerator(t, domain.Reply{}, false)
	in2 := validInput()
	in2.Turn.LastPostID = "post_2"
	in2.Turn.Messages = []domain.Message{
		{PostID: "post_2", Body: "Yes please do that for me", Role: domain.RoleGuest, CreatedAt: time.Now()},
	}
	processinquiry.New(d2).Run(context.Background(), in2)

	if len(g.postedNotes) != 1 {
		t.Fatalf("turn 2 must NOT auto-send, got %d notes", len(g.postedNotes))
	}
	if len(esc.records) != 1 {
		t.Fatalf("turn 2 must escalate, got %d escalations", len(esc.records))
	}
	if esc.records[0].Reason != "commitment_needs_human" {
		t.Fatalf("turn 2 escalation reason = %q, want commitment_needs_human", esc.records[0].Reason)
	}
}

// TestEscalatedReplyDoesNotPoisonMemory locks in the rule that an
// undelivered reply (critic-rejected, validator-rejected, post-note failure)
// must not be appended to the memory thread. From the guest's perspective
// the bot was silent, so the next turn's classifier must see an unanswered
// guest question — not a phantom host message that makes a follow-up like
// "?" look like a vague continuation of a closed exchange. Regression for
// the conv_ui_002 incident where turn 1 escalated on critic_rejected and
// turn 2 ("?") classified X1 → qualifier_saturated → escalated again.
func TestEscalatedReplyDoesNotPoisonMemory(t *testing.T) {
	t.Parallel()
	g := &fakeGuesty{}
	esc := &fakeEscalations{}
	mem := newFakeMemory()
	idem := newFakeIdempotency()
	deps := processinquiry.Deps{
		Guesty:          g,
		Idempotency:     idem,
		Escalations:     esc,
		Memory:          mem,
		Classifications: newFakeClassifications(),
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
		MemoryLimits:    processinquiry.MemoryLimits{Cap: 50, Keep: 20},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Log:             discardLogger(),
		Critic:          &fakeCritic{verdict: failVerdict("critic_uncertain")},
	}
	deps.Classifier = mustClassifier(t,
		`{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`,
	)
	deps.Generator = mustGenerator(t, domain.Reply{
		Body:        "Yes, the Soho 2BR is open Apr 24-26 for 4 at $480 total. Self check-in, courtyard quiet. Want me to send the booking link?",
		Confidence:  0.95,
		CloserBeats: closerAll(),
	}, true)

	in := validInput()
	processinquiry.New(deps).Run(context.Background(), in)

	if len(g.postedNotes) != 0 {
		t.Fatalf("critic-rejected reply must NOT be posted to Guesty, got %d", len(g.postedNotes))
	}
	if len(esc.records) != 1 {
		t.Fatalf("expected 1 escalation, got %d", len(esc.records))
	}
	rec, _ := mem.Get(context.Background(), in.Turn.Key)
	if len(rec.Thread) != 1 {
		t.Fatalf("memory thread must contain only the guest message after a critic rejection, got %d entries: %+v",
			len(rec.Thread), rec.Thread)
	}
	if rec.Thread[0].Role != domain.RoleGuest {
		t.Fatalf("the only memory entry must be the guest message, got role %s", rec.Thread[0].Role)
	}
	if rec.LastAutoSendAt != nil {
		t.Fatalf("LastAutoSendAt must remain nil when no note was posted, got %v", rec.LastAutoSendAt)
	}
	if rec.LastEscalationAt == nil {
		t.Fatalf("LastEscalationAt must be set after an escalation")
	}
}

// TestMultiTurnContextCarriesForwardEntities verifies the classifier's user
// message gets `known_from_prior_turns` even when the fresh webhook thread is
// empty — this is the root-cause fix for the tester-UI scenario.
func TestMultiTurnContextCarriesForwardEntities(t *testing.T) {
	t.Parallel()

	// Set up a memory record with prior context (simulates an earlier turn).
	mem := newFakeMemory()
	checkIn := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	checkOut := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	four := 4
	_ = mem.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = testConvID
		r.KnownEntities = domain.ExtractedEntities{CheckIn: &checkIn, CheckOut: &checkOut, GuestCount: &four}
		r.Thread = []domain.Message{
			{PostID: "post_0", Body: "Is the Soho 2BR open April 24-26?", Role: domain.RoleGuest, CreatedAt: time.Now().Add(-10 * time.Minute)},
			{PostID: "bot_post_0", Body: "Yes the Soho 2BR is open April 24-26. Total is $480.", Role: domain.RoleHost, CreatedAt: time.Now().Add(-9 * time.Minute)},
		}
	})

	// A follow-up turn with a NEW entity (pets) and an empty webhook thread
	// (matches what the tester UI sends and what Guesty sometimes sends for
	// first-page fetches).
	deps := emptyDeps(t)
	deps.Memory = mem
	deps.MemoryLimits = processinquiry.MemoryLimits{Cap: 50, Keep: 20}
	clsJSON := `{"primary_code":"Y5","confidence":0.9,"extracted_entities":{"pets":true},"risk_flag":false,"next_action":"escalate_human","reasoning":"pets permission"}`
	deps.Classifier = mustClassifier(t, clsJSON)

	in := validInput()
	in.Turn.LastPostID = "post_3"
	in.Turn.Messages = []domain.Message{
		{PostID: "post_3", Body: "Are small dogs ok?", Role: domain.RoleGuest, CreatedAt: time.Now()},
	}
	in.Conversation.Thread = nil // empty webhook thread → memory is authoritative

	processinquiry.New(deps).Run(context.Background(), in)

	got, _ := mem.Get(context.Background(), testConvID)
	if got.KnownEntities.CheckIn == nil {
		t.Fatal("CheckIn must survive across turns (memory merge, not overwrite)")
	}
	if got.KnownEntities.Pets == nil || !*got.KnownEntities.Pets {
		t.Fatalf("Pets from new turn must merge into KnownEntities, got %+v", got.KnownEntities)
	}
	if *got.KnownEntities.GuestCount != 4 {
		t.Fatalf("GuestCount must persist across turns, got %d", *got.KnownEntities.GuestCount)
	}
}
