package http

import (
	nethttp "net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter builds the chi router with the three routes the service exposes.
// Kept in its own file so cmd/server/main.go can import a tiny constructor
// without pulling Handler details.
func NewRouter(h *Handler) nethttp.Handler {
	r := chi.NewRouter()
	r.Post("/webhooks/guesty/message-received", h.Webhook)
	r.Get("/escalations", h.Escalations)
	r.Get("/healthz", h.Health)
	return r
}
