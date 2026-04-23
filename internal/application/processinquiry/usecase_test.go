package processinquiry_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/reviewreply"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// fakeCritic returns a fixed verdict so a test can drive the orchestrator
// down the critic-rejection path without spinning up a real LLM.
type fakeCritic struct {
	verdict reviewreply.Verdict
}

func (f *fakeCritic) Review(_ context.Context, _ reviewreply.Input) reviewreply.Verdict {
	return f.verdict
}

// failVerdict builds a critic verdict that fails with the given hard-blocker
// tag, matching the on-the-wire shape the production critic emits.
func failVerdict(issue string) reviewreply.Verdict {
	return reviewreply.Verdict{
		Pass:       false,
		Issues:     []string{issue},
		Confidence: 0.9,
		Reasoning:  "test fixture: critic blocker",
	}
}

// --- fakes ---

type fakeLLM struct {
	steps []openai.ChatCompletionResponse
	idx   int
}

func (f *fakeLLM) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if f.idx >= len(f.steps) {
		return openai.ChatCompletionResponse{}, errors.New("no more responses")
	}
	r := f.steps[f.idx]
	f.idx++
	return r, nil
}

type fakeGuesty struct {
	postNoteErr  error
	postedNotes  []string
	postedConvID string
	mu           sync.Mutex
}

func (*fakeGuesty) GetListing(_ context.Context, _ string) (domain.Listing, error) {
	return domain.Listing{ID: "L1", Title: "Soho 2BR", MaxGuests: 4, Bedrooms: 2}, nil
}

func (*fakeGuesty) CheckAvailability(_ context.Context, _ string, _, _ time.Time) (domain.Availability, error) {
	return domain.Availability{Available: true, Nights: 2, TotalUSD: 480}, nil
}

func (*fakeGuesty) GetConversationHistory(_ context.Context, _ string, _ int, _ string) ([]domain.Message, error) {
	return nil, nil
}

func (*fakeGuesty) GetConversation(_ context.Context, _ string) (domain.Conversation, error) {
	return domain.Conversation{}, nil
}

func (g *fakeGuesty) PostNote(_ context.Context, convID, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.postNoteErr != nil {
		return g.postNoteErr
	}
	g.postedConvID = convID
	g.postedNotes = append(g.postedNotes, body)
	return nil
}

func (*fakeGuesty) CreateReservation(_ context.Context, in domain.ReservationHoldInput) (domain.ReservationHoldResult, error) {
	return domain.ReservationHoldResult{
		ID:               "res_fake_" + in.ListingID,
		Status:           in.Status,
		CheckIn:          in.CheckIn,
		CheckOut:         in.CheckOut,
		ConfirmationCode: "FAKEHOLD",
	}, nil
}

type fakeIdempotency struct {
	completed map[string]bool
	mu        sync.Mutex
}

func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{completed: map[string]bool{}}
}

func (f *fakeIdempotency) SeenOrClaim(_ context.Context, _ domain.ConversationKey, _ string) (bool, error) {
	return false, nil
}

func (f *fakeIdempotency) Complete(_ context.Context, k domain.ConversationKey, postID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed[string(k)+"|"+postID] = true
	return nil
}

type fakeEscalations struct {
	records []domain.Escalation
	mu      sync.Mutex
}

func (f *fakeEscalations) Record(_ context.Context, e domain.Escalation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, e)
	return nil
}

func (f *fakeEscalations) List(_ context.Context, _ int) ([]domain.Escalation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Escalation, len(f.records))
	copy(out, f.records)
	return out, nil
}

type fakeClassifications struct {
	saved map[string]domain.Classification
	mu    sync.Mutex
}

func newFakeClassifications() *fakeClassifications {
	return &fakeClassifications{saved: map[string]domain.Classification{}}
}

func (f *fakeClassifications) Put(_ context.Context, postID string, c domain.Classification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[postID] = c
	return nil
}

func (f *fakeClassifications) Get(_ context.Context, postID string) (domain.Classification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.saved[postID]
	if !ok {
		return domain.Classification{}, errors.New("not found")
	}
	return c, nil
}

type fakeReplies struct {
	saved map[string]domain.Reply
	mu    sync.Mutex
}

func newFakeReplies() *fakeReplies {
	return &fakeReplies{saved: map[string]domain.Reply{}}
}

