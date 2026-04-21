package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"strings"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// AdminTogglesSource is the consumer-side contract the admin handler needs.
// Satisfied structurally by *togglesource.Source.
type AdminTogglesSource interface {
	Current() domain.Toggles
	SetAutoResponse(ctx context.Context, enabled bool, actor string) (prev, now bool)
}

// AdminHandler exposes operator-controlled runtime state — currently only the
// auto-response kill-switch. Guarded by a shared bearer token against the
// Authorization header; an empty configured Token disables the routes entirely
// (503) so an unconfigured deployment never accepts anonymous flips.
type AdminHandler struct {
	Source AdminTogglesSource
	Token  string
	Log    *slog.Logger
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
