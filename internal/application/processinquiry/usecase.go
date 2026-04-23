package processinquiry

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/commitment"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
	"github.com/chaustre/inquiryiq/internal/application/qualifyreply"
	"github.com/chaustre/inquiryiq/internal/application/reviewreply"
	"github.com/chaustre/inquiryiq/internal/application/trackconversion"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

// tracerName identifies spans this orchestrator emits so operators can filter
// them separately from infrastructure spans (HTTP, DB, etc.).
const tracerName = "github.com/chaustre/inquiryiq/processinquiry"

func tracer() trace.Tracer { return otel.Tracer(tracerName) }

// conversionTracker is the narrow interface the orchestrator uses to record
// bot-managed reservations. Satisfied by *trackconversion.UseCase.
type conversionTracker interface {
	MarkManaged(ctx context.Context, in trackconversion.ManagedInput)
}

// confidenceRecorder is the narrow interface the orchestrator uses to record
// the LLM self-rated confidence of each stage so operators can track gate
// calibration. Satisfied by *telemetry.ConfidenceRecorder.
type confidenceRecorder interface {
	RecordClassifier(ctx context.Context, primaryCode string, confidence float64)
	RecordGenerator(ctx context.Context, primaryCode string, confidence float64)
}

// replyCritic is the narrow interface the orchestrator uses to score a
// generated reply. When nil, the orchestrator skips the critic step — kept
// optional so tests and early-stage deployments can run without a second LLM
// call. Satisfied by *reviewreply.UseCase.
type replyCritic interface {
	Review(ctx context.Context, in reviewreply.Input) reviewreply.Verdict
}

// qualifierUC is the narrow interface the orchestrator uses to produce X1
// auto-reply qualifiers. When nil, the orchestrator falls back to the
// spec-faithful "X1 always escalates" behaviour. Satisfied by
// *qualifyreply.UseCase.
type qualifierUC interface {
	Generate(ctx context.Context, in qualifyreply.Input) (domain.Reply, error)
}

// togglesProvider is the runtime source of the auto-send toggles. The
// orchestrator reads Current() on every turn so an operator kill-switch flip
// takes effect immediately. Satisfied by *togglesource.Source in production
// and by StaticToggles in tests.
type togglesProvider interface {
	Current() domain.Toggles
}

// eventPublisher is the narrow consumer-side contract the orchestrator uses
// to emit domain events. Satisfied by *eventbus.Bus; a nil publisher makes
// every emit a no-op so tests don't need to stand up a bus.
type eventPublisher interface {
	Publish(ctx context.Context, topic string, payload any)
}

// summarizer compresses the oldest N entries of a memory thread into a rolled
// summary string. Satisfied by *summarize.UseCase; when nil the orchestrator
// falls back to simple truncation so tests and bare deployments still work.
type summarizer interface {
	Summarize(ctx context.Context, in SummarizeInput) (string, error)
}

// SummarizeInput is the payload the orchestrator hands to the summarizer.
// Kept local so processinquiry can declare the contract without importing the
// summarize package (avoids the cyclic dep with the wiring layer).
type SummarizeInput struct {
	ExistingSummary string
	OlderEntries    []domain.Message
	Now             time.Time
}

// MemoryLimits bounds the per-conversation thread. When len(Thread) exceeds
// Cap the orchestrator folds (len(Thread)-Keep) oldest entries into
// LastSummary and keeps the remaining Keep entries verbatim.
type MemoryLimits struct {
	Cap  int
	Keep int
}

// StaticToggles is the test/config-driven adapter that always returns the same
// domain.Toggles value. Production wiring uses togglesource.Source instead so
// the admin kill-switch can flip state at runtime.
type StaticToggles domain.Toggles

// Current implements togglesProvider on StaticToggles.
func (s StaticToggles) Current() domain.Toggles { return domain.Toggles(s) }