func (f *fakeReplies) Put(_ context.Context, postID string, r domain.Reply) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved[postID] = r
	return nil
}

func (f *fakeReplies) Get(_ context.Context, postID string) (domain.Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.saved[postID]
	if !ok {
		return domain.Reply{}, errors.New("not found")
	}
	return r, nil
}

type fakeMemory struct {
	records  map[domain.ConversationKey]domain.ConversationMemoryRecord
	byGuest  map[string][]domain.ConversationMemoryRecord
	mu       sync.Mutex
	lastSave *domain.ConversationMemoryRecord
}

func newFakeMemory() *fakeMemory {
	return &fakeMemory{
		records: map[domain.ConversationKey]domain.ConversationMemoryRecord{},
		byGuest: map[string][]domain.ConversationMemoryRecord{},
	}
}

func (f *fakeMemory) Get(_ context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.records[k], nil
}

func (f *fakeMemory) Update(_ context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.records[k]
	mut(&r)
	f.records[k] = r
	f.lastSave = &r
	return nil
}

func (f *fakeMemory) ListByGuest(_ context.Context, guestID string, _ int) ([]domain.ConversationMemoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byGuest[guestID], nil
}

// --- helpers ---

func mustClassifier(t *testing.T, raw string) *classify.UseCase {
	t.Helper()
	f := &fakeLLM{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: raw}}}},
	}}
	u, err := classify.New(f, "m", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustClassifierErr(t *testing.T) *classify.UseCase {
	t.Helper()
	f := &fakeLLM{} // empty -> Chat returns error
	u, err := classify.New(f, "m", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustGenerator(t *testing.T, reply domain.Reply, withAvailabilityTool bool) *generatereply.UseCase {
	t.Helper()
	replyJSON, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	steps := make([]openai.ChatCompletionResponse, 0, 2)
	if withAvailabilityTool {
		steps = append(steps, openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{
				ToolCalls: []openai.ToolCall{{
					ID: "c1", Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: "check_availability", Arguments: `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`},
				}},
			},
		}}})
	}
	steps = append(steps, openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{
		Message: openai.ChatCompletionMessage{Content: string(replyJSON)},
	}}})
	f := &fakeLLM{steps: steps}
	return generatereply.New(f, &fakeGuesty{}, "m", 5*time.Second, 4)
}

func closerAll() domain.CloserBeats {
	return domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true}
}

const goodReplyBody = "Hi Sarah — you're looking at Fri–Sun for 4, quick city weekend. Our Soho 2BR sleeps 4 with self check-in on Spring St. Those dates are open and the total is $480 for 2 nights, taxes included. The courtyard bedroom is the quietest sleep in Manhattan. Want me to hold it while you decide?"

// testConvID is the synthetic conversation id every orchestrator test uses.
// Declared once so goconst stops flagging the duplication across tests.
const testConvID = "conv_test"

func validInput() processinquiry.Input {
	return processinquiry.Input{
		Turn: domain.Turn{
			Key:        domain.ConversationKey(testConvID),
			LastPostID: "post_1",
			Messages: []domain.Message{
				{PostID: "post_1", Body: "Open Fri-Sun for 4 adults?", Role: domain.RoleGuest, CreatedAt: time.Now()},
			},
		},
		Conversation: domain.Conversation{
			RawID:       testConvID,
			GuestID:     "guest_1",
			GuestName:   "Sarah",
			Integration: domain.Integration{Platform: "airbnb2"},
		},
		ListingID: "L1",
		Now:       time.Now().UTC(),
	}
}

