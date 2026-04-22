package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/budget"
)

// AdminTogglesSource is the consumer-side contract the admin handler needs.
// Satisfied structurally by *togglesource.Source.
type AdminTogglesSource interface {
	Current() domain.Toggles
	SetAutoResponse(ctx context.Context, enabled bool, actor string) (prev, now bool)
}

// AdminBudgetSource is the consumer-side contract for the /admin/budget
// endpoint. Satisfied structurally by *budget.Watcher.
type AdminBudgetSource interface {
	Status() budget.Status
}

// AdminConversionsSource is the consumer-side contract for the
// /admin/conversions endpoint. The handler pulls the raw managed list
// and derives the counts so the store interface stays small.
type AdminConversionsSource interface {
	List(ctx context.Context, limit int) ([]domain.ManagedReservation, error)
}

// AdminClassificationsSource is the consumer-side contract for the
// /admin/classifications/{post_id} endpoint. Satisfied structurally by
// any repository.ClassificationStore (filestore or mongostore).
type AdminClassificationsSource interface {
	Get(ctx context.Context, postID string) (domain.Classification, error)
}

// AdminRepliesSource is the consumer-side contract for reading the stored
// generator reply (body + tool calls + CLOSER beats) per post_id.
// Satisfied structurally by repository.ReplyStore implementations.
type AdminRepliesSource interface {
	Get(ctx context.Context, postID string) (domain.Reply, error)
}

// AdminEscalationsSource is the consumer-side contract for joining a
// post_id with any escalation recorded against it. Satisfied by the
// existing repository.EscalationStore via List() + filter in-handler.
type AdminEscalationsSource interface {
	List(ctx context.Context, limit int) ([]domain.Escalation, error)
}

// AdminHandler exposes operator-controlled runtime state — the auto-response
// kill-switch plus a read-only view of the LLM budget. Guarded by a shared
// bearer token against the Authorization header; an empty configured Token
// disables the routes entirely (503) so an unconfigured deployment never
// accepts anonymous flips. Budget/Conversions are optional — when nil, their
// routes return 503 so the endpoints do not lie.
type AdminHandler struct {
	Source          AdminTogglesSource
	Budget          AdminBudgetSource
	Conversions     AdminConversionsSource
	Classifications AdminClassificationsSource
	Replies         AdminRepliesSource
	Escalations     AdminEscalationsSource
	Token           string
	Log             *slog.Logger
}

// GetAutoResponse returns the current AutoResponseEnabled flag as JSON.
func (h *AdminHandler) GetAutoResponse(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !h.authorized(w, r) {
		return
	}
	t := h.Source.Current()
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"auto_response_enabled": t.AutoResponseEnabled,
	})
}

// autoResponseUpdateDTO is the body shape POST /admin/auto-response accepts.
// Minimal on purpose — the only field is the flag operators flip. `actor` is
// free-form; we do not authenticate operators beyond the bearer token but we
// capture the name they self-report for the audit log.
type autoResponseUpdateDTO struct {
	AutoResponseEnabled bool   `json:"auto_response_enabled"`
	Actor               string `json:"actor"`
}

// SetAutoResponse flips the auto-response flag based on the JSON body and
// returns { previous, auto_response_enabled, actor } so the caller sees both
// states in one response and the audit trail is self-contained.
func (h *AdminHandler) SetAutoResponse(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !h.authorized(w, r) {
		return
	}
	var body autoResponseUpdateDTO
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	actor := strings.TrimSpace(body.Actor)
	if actor == "" {
		actor = "unknown"
	}
	prev, now := h.Source.SetAutoResponse(r.Context(), body.AutoResponseEnabled, actor)
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"previous":              prev,
		"auto_response_enabled": now,
		"actor":                 actor,
	})
}

// GetBudget returns the current day's LLM spend snapshot so operators can
// see how close the watcher is to tripping the kill-switch without tailing
// logs or Prometheus. 503 when no Budget source is wired — a deployment
// that does not track cost should not lie about its cap.
func (h *AdminHandler) GetBudget(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !h.authorized(w, r) {
		return
	}
	if h.Budget == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": "budget tracking disabled"})
		return
	}
	writeJSON(w, nethttp.StatusOK, h.Budget.Status())
}