// Deps bundles every collaborator the orchestrator consumes. Construction is
// the wiring responsibility of cmd/server; UseCase itself does not know how
// any collaborator is built. Replies may be nil — when absent the orchestrator
// just skips reply persistence (tests and bare deployments run without it).
type Deps struct {
	Classifier      *classify.UseCase
	Generator       *generatereply.UseCase
	Qualifier       qualifierUC
	Guesty          repository.GuestyClient
	Idempotency     repository.IdempotencyStore
	Escalations     repository.EscalationStore
	Memory          repository.ConversationMemoryStore
	Classifications repository.ClassificationStore
	Replies         repository.ReplyStore
	Conversions     conversionTracker
	Confidence      confidenceRecorder
	Critic          replyCritic
	Toggles         togglesProvider
	Events          eventPublisher
	Summarizer      summarizer
	MemoryLimits    MemoryLimits
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
	ctx, span := tracer().Start(ctx, "processinquiry.Run", trace.WithAttributes(
		attribute.String("conversation.key", string(in.Turn.Key)),
		attribute.String("post.id", in.Turn.LastPostID),
		attribute.String("conversation.platform", in.Conversation.Integration.Platform),
		attribute.String("listing.id", in.ListingID),
	))
	defer span.End()

	if u.detectInjection(ctx, in) {
		return
	}
	if u.shortCircuitKillSwitch(ctx, in) {
		return
	}
	prior := u.priorContext(ctx, in)
	if u.commitmentHandoff(ctx, in, prior) {
		return
	}
	cls, ok := u.classifyOrEscalate(ctx, in, prior)
	if !ok {
		return
	}
	cls = u.enforcePriority(ctx, cls)
	u.recordClassifierConfidence(ctx, cls)
	span.SetAttributes(
		attribute.String("classification.primary_code", string(cls.PrimaryCode)),
		attribute.Float64("classification.confidence", cls.Confidence),
	)
	if cls.PrimaryCode == domain.X1 && u.d.Qualifier != nil {
		if qualifierSaturated(prior.KnownEntities) {
			span.SetAttributes(attribute.String("decision.pre_generate", "qualifier_saturated"))
			u.recordEscalation(ctx, in, cls, nil, domain.Decision{Reason: "qualifier_saturated"})
			u.closeTurn(ctx, in, cls, nil, false)
			return
		}
		u.qualifyAndSend(ctx, in, cls, prior)
		return
	}
	gate1 := decide.PreGenerate(cls, u.d.Toggles.Current(), u.d.Thresholds.ClassifierMin)
	if !gate1.AutoSend {
		span.SetAttributes(attribute.String("decision.pre_generate", gate1.Reason))
		u.recordEscalation(ctx, in, cls, nil, gate1)
		u.closeTurn(ctx, in, cls, nil, false)
		return
	}
	reply, ok := u.generateOrEscalate(ctx, in, cls, prior)
	if !ok {
		return
	}
	u.recordGeneratorConfidence(ctx, cls, reply)
	u.gateAndSend(ctx, in, cls, reply)
}

// qualifyAndSend is the X1 (GRAY) auto-reply path. It runs the qualifier
// LLM call, applies the qualifier-specific Gate 2, and either posts an
// internal note with a 1–2 question qualifier OR escalates when the auto-
// send rules aren't met. The critic is intentionally skipped: the critic
// rubric bakes in CLOSER assumptions that the qualifier path does not
// follow, and a short qualifier has no sell-certainty or factual claims to
// audit. Restricted-content + hedging + length + confidence are still
// enforced via decide.DecideQualifier, so the safety invariants hold.
func (u *UseCase) qualifyAndSend(ctx context.Context, in Input, cls domain.Classification, prior domain.PriorContext) {
	ctx, span := tracer().Start(ctx, "processinquiry.Qualify",
		trace.WithAttributes(attribute.String("classification.primary_code", string(cls.PrimaryCode))))
	defer span.End()

	// Respect the operator kill-switch just like every other auto-send path.
	if !u.d.Toggles.Current().AutoResponseEnabled {
		u.recordEscalation(ctx, in, cls, nil, domain.Decision{Reason: "auto_disabled"})
		u.closeTurn(ctx, in, cls, nil, false)
		return
	}

	reply, err := u.d.Qualifier.Generate(ctx, qualifyreply.Input{
		Turn:           in.Turn,
		Classification: cls,
		Prior:          prior,
		GuestName:      in.Conversation.GuestName,
		ConversationID: in.Conversation.RawID,
		ListingID:      in.ListingID,
		Now:            in.Now,
	})
	if err != nil {
		u.logErr(ctx, "qualifier_failed", err)
		u.recordEscalation(ctx, in, cls, nil, domain.Decision{
			Reason: "qualifier_failed",
			Detail: []string{err.Error()},
		})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return
	}
	if u.d.Replies != nil {
		if err := u.d.Replies.Put(ctx, in.Turn.LastPostID, reply); err != nil {
			u.logErr(ctx, "reply_persist_failed", err)
		}
	}
	u.recordGeneratorConfidence(ctx, cls, reply)

	final := decide.DecideQualifier(reply, u.d.Toggles.Current(), u.d.Thresholds.GeneratorMin)
	span.SetAttributes(
		attribute.Bool("decision.auto_send", final.AutoSend),
		attribute.String("decision.reason", final.Reason),
	)
	if !final.AutoSend {
		u.recordEscalation(ctx, in, cls, &reply, final)
		u.closeTurn(ctx, in, cls, &reply, false)
		return
	}
	if err := u.d.Guesty.PostNote(ctx, in.Conversation.RawID, reply.Body); err != nil {
		u.logErr(ctx, "post_note_failed", err)
		u.recordEscalation(ctx, in, cls, &reply, domain.Decision{
			Reason: "post_note_failed",
			Detail: []string{err.Error()},
		})
		u.closeTurn(ctx, in, cls, &reply, false)
		return
	}
	u.markManaged(ctx, in, cls)
	u.closeTurn(ctx, in, cls, &reply, true)
}