func newDeps(t *testing.T, classifierJSON string, reply domain.Reply, withAvailability bool, postErr error) (processinquiry.Deps, *fakeGuesty, *fakeEscalations, *fakeMemory, *fakeIdempotency) {
	t.Helper()
	g := &fakeGuesty{postNoteErr: postErr}
	esc := &fakeEscalations{}
	mem := newFakeMemory()
	idem := newFakeIdempotency()
	deps := processinquiry.Deps{
		Classifier:      mustClassifier(t, classifierJSON),
		Generator:       mustGenerator(t, reply, withAvailability),
		Guesty:          g,
		Idempotency:     idem,
		Escalations:     esc,
		Memory:          mem,
		Classifications: newFakeClassifications(),
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return deps, g, esc, mem, idem
}

// --- tests ---

func TestRunHappyAutoSend(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	reply := domain.Reply{Body: goodReplyBody, Confidence: 0.85, CloserBeats: closerAll()}
	deps, g, esc, mem, idem := newDeps(t, clsJSON, reply, true, nil)

	in := validInput()
	processinquiry.New(deps).Run(context.Background(), in)

	if len(g.postedNotes) != 1 {
		t.Fatalf("want 1 note posted, got %d", len(g.postedNotes))
	}
	if g.postedConvID != testConvID {
		t.Fatalf("want conv_test, got %q", g.postedConvID)
	}
	if len(esc.records) != 0 {
		t.Fatalf("no escalation expected, got %+v", esc.records)
	}
	if mem.lastSave == nil || mem.lastSave.LastAutoSendAt == nil {
		t.Fatalf("memory not updated with LastAutoSendAt: %+v", mem.lastSave)
	}
	if !idem.completed["conv_test|post_1"] {
		t.Fatal("idempotency Complete not called")
	}
}

func TestRunPreGenerateEscalatesOnRequiresHuman(t *testing.T) {
	t.Parallel()
	// Y2 is always-escalate
	clsJSON := `{"primary_code":"Y2","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"escalate_human","reasoning":"refund"}`
	deps, g, esc, _, idem := newDeps(t, clsJSON, domain.Reply{}, false, nil)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 0 {
		t.Fatalf("no note should be posted, got %d", len(g.postedNotes))
	}
	if len(esc.records) != 1 {
		t.Fatalf("want 1 escalation, got %d", len(esc.records))
	}
	if esc.records[0].Reason != "code_requires_human" {
		t.Fatalf("reason: %q", esc.records[0].Reason)
	}
	if !idem.completed["conv_test|post_1"] {
		t.Fatal("idempotency not closed after escalation")
	}
}

func TestRunPreGenerateEscalatesOnRiskFlag(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"G1","confidence":0.9,"extracted_entities":{},"risk_flag":true,"risk_reason":"venmo","next_action":"escalate_human","reasoning":"off-platform"}`
	deps, g, esc, _, _ := newDeps(t, clsJSON, domain.Reply{}, false, nil)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 0 {
		t.Fatalf("no note should be posted")
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "risk_flag" {
		t.Fatalf("want risk_flag escalation, got %+v", esc.records)
	}
}

func TestRunGeneratorAbortEscalates(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y1","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"parking"}`
	reply := domain.Reply{AbortReason: "policy_decline", Confidence: 0.1, CloserBeats: domain.CloserBeats{}}
	deps, g, esc, _, _ := newDeps(t, clsJSON, reply, false, nil)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 0 {
		t.Fatalf("no note should be posted")
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "generator_aborted" {
		t.Fatalf("want generator_aborted, got %+v", esc.records)
	}
	if esc.records[0].Reply == nil || esc.records[0].Reply.AbortReason != "policy_decline" {
		t.Fatalf("escalation should carry the reply with abort reason")
	}
}

func TestRunRestrictedContentInReplyEscalates(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y1","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"parking"}`
	bodyWithVenmo := "Hi Sarah, parking is across the street. We accept venmo if that helps speed things up. Want me to hold it? Those dates are open. The courtyard bedroom is quiet."
	reply := domain.Reply{Body: bodyWithVenmo, Confidence: 0.9, CloserBeats: closerAll()}
	deps, g, esc, _, _ := newDeps(t, clsJSON, reply, true, nil)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 0 {
		t.Fatalf("should NOT post; got %d", len(g.postedNotes))
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "restricted_content" {
		t.Fatalf("want restricted_content escalation, got %+v", esc.records)
	}
}

func TestRunCriticRestrictedContactAndIntentAreAdvisory(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	reply := domain.Reply{Body: goodReplyBody, Confidence: 0.9, CloserBeats: closerAll()}
	deps, g, esc, _, _ := newDeps(t, clsJSON, reply, true, nil)
	deps.Critic = &fakeCritic{verdict: reviewreply.Verdict{
		Pass:       false,
		Issues:     []string{"restricted_contact_exchange", "intent_mismatch"},
		Confidence: 0.91,
		Reasoning:  "fixture: noisy critic tags",
	}}

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 1 {
		t.Fatalf("should still auto-send; got %d notes", len(g.postedNotes))
	}
	if len(esc.records) != 0 {
		t.Fatalf("critic-only advisory tags must not escalate, got %+v", esc.records)
	}
}

func TestRunCommitmentHandoffSkippedWhenPriorHoldSucceeded(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	reply := domain.Reply{Body: goodReplyBody, Confidence: 0.9, CloserBeats: closerAll()}
	deps, g, esc, mem, _ := newDeps(t, clsJSON, reply, true, nil)
	replies := newFakeReplies()
	deps.Replies = replies

	_ = mem.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.Thread = []domain.Message{{
			PostID: "bot_post_prev",
			Role:   domain.RoleHost,
			Body:   "Yes, the Soho 2BR is available. Want me to hold the dates while you decide?",
		}}
	})
	_ = replies.Put(context.Background(), "post_prev", domain.Reply{UsedTools: []domain.ToolCall{{
		Name:   "hold_reservation",
		Error:  "",
		Result: json.RawMessage(`{"id":"res_ok"}`),
	}}})

	in := validInput()
	in.Turn.Messages = []domain.Message{{PostID: "post_2", Body: "Yes please", Role: domain.RoleGuest, CreatedAt: time.Now()}}
	in.Turn.LastPostID = "post_2"
	processinquiry.New(deps).Run(context.Background(), in)

	if len(esc.records) != 0 {
		t.Fatalf("should not escalate when prior hold already succeeded, got %+v", esc.records)
	}
	if len(g.postedNotes) != 1 {
		t.Fatalf("should auto-send follow-up reply, got %d notes", len(g.postedNotes))
	}
}

func TestRunCommitmentHandoffStillEscalatesWhenPriorHoldFailed(t *testing.T) {
	t.Parallel()
	deps, g, esc, mem, _ := newDeps(t, `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`,
		domain.Reply{}, false, nil)
	deps.Classifier = mustClassifierErr(t)
	replies := newFakeReplies()
	deps.Replies = replies

	_ = mem.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.Thread = []domain.Message{{
			PostID: "bot_post_prev",
			Role:   domain.RoleHost,
			Body:   "Want me to hold the dates while you decide?",
		}}
	})
	_ = replies.Put(context.Background(), "post_prev", domain.Reply{UsedTools: []domain.ToolCall{{
		Name:   "hold_reservation",
		Error:  "invalid_listing_id: mismatch",
		Result: json.RawMessage(`{"error":"invalid_listing_id"}`),
	}}})

	in := validInput()
	in.Turn.Messages = []domain.Message{{PostID: "post_2", Body: "Yes please", Role: domain.RoleGuest, CreatedAt: time.Now()}}
	in.Turn.LastPostID = "post_2"
	processinquiry.New(deps).Run(context.Background(), in)

	if len(g.postedNotes) != 0 {
		t.Fatalf("must not auto-send when prior hold failed, got %d notes", len(g.postedNotes))
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "commitment_needs_human" {
		t.Fatalf("must escalate commitment flow when prior hold failed, got %+v", esc.records)
	}
}

func TestRunCommitmentHandoffSkipsEscalationForNonBotAliasWhenHoldSucceeded(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	reply := domain.Reply{Body: goodReplyBody, Confidence: 0.9, CloserBeats: closerAll()}
	deps, g, esc, mem, _ := newDeps(t, clsJSON, reply, true, nil)
	replies := newFakeReplies()
	deps.Replies = replies

	const hostOffer = "Yes, the Soho 2BR is available. Want me to hold the dates while you decide?"
	_ = mem.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.Thread = []domain.Message{
			{PostID: "bot_post_prev", Role: domain.RoleHost, Body: hostOffer},
			{PostID: "host_ext_123", Role: domain.RoleHost, Body: hostOffer},
		}
	})
	_ = replies.Put(context.Background(), "post_prev", domain.Reply{UsedTools: []domain.ToolCall{{
		Name:   "hold_reservation",
		Error:  "",
		Result: json.RawMessage(`{"id":"res_ok"}`),
	}}})

	in := validInput()
	in.Turn.Messages = []domain.Message{{PostID: "post_2", Body: "Yes please", Role: domain.RoleGuest, CreatedAt: time.Now()}}
	in.Turn.LastPostID = "post_2"
	processinquiry.New(deps).Run(context.Background(), in)

	if len(esc.records) != 0 {
		t.Fatalf("should not escalate when non-bot alias maps to successful hold, got %+v", esc.records)
	}
	if len(g.postedNotes) != 1 {
		t.Fatalf("should auto-send follow-up reply, got %d notes", len(g.postedNotes))
	}
}

func TestRunCommitmentHandoffEscalatesOnLockRequestAfterSuccessfulHold(t *testing.T) {
	t.Parallel()
	deps, g, esc, mem, _ := newDeps(t, `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`,
		domain.Reply{}, false, nil)
	deps.Classifier = mustClassifierErr(t)
	replies := newFakeReplies()
	deps.Replies = replies

	_ = mem.Update(context.Background(), domain.ConversationKey(testConvID), func(r *domain.ConversationMemoryRecord) {
		r.Thread = []domain.Message{{
			PostID: "bot_post_prev",
			Role:   domain.RoleHost,
			Body:   "Great, I've placed a hold on the Soho 2BR for April 24-26 at $480 total. Ready to lock these dates in?",
		}}
	})
	_ = replies.Put(context.Background(), "post_prev", domain.Reply{UsedTools: []domain.ToolCall{{
		Name:   "hold_reservation",
		Error:  "",
		Result: json.RawMessage(`{"id":"res_ok"}`),
	}}})

	in := validInput()
	in.Turn.Messages = []domain.Message{{PostID: "post_2", Body: "Yes lock them in for me", Role: domain.RoleGuest, CreatedAt: time.Now()}}
	in.Turn.LastPostID = "post_2"
	processinquiry.New(deps).Run(context.Background(), in)

	if len(g.postedNotes) != 0 {
		t.Fatalf("must not auto-send when guest requests lock-in, got %d notes", len(g.postedNotes))
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "commitment_needs_human" {
		t.Fatalf("must escalate lock-in request after hold, got %+v", esc.records)
	}
}

func TestRunPostNoteFailureEscalates(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"Y6","confidence":0.9,"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26"},"risk_flag":false,"next_action":"generate_reply","reasoning":"dates"}`
	reply := domain.Reply{Body: goodReplyBody, Confidence: 0.85, CloserBeats: closerAll()}
	deps, _, esc, _, idem := newDeps(t, clsJSON, reply, true, errors.New("boom"))

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(esc.records) != 1 || esc.records[0].Reason != "post_note_failed" {
		t.Fatalf("want post_note_failed escalation, got %+v", esc.records)
	}
	if esc.records[0].Detail == nil || esc.records[0].Detail[0] != "boom" {
		t.Fatalf("escalation detail should carry transport error, got %+v", esc.records[0].Detail)
	}
	if !idem.completed["conv_test|post_1"] {
		t.Fatal("idempotency must still be closed after post-failure")
	}
}

