package processinquiry

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

// Deps bundles every collaborator the orchestrator consumes. Construction is
// the wiring responsibility of cmd/server; UseCase itself does not know how
// any collaborator is built.
type Deps struct {
	Classifier      *classify.UseCase
	Generator       *generatereply.UseCase
	Guesty          repository.GuestyClient
	Idempotency     repository.IdempotencyStore
	Escalations     repository.EscalationStore
	Memory          repository.ConversationMemoryStore
	Classifications repository.ClassificationStore
	Toggles         domain.Toggles
	Thresholds      decide.Thresholds
	Log             *slog.Logger
}

// UseCase is the top-level orchestrator. One instance per server.
type UseCase struct {
	d Deps
}

// New constructs a UseCase.
func New(d Deps) *UseCase { return &UseCase{d: d} }

// Input is produced by the debouncer flushing a Turn, plus the resolved
// conversation snapshot from Guesty and the listing id for the reservation.
type Input struct {
	Turn         domain.Turn
	Conversation domain.Conversation
	ListingID    string
	Now          time.Time
}

// Run is the full pipeline for a debounced turn: prior-context -> classify ->
// GATE 1 -> generate -> validate -> GATE 2 -> post-note OR escalate -> memory.
// Never panics — all errors are logged and, where possible, recorded as
// escalations so operators always see the turn.
func (u *UseCase) Run(ctx context.Context, in Input) {
	ctx = obs.With(ctx,
		slog.String("post_id", in.Turn.LastPostID),
		slog.String("conversation_key", string(in.Turn.Key)),
	)
	prior := u.priorContext(ctx, in)
	cls, ok := u.classifyOrEscalate(ctx, in, prior)
	if !ok {
		return
	}
	gate1 := decide.PreGenerate(cls, u.d.Toggles, u.d.Thresholds.ClassifierMin)
	if !gate1.AutoSend {
		u.recordEscalation(ctx, in, cls, nil, gate1)
		u.closeTurn(ctx, in, cls, nil)
		return
	}
	reply, ok := u.generateOrEscalate(ctx, in, cls, prior)
	if !ok {
		return
	}
	u.gateAndSend(ctx, in, cls, reply)
}

func (u *UseCase) gateAndSend(ctx context.Context, in Input, cls domain.Classification, reply domain.Reply) {
	issues := decide.ValidateReply(reply)
	final := decide.Decide(cls, reply, issues, u.d.Toggles, u.d.Thresholds)
	if !final.AutoSend {
		u.recordEscalation(ctx, in, cls, &reply, final)
		u.closeTurn(ctx, in, cls, &reply)
		return
	}
	if err := u.d.Guesty.PostNote(ctx, in.Conversation.RawID, reply.Body); err != nil {
		u.logErr(ctx, "post_note_failed", err)
		u.recordEscalation(ctx, in, cls, &reply, domain.Decision{
			Reason: "post_note_failed",
			Detail: []string{err.Error()},
		})
	}
	u.closeTurn(ctx, in, cls, &reply)
}

func (u *UseCase) priorContext(ctx context.Context, in Input) domain.PriorContext {
	rec, _ := u.d.Memory.Get(ctx, in.Turn.Key)
	profile := ""
	if in.Conversation.GuestID != "" {
		siblings, err := u.d.Memory.ListByGuest(ctx, in.Conversation.GuestID, 5)
		if err == nil {
			profile = BuildGuestProfile(siblings)
		}
	}
	return domain.PriorContext{
		Summary:       rec.LastSummary,
		KnownEntities: rec.KnownEntities,
		Thread:        in.Conversation.Thread,
		GuestProfile:  profile,
	}
}

func (u *UseCase) classifyOrEscalate(ctx context.Context, in Input, prior domain.PriorContext) (domain.Classification, bool) {
	cls, err := u.d.Classifier.Classify(ctx, classify.Input{
		Turn: in.Turn, Prior: prior, Now: in.Now,
	})
	if err != nil {
		u.logErr(ctx, "classify_failed", err)
		u.recordEscalation(ctx, in, domain.Classification{}, nil, domain.Decision{
			Reason: "classifier_failed",
			Detail: []string{err.Error()},
		})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return cls, false
	}
	if err := u.d.Classifications.Put(ctx, in.Turn.LastPostID, cls); err != nil {
		u.logErr(ctx, "classification_persist_failed", err)
	}
	return cls, true
}

func (u *UseCase) generateOrEscalate(ctx context.Context, in Input, cls domain.Classification, prior domain.PriorContext) (domain.Reply, bool) {
	reply, err := u.d.Generator.Generate(ctx, generatereply.Input{
		Turn:           in.Turn,
		Classification: cls,
		Prior:          prior,
		ConversationID: in.Conversation.RawID,
		ListingID:      in.ListingID,
		Now:            in.Now,
	})
	if err != nil {
		u.logErr(ctx, "generate_failed", err)
		u.recordEscalation(ctx, in, cls, nil, domain.Decision{
			Reason: "generator_failed",
			Detail: []string{err.Error()},
		})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return domain.Reply{}, false
	}
	return reply, true
}

func (u *UseCase) recordEscalation(ctx context.Context, in Input, cls domain.Classification, reply *domain.Reply, d domain.Decision) {
	esc := domain.Escalation{
		ID:              uuid.NewString(),
		TraceID:         obs.TraceIDFrom(ctx),
		PostID:          in.Turn.LastPostID,
		ConversationKey: in.Turn.Key,
		GuestID:         in.Conversation.GuestID,
		GuestName:       in.Conversation.GuestName,
		Platform:        in.Conversation.Integration.Platform,
		CreatedAt:       in.Now,
		Reason:          d.Reason,
		Detail:          d.Detail,
		Classification:  cls,
		Reply:           reply,
	}
	if reply != nil {
		esc.MissingInfo = reply.MissingInfo
		esc.PartialFindings = reply.PartialFindings
	}
	if err := u.d.Escalations.Record(ctx, esc); err != nil {
		u.logErr(ctx, "escalation_persist_failed", err)
	}
}

func (u *UseCase) closeTurn(ctx context.Context, in Input, cls domain.Classification, reply *domain.Reply) {
	err := u.d.Memory.Update(ctx, in.Turn.Key, func(r *domain.ConversationMemoryRecord) {
		applyMemoryUpdate(r, in, cls, reply)
	})
	if err != nil {
		u.logErr(ctx, "memory_update_failed", err)
	}
	if err := u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID); err != nil {
		u.logErr(ctx, "idempotency_complete_failed", err)
	}
}

func applyMemoryUpdate(r *domain.ConversationMemoryRecord, in Input, cls domain.Classification, reply *domain.Reply) {
	r.ConversationKey = in.Turn.Key
	if in.Conversation.GuestID != "" {
		r.GuestID = in.Conversation.GuestID
	}
	if in.Conversation.Integration.Platform != "" {
		r.Platform = in.Conversation.Integration.Platform
	}
	if cls.PrimaryCode != "" {
		copied := cls
		r.LastClassification = &copied
	}
	now := in.Now
	if reply != nil && reply.AbortReason == "" {
		r.LastAutoSendAt = &now
	}
	if reply == nil || reply.AbortReason != "" {
		r.LastEscalationAt = &now
	}
	r.UpdatedAt = now
}

func (u *UseCase) logErr(ctx context.Context, msg string, err error) {
	if u.d.Log == nil {
		return
	}
	u.d.Log.ErrorContext(ctx, msg, slog.String("err", err.Error()))
}