func (u *UseCase) gateAndSend(ctx context.Context, in Input, cls domain.Classification, reply domain.Reply) {
	ctx, span := tracer().Start(ctx, "processinquiry.Decide")
	defer span.End()
	issues := decide.ValidateReply(reply)
	issues = append(issues, u.criticIssues(ctx, in, cls, reply)...)
	final := decide.Decide(cls, reply, issues, u.d.Toggles.Current(), u.d.Thresholds)
	span.SetAttributes(
		attribute.Bool("decision.auto_send", final.AutoSend),
		attribute.String("decision.reason", final.Reason),
	)
	if !final.AutoSend {
		u.recordEscalation(ctx, in, cls, &reply, final)
		u.closeTurn(ctx, in, cls, &reply, false)
		return
	}
	if err := u.d.Guesty.PostNote(ctx, in.Conversation.RawID, reply.Body); err != nil {
		u.logErr(ctx, "post_note_failed", err)
		u.recordEscalation(ctx, in, cls, &reply, domain.Decision{
			Reason: "post_note_failed",
			Detail: []string{err.Error()},
		})
		u.closeTurn(ctx, in, cls, &reply, false)
		return
	}
	u.markManaged(ctx, in, cls)
	u.closeTurn(ctx, in, cls, &reply, true)
}

func (u *UseCase) markManaged(ctx context.Context, in Input, cls domain.Classification) {
	if u.d.Conversions == nil {
		return
	}
	u.d.Conversions.MarkManaged(ctx, trackconversion.ManagedInput{
		ReservationID:   firstReservationID(in.Conversation),
		ConversationKey: in.Turn.Key,
		GuestID:         in.Conversation.GuestID,
		Platform:        in.Conversation.Integration.Platform,
		PrimaryCode:     cls.PrimaryCode,
	})
	if u.d.Events != nil {
		u.d.Events.Publish(ctx, "conversion.managed", map[string]any{
			"reservation_id":   firstReservationID(in.Conversation),
			"conversation_key": string(in.Turn.Key),
			"platform":         in.Conversation.Integration.Platform,
			"primary_code":     string(cls.PrimaryCode),
			"at":               in.Now,
		})
	}
}

func firstReservationID(c domain.Conversation) string {
	if len(c.Reservations) == 0 {
		return ""
	}
	return c.Reservations[0].ID
}

// priorContext reads the memory record as the authoritative thread source and
// merges in any webhook-thread entries we haven't seen yet (e.g. host replied
// manually outside the bot). memory-first — not webhook-first — so the
// orchestrator keeps context even when Guesty's thread view drops an internal
// note or the tester UI sends an empty thread.
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
		Thread:        mergeThread(rec.Thread, in.Conversation.Thread, in.Turn.Messages),
		GuestProfile:  profile,
	}
}