func TestRunClassifierFailureEscalates(t *testing.T) {
	t.Parallel()
	deps, _, esc, _, idem := newDeps(t, `not json`, domain.Reply{}, false, nil)
	// Override classifier to one whose Chat always errors (empty steps).
	deps.Classifier = mustClassifierErr(t)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(esc.records) != 1 || esc.records[0].Reason != "classifier_failed" {
		t.Fatalf("want classifier_failed escalation, got %+v", esc.records)
	}
	if !idem.completed["conv_test|post_1"] {
		t.Fatal("idempotency must be closed when classifier fails")
	}
}

func TestRunAutoDisabledEscalates(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"G1","confidence":0.95,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"book"}`
	deps, g, esc, _, _ := newDeps(t, clsJSON, domain.Reply{}, false, nil)
	deps.Toggles = processinquiry.StaticToggles{AutoResponseEnabled: false}

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(g.postedNotes) != 0 {
		t.Fatalf("no note when auto disabled")
	}
	if len(esc.records) != 1 || esc.records[0].Reason != "auto_disabled" {
		t.Fatalf("want auto_disabled escalation, got %+v", esc.records)
	}
}

func TestRunLowClassifierConfidenceEscalates(t *testing.T) {
	t.Parallel()
	clsJSON := `{"primary_code":"G1","confidence":0.4,"extracted_entities":{},"risk_flag":false,"next_action":"escalate_human","reasoning":"low conf"}`
	deps, _, esc, _, _ := newDeps(t, clsJSON, domain.Reply{}, false, nil)

	processinquiry.New(deps).Run(context.Background(), validInput())

	if len(esc.records) != 1 || esc.records[0].Reason != "classifier_low_confidence" {
		t.Fatalf("want classifier_low_confidence, got %+v", esc.records)
	}
}
