package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

// maxBodyBytes caps the accepted webhook body. Guesty payloads are well under
// this; anything larger is a malformed or malicious request.
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler wires the transport layer to the application pipeline. It reads the
// raw webhook body, verifies the Svix signature, dedupes on
// (ConversationKey,postID), and either drops the buffer (host replied),
// records an empty-body escalation, or hands the message to the debouncer.
// Async hand-off to the orchestrator happens via the debouncer flush callback
// wired in cmd/server — the handler does not call the orchestrator directly.
type Handler struct {
	Webhooks         repository.WebhookStore
	EscalationsStore repository.EscalationStore
	Idempotency      repository.IdempotencyStore
	Resolver         repository.ConversationResolver
	Debouncer        repository.Debouncer
	SvixSecret       string
	SvixMaxDrift     time.Duration
	Log              *slog.Logger
	Now              func() time.Time
}

// NewHandler constructs a Handler. Callers are expected to populate every
// field; zero-value fields will panic at request time.
func NewHandler(h Handler) *Handler {
	if h.Now == nil {
		h.Now = func() time.Time { return time.Now().UTC() }
	}
	return &h
}

// Health answers the liveness probe.
func (h *Handler) Health(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, map[string]string{"status": "ok"})
}

// Escalations returns the most recent escalations for operator review.
func (h *Handler) Escalations(w nethttp.ResponseWriter, r *nethttp.Request) {
	records, err := h.EscalationsStore.List(r.Context(), 100)
	if err != nil {
		h.Log.ErrorContext(r.Context(), "escalations_list_failed", slog.String("err", err.Error()))
		nethttp.Error(w, "internal error", nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, records)
}

// Webhook is the Guesty message-received entry point. Fast-ack 202 once the
// message is queued for async processing by the debouncer.
func (h *Handler) Webhook(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, err := readBody(r)
	if err != nil {
		nethttp.Error(w, "body too large", nethttp.StatusRequestEntityTooLarge)
		return
	}
	ctx, traceID := obs.WithTraceID(r.Context())
	ctx = obs.With(ctx, slog.String("trace_id", traceID))

	if err := h.verifySignature(r, body); err != nil {
		h.Log.WarnContext(ctx, "webhook_signature_invalid", slog.String("err", err.Error()))
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return
	}
	dto, err := parseDTO(body)
	if err != nil {
		h.Log.WarnContext(ctx, "webhook_bad_dto", slog.String("err", err.Error()))
		nethttp.Error(w, "bad request", nethttp.StatusBadRequest)
		return
	}
	msg, conv := ToDomain(dto)
	h.appendWebhookRecord(ctx, r, body, dto, traceID)
	h.dispatch(ctx, w, dispatchIn{msg: msg, conv: conv, traceID: traceID})
}

type dispatchIn struct {
	msg     domain.Message
	conv    domain.Conversation
	traceID string
}

func (h *Handler) dispatch(ctx context.Context, w nethttp.ResponseWriter, in dispatchIn) {
	key, err := h.Resolver.Resolve(ctx, in.conv)
	if err != nil {
		h.Log.ErrorContext(ctx, "resolve_key_failed", slog.String("err", err.Error()))
		nethttp.Error(w, "internal error", nethttp.StatusInternalServerError)
		return
	}
	if in.msg.Role != domain.RoleGuest {
		h.Debouncer.CancelIfHostReplied(key, in.msg.Role)
		writeAccepted(w)
		return
	}
	if strings.TrimSpace(in.msg.Body) == "" {
		h.recordEmptyBodyEscalation(ctx, key, in)
		writeAccepted(w)
		return
	}
	already, err := h.Idempotency.SeenOrClaim(ctx, key, in.msg.PostID)
	if err != nil {
		h.Log.ErrorContext(ctx, "idempotency_claim_failed", slog.String("err", err.Error()))
		nethttp.Error(w, "internal error", nethttp.StatusInternalServerError)
		return
	}
	if already {
		writeJSON(w, nethttp.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	h.Debouncer.Push(ctx, key, in.msg)
	writeAccepted(w)
}

func (h *Handler) verifySignature(r *nethttp.Request, body []byte) error {
	id := r.Header.Get("svix-id")
	ts := r.Header.Get("svix-timestamp")
	sig := r.Header.Get("svix-signature")
	if id == "" || ts == "" || sig == "" {
		return errors.New("missing svix headers")
	}
	return VerifySignature(h.SvixSecret, id, ts, body, sig, h.SvixMaxDrift, h.Now())
}

func (h *Handler) appendWebhookRecord(ctx context.Context, r *nethttp.Request, body []byte, dto WebhookRequestDTO, traceID string) {
	rec := repository.WebhookRecord{
		SvixID:     r.Header.Get("svix-id"),
		Headers:    headerSnapshot(r),
		RawBody:    body,
		ReceivedAt: h.Now(),
		PostID:     dto.Message.PostID,
		ConvRawID:  dto.Conversation.ID,
		TraceID:    traceID,
	}
	if err := h.Webhooks.Append(ctx, rec); err != nil {
		h.Log.WarnContext(ctx, "webhook_append_failed", slog.String("err", err.Error()))
	}
}

func (h *Handler) recordEmptyBodyEscalation(ctx context.Context, key domain.ConversationKey, in dispatchIn) {
	esc := domain.Escalation{
		ID:              uuid.NewString(),
		TraceID:         in.traceID,
		PostID:          in.msg.PostID,
		ConversationKey: key,
		GuestID:         in.conv.GuestID,
		GuestName:       in.conv.GuestName,
		Platform:        in.conv.Integration.Platform,
		CreatedAt:       h.Now(),
		Reason:          "empty_body",
		Detail:          []string{"message body empty or whitespace-only"},
	}
	if err := h.EscalationsStore.Record(ctx, esc); err != nil {
		h.Log.ErrorContext(ctx, "empty_body_escalation_failed", slog.String("err", err.Error()))
	}
}

func parseDTO(body []byte) (WebhookRequestDTO, error) {
	var dto WebhookRequestDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return WebhookRequestDTO{}, err
	}
	return dto, nil
}

func readBody(r *nethttp.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
}

func headerSnapshot(r *nethttp.Request) map[string]string {
	keys := []string{"svix-id", "svix-timestamp", "svix-signature", "user-agent", "content-type"}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v := r.Header.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func writeJSON(w nethttp.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAccepted(w nethttp.ResponseWriter) {
	writeJSON(w, nethttp.StatusAccepted, map[string]string{"status": "accepted"})
}