// commitmentHandoff runs the deterministic commitment detector before any LLM
// call. It scans prior host messages newest->oldest for an actionable offer +
// guest acceptance. If the matched offer is already backed by a successful
// hold_reservation tool run, we skip escalation and let the normal pipeline
// continue. Otherwise we escalate to a human. Returns true when this function
// handled the turn.
func (u *UseCase) commitmentHandoff(ctx context.Context, in Input, prior domain.PriorContext) bool {
	guestBody := lastGuestBody(in.Turn)
	if guestBody == "" {
		return false
	}
	candidate, ok := u.latestCommitmentCandidate(ctx, prior.Thread, guestBody)
	if !ok {
		return false
	}
	u.recordEscalation(ctx, in, domain.Classification{}, nil, domain.Decision{
		Reason: candidate.match.EscalationTag,
		Detail: []string{
			"matched_offer=" + candidate.match.MatchedOffer,
			"matched_reply=" + candidate.match.MatchedReply,
		},
	})
	u.closeTurn(ctx, in, domain.Classification{}, nil, false)
	return true
}

type commitmentCandidate struct {
	host  domain.Message
	match commitment.Result
}

func (u *UseCase) latestCommitmentCandidate(
	ctx context.Context,
	thread []domain.Message,
	guestBody string,
) (commitmentCandidate, bool) {
	var fallback commitmentCandidate
	haveFallback := false
	for i := len(thread) - 1; i >= 0; i-- {
		host := thread[i]
		if host.Role != domain.RoleHost || strings.TrimSpace(host.Body) == "" {
			continue
		}
		match := commitment.Detect(host.Body, guestBody)
		if !match.Ok {
			continue
		}
		holdSucceeded := u.hasSuccessfulHold(ctx, host.PostID) || u.hasSuccessfulHoldByBodyAlias(ctx, thread, host)
		if holdSucceeded {
			if commitment.WantsFinalization(guestBody) {
				return commitmentCandidate{host: host, match: match}, true
			}
			return commitmentCandidate{}, false
		}
		candidate := commitmentCandidate{host: host, match: match}
		if strings.HasPrefix(host.PostID, "bot_") {
			return candidate, true
		}
		if !haveFallback {
			fallback = candidate
			haveFallback = true
		}
	}
	if haveFallback {
		return fallback, true
	}
	return commitmentCandidate{}, false
}

func (u *UseCase) hasSuccessfulHold(ctx context.Context, hostPostID string) bool {
	if u.d.Replies == nil {
		return false
	}
	for _, replyPostID := range holdLookupIDs(hostPostID) {
		reply, err := u.d.Replies.Get(ctx, replyPostID)
		if err != nil {
			continue
		}
		for i := range reply.UsedTools {
			if reply.UsedTools[i].Name != "hold_reservation" {
				continue
			}
			if reply.UsedTools[i].Error == "" {
				return true
			}
		}
	}
	return false
}

func holdLookupIDs(hostPostID string) []string {
	if hostPostID == "" {
		return nil
	}
	ids := []string{hostPostID}
	if strings.HasPrefix(hostPostID, "bot_") {
		trimmed := strings.TrimPrefix(hostPostID, "bot_")
		if trimmed != "" {
			ids = append(ids, trimmed)
		}
	} else {
		ids = append(ids, "bot_"+hostPostID)
	}
	return ids
}

func (u *UseCase) hasSuccessfulHoldByBodyAlias(ctx context.Context, thread []domain.Message, host domain.Message) bool {
	if strings.HasPrefix(host.PostID, "bot_") {
		return false
	}
	hostBody := comparableBody(host.Body)
	if hostBody == "" {
		return false
	}
	for i := len(thread) - 1; i >= 0; i-- {
		candidate := thread[i]
		if candidate.Role != domain.RoleHost || !strings.HasPrefix(candidate.PostID, "bot_") {
			continue
		}
		if candidate.PostID == host.PostID {
			continue
		}
		if comparableBody(candidate.Body) != hostBody {
			continue
		}
		if u.hasSuccessfulHold(ctx, candidate.PostID) {
			return true
		}
	}
	return false
}