// GetConversions summarises the bot-managed → converted reservations the
// tracker has recorded so operators can watch conversion rate alongside the
// live escalation queue. 503 when no Conversions source is wired — the
// endpoint never fabricates zeros.
func (h *AdminHandler) GetConversions(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !h.authorized(w, r) {
		return
	}
	if h.Conversions == nil {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": "conversion tracking disabled"})
		return
	}
	items, err := h.Conversions.List(r.Context(), 500)
	if err != nil {
		h.Log.WarnContext(r.Context(), "admin_conversions_list_failed", slog.String("err", err.Error()))
		writeJSON(w, nethttp.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	managed := len(items)
	converted := 0
	for i := range items {
		if items[i].ConvertedAt != nil {
			converted++
		}
	}
	rate := 0.0
	if managed > 0 {
		rate = float64(converted) / float64(managed)
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{
		"managed":   managed,
		"converted": converted,
		"rate":      rate,
		"items":     items,
	})
}

// GetTurnByPostID returns everything the service captured for one guest turn
// keyed by its post_id: the classifier verdict (primary code, entities,
// reasoning, confidence, risk) and any escalation that was recorded against
// the same post_id. Designed for the tester UI so a developer can see why
// the bot decided what it did without tailing logs.
func (h *AdminHandler) GetTurnByPostID(w nethttp.ResponseWriter, r *nethttp.Request) {
	if !h.authorized(w, r) {
		return
	}
	postID := chi.URLParam(r, "post_id")
	if postID == "" {
		writeJSON(w, nethttp.StatusBadRequest, map[string]string{"error": "post_id required"})
		return
	}
	out := map[string]any{"post_id": postID}
	if h.Classifications != nil {
		cls, err := h.Classifications.Get(r.Context(), postID)
		switch {
		case err == nil:
			out["classification"] = cls
		case strings.Contains(err.Error(), "not found"):
			// Turn still in flight or before classification — report absence,
			// not an error. The UI polls and will see it on the next tick.
			out["classification"] = nil
		default:
			h.Log.WarnContext(r.Context(), "admin_classification_lookup_failed",
				slog.String("post_id", postID), slog.String("err", err.Error()))
			out["classification_error"] = err.Error()
		}
	}
	if h.Replies != nil {
		rep, err := h.Replies.Get(r.Context(), postID)
		switch {
		case err == nil:
			out["reply"] = rep
		case strings.Contains(err.Error(), "not found"):
			out["reply"] = nil
		default:
			h.Log.WarnContext(r.Context(), "admin_reply_lookup_failed",
				slog.String("post_id", postID), slog.String("err", err.Error()))
			out["reply_error"] = err.Error()
		}
	}
	if h.Escalations != nil {
		out["escalation"] = findEscalationByPostID(r.Context(), h.Escalations, postID)
	}
	writeJSON(w, nethttp.StatusOK, out)
}

// findEscalationByPostID walks the recent escalation list (capped at 500 to
// keep the operator-facing response bounded) and returns the first match.
// Returns nil when no escalation was recorded for this post — which means
// the turn auto-sent, not that the lookup failed.
func findEscalationByPostID(ctx context.Context, es AdminEscalationsSource, postID string) *domain.Escalation {
	items, err := es.List(ctx, 500)
	if err != nil {
		return nil
	}
	for i := range items {
		if items[i].PostID == postID {
			return &items[i]
		}
	}
	return nil
}

// authorized enforces bearer-token auth. Constant-time comparison avoids
// timing-channel leaks of the expected token.
func (h *AdminHandler) authorized(w nethttp.ResponseWriter, r *nethttp.Request) bool {
	if h.Token == "" {
		writeJSON(w, nethttp.StatusServiceUnavailable, map[string]string{"error": "admin disabled: ADMIN_TOKEN unset"})
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
		writeJSON(w, nethttp.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}