func comparableBody(s string) string {
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func lastGuestBody(t domain.Turn) string {
	if len(t.Messages) == 0 {
		return ""
	}
	return t.Messages[len(t.Messages)-1].Body
}

// qualifierSaturated returns true when the bot already knows the three
// booking-critical entities (check-in, check-out, guest count) for this
// conversation. Asking a qualifier once more would just loop the guest on
// information we already have, so the orchestrator escalates instead.
func qualifierSaturated(e domain.ExtractedEntities) bool {
	return e.CheckIn != nil && e.CheckOut != nil && e.GuestCount != nil
}

// mergeThread layers webhook-thread entries on top of the memory-stored
// thread, deduplicating by PostID. Messages already present in the current
// Turn are filtered out so the classifier sees them exactly once (in the
// guest_turn envelope, not prior_thread).
func mergeThread(memory, webhook, currentTurn []domain.Message) []domain.Message {
	turnIDs := make(map[string]struct{}, len(currentTurn))
	for i := range currentTurn {
		if currentTurn[i].PostID != "" {
			turnIDs[currentTurn[i].PostID] = struct{}{}
		}
	}
	out := make([]domain.Message, 0, len(memory)+len(webhook))
	seen := make(map[string]struct{}, len(memory)+len(webhook))
	appendIfNew := func(m domain.Message) {
		if m.PostID != "" {
			if _, dup := seen[m.PostID]; dup {
				return
			}
			if _, inTurn := turnIDs[m.PostID]; inTurn {
				return
			}
			seen[m.PostID] = struct{}{}
		}
		out = append(out, m)
	}
	for i := range memory {
		appendIfNew(memory[i])
	}
	for i := range webhook {
		appendIfNew(webhook[i])
	}
	return out
}

// criticBlockerTags is the set of critic issue tags that fail the auto-send
// gate. Other tags (missing_beat_*, too_short, generic_intro,
// factual_unsupported) are advisory: small models report a lot of false
// positives on subjective quality checks, and the deterministic Go gates
// already enforce the real fabrication risks — sell_certainty must pair
// with check_availability (decide.ValidateReply), and uncovered hold /
// out-of-band channel commitments are caught by uncovered_commitment_*
// patterns. The critic-LLM second-guessing a tool-grounded reply is signal
// for observability, not a hard kill switch. Deterministic rules already
// enforce restricted content and commitment promises, so those critic tags are
// advisory-only here to avoid false escalations.
var criticBlockerTags = map[string]struct{}{
	"hedging":          {},
	"off_topic":        {},
	"critic_uncertain": {},
}

// criticIssues invokes the reply critic (when configured) and returns the
// subset of its issue tags that are actual auto-send blockers, prefixed with
// "critic:". A nil critic or a Pass=true verdict returns nil. Advisory-only
// tags (missing_beat_*, too_short, label_leak, …) are logged but do not
// contribute to the escalation reason — they're captured in
// critic_reasoning/critic_confidence so observability can still spot drift
// without flooding the escalation queue.
func (u *UseCase) criticIssues(ctx context.Context, in Input, cls domain.Classification, reply domain.Reply) []string {
	if u.d.Critic == nil {
		return nil
	}
	verdict := u.d.Critic.Review(ctx, reviewreply.Input{
		GuestBody:      firstMessageBody(in.Turn),
		Classification: cls,
		Reply:          reply,
		ToolOutputs:    toolObservationsFrom(reply),
		Now:            in.Now,
	})
	if verdict.Pass {
		return nil
	}
	blockers := make([]string, 0, len(verdict.Issues))
	advisory := make([]string, 0, len(verdict.Issues))
	for i := range verdict.Issues {
		if _, hard := criticBlockerTags[verdict.Issues[i]]; hard {
			blockers = append(blockers, "critic:"+verdict.Issues[i])
			continue
		}
		advisory = append(advisory, verdict.Issues[i])
	}
	if len(advisory) > 0 {
		u.d.Log.InfoContext(ctx, "critic_advisory_only",
			slog.String("post_id", in.Turn.LastPostID),
			slog.String("primary_code", string(cls.PrimaryCode)),
			slog.Any("advisory", advisory),
			slog.Float64("critic_confidence", verdict.Confidence),
			slog.String("critic_reasoning", verdict.Reasoning),
		)
	}
	if len(blockers) == 0 {
		return nil
	}
	return append([]string{"critic_rejected"}, blockers...)
}

func firstMessageBody(t domain.Turn) string {
	if len(t.Messages) == 0 {
		return ""
	}
	return t.Messages[0].Body
}

func toolObservationsFrom(r domain.Reply) []reviewreply.ToolObservation {
	if len(r.UsedTools) == 0 {
		return nil
	}
	out := make([]reviewreply.ToolObservation, 0, len(r.UsedTools))
	for i := range r.UsedTools {
		t := r.UsedTools[i]
		out = append(out, reviewreply.ToolObservation{
			Name:     t.Name,
			Request:  string(t.Arguments),
			Response: string(t.Result),
		})
	}
	return out
}

// shortCircuitKillSwitch exits the pipeline before any LLM call when
// auto_response_enabled is off. Recording the escalation up-front keeps the
// "every turn lands in exactly one of {auto-send, escalation}" invariant,
// and — critically — skipping classification means the kill-switch actually
// saves tokens. The budget watcher depends on this: once the daily cap flips
// the toggle, every subsequent turn must stop costing money.
// Returns true when the turn was handled and Run must return immediately.
func (u *UseCase) shortCircuitKillSwitch(ctx context.Context, in Input) bool {
	if u.d.Toggles.Current().AutoResponseEnabled {
		return false
	}
	u.recordEscalation(ctx, in, domain.Classification{}, nil, domain.Decision{
		Reason: "auto_disabled",
	})
	u.closeTurn(ctx, in, domain.Classification{}, nil, false)
	return true
}

// detectInjection short-circuits the pipeline when any message in the turn
// trips a known prompt-injection pattern. The turn is recorded as an
// escalation with reason=prompt_injection_suspected and the matched-pattern
// name in Detail, so operators see the trigger without every pattern becoming
// a distinct metric label. Returns true when the turn was handled (caller
// must return immediately).
func (u *UseCase) detectInjection(ctx context.Context, in Input) bool {
	for i := range in.Turn.Messages {
		body := in.Turn.Messages[i].Body
		hit, pattern := promptsafety.DetectWithPattern(body)
		if !hit {
			continue
		}
		u.recordEscalation(ctx, in, domain.Classification{}, nil, domain.Decision{
			Reason: promptsafety.ReasonPromptInjection,
			Detail: []string{pattern},
		})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return true
	}
	return false
}

func (u *UseCase) recordClassifierConfidence(ctx context.Context, cls domain.Classification) {
	if u.d.Confidence == nil {
		return
	}
	u.d.Confidence.RecordClassifier(ctx, string(cls.PrimaryCode), cls.Confidence)
}

func (u *UseCase) recordGeneratorConfidence(ctx context.Context, cls domain.Classification, reply domain.Reply) {
	if u.d.Confidence == nil {
		return
	}
	u.d.Confidence.RecordGenerator(ctx, string(cls.PrimaryCode), reply.Confidence)
}

// enforcePriority applies the §6 multi-signal priority rule to the classifier
// output. When the LLM returned a lower-priority primary alongside a
// higher-priority secondary (e.g. primary=Y1, secondary=R1 for
// "any discount? also parking?"), the two are swapped so downstream gates see
// the code that should actually drive the decision. The swap is logged for
// operator visibility into LLM-vs-spec divergence.
func (u *UseCase) enforcePriority(ctx context.Context, cls domain.Classification) domain.Classification {
	corrected, swapped := cls.EnforcePriority()
	if !swapped {
		return cls
	}
	if u.d.Log != nil {
		u.d.Log.InfoContext(ctx, "classification_priority_swapped",
			slog.String("from_primary", string(cls.PrimaryCode)),
			slog.String("to_primary", string(corrected.PrimaryCode)),
		)
	}
	return corrected
}

func (u *UseCase) classifyOrEscalate(ctx context.Context, in Input, prior domain.PriorContext) (domain.Classification, bool) {
	ctx, span := tracer().Start(ctx, "processinquiry.Classify")
	defer span.End()
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
	ctx, span := tracer().Start(ctx, "processinquiry.Generate",
		trace.WithAttributes(attribute.String("classification.primary_code", string(cls.PrimaryCode))))
	defer span.End()
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
	if u.d.Replies != nil {
		if err := u.d.Replies.Put(ctx, in.Turn.LastPostID, reply); err != nil {
			u.logErr(ctx, "reply_persist_failed", err)
		}
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
	u.publishEscalation(ctx, esc)
}

func (u *UseCase) publishEscalation(ctx context.Context, e domain.Escalation) {
	if u.d.Events == nil {
		return
	}
	u.d.Events.Publish(ctx, "escalation.recorded", map[string]any{
		"id":               e.ID,
		"trace_id":         e.TraceID,
		"post_id":          e.PostID,
		"conversation_key": string(e.ConversationKey),
		"guest_id":         e.GuestID,
		"platform":         e.Platform,
		"reason":           e.Reason,
		"detail":           e.Detail,
		"created_at":       e.CreatedAt,
	})
}

// closeTurn flushes the just-handled turn to durable storage. sent must be
// true only when the reply was actually posted to Guesty (PostNote returned
// nil); every escalation, validator/critic rejection, and post-note failure
// passes false. The flag drives whether the bot reply joins the memory
// thread and which timestamp gets stamped — see applyMemoryUpdate.
func (u *UseCase) closeTurn(ctx context.Context, in Input, cls domain.Classification, reply *domain.Reply, sent bool) {
	err := u.d.Memory.Update(ctx, in.Turn.Key, func(r *domain.ConversationMemoryRecord) {
		u.applyMemoryUpdate(ctx, r, in, cls, reply, sent)
	})
	if err != nil {
		u.logErr(ctx, "memory_update_failed", err)
	}
	if err := u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID); err != nil {
		u.logErr(ctx, "idempotency_complete_failed", err)
	}
}

// applyMemoryUpdate writes everything worth remembering from the just-closed
// turn into the persistent record: the guest messages, the bot reply (only
// when it was actually posted to Guesty), the merged entities, and the
// classification + timestamp bookkeeping. The host-message append is gated
// on sent — not on reply presence — because rejected replies (critic,
// validator, post-note failure) never reached the guest, and including them
// in the thread would make the next turn's classifier hallucinate a closed
// exchange. Thread is capped via foldOverflow so unbounded conversations
// don't turn the memory store into a data lake.
func (u *UseCase) applyMemoryUpdate(
	ctx context.Context,
	r *domain.ConversationMemoryRecord,
	in Input,
	cls domain.Classification,
	reply *domain.Reply,
	sent bool,
) {
	r.ConversationKey = in.Turn.Key
	if in.Conversation.GuestID != "" {
		r.GuestID = in.Conversation.GuestID
	}
	if in.Conversation.Integration.Platform != "" {
		r.Platform = in.Conversation.Integration.Platform
	}
	for i := range in.Turn.Messages {
		r.AppendMessage(in.Turn.Messages[i])
	}
	if sent && reply != nil {
		r.AppendMessage(domain.Message{
			PostID:    "bot_" + in.Turn.LastPostID,
			Body:      reply.Body,
			CreatedAt: in.Now,
			Role:      domain.RoleHost,
		})
	}
	if cls.PrimaryCode != "" {
		r.KnownEntities = domain.MergeEntities(r.KnownEntities, cls.ExtractedEntities)
		copied := cls
		r.LastClassification = &copied
	}
	now := in.Now
	if sent {
		r.LastAutoSendAt = &now
	} else {
		r.LastEscalationAt = &now
	}
	u.foldOverflow(ctx, r, now)
	r.UpdatedAt = now
}

// foldOverflow collapses older thread entries into r.LastSummary when the
// thread exceeds the configured cap. When no summarizer is wired we degrade
// to plain truncation — the rolling summary is a nice-to-have, unbounded
// growth is not. Callers must set r.UpdatedAt after foldOverflow returns.
func (u *UseCase) foldOverflow(ctx context.Context, r *domain.ConversationMemoryRecord, now time.Time) {
	limit := u.d.MemoryLimits.Cap
	keep := u.d.MemoryLimits.Keep
	if limit <= 0 || keep <= 0 || len(r.Thread) <= limit {
		return
	}
	split := len(r.Thread) - keep
	if split <= 0 {
		return
	}
	older := r.Thread[:split]
	if u.d.Summarizer == nil {
		if u.d.Log != nil {
			u.d.Log.WarnContext(ctx, "memory_thread_truncated_no_summarizer",
				slog.Int("dropped", split),
				slog.String("conversation_key", string(r.ConversationKey)),
			)
		}
		r.Thread = append([]domain.Message(nil), r.Thread[split:]...)
		return
	}
	summary, err := u.d.Summarizer.Summarize(ctx, SummarizeInput{
		ExistingSummary: r.LastSummary,
		OlderEntries:    older,
		Now:             now,
	})
	if err != nil {
		u.logErr(ctx, "memory_summary_failed", err)
		r.Thread = append([]domain.Message(nil), r.Thread[split:]...)
		return
	}
	r.LastSummary = summary
	if split > 0 {
		r.LastSummaryPostID = older[len(older)-1].PostID
	}
	r.Thread = append([]domain.Message(nil), r.Thread[split:]...)
}

func (u *UseCase) logErr(ctx context.Context, msg string, err error) {
	if u.d.Log == nil {
		return
	}
	u.d.Log.ErrorContext(ctx, msg, slog.String("err", err.Error()))
}
