package http

import (
	nethttp "net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter builds the chi router with the routes the service exposes. Kept
// in its own file so cmd/server/main.go can import a tiny constructor
// without pulling Handler details. Passing a nil AdminHandler omits the
// /admin/* routes entirely — useful for environments that should never expose
// runtime toggles, and for the replay CLI.
func NewRouter(h *Handler, rh *ReservationHandler, ah *AdminHandler) nethttp.Handler {
	r := chi.NewRouter()
	r.Post("/webhooks/guesty/message-received", h.Webhook)
	if rh != nil {
		r.Post("/webhooks/guesty/reservation-updated", rh.Updated)
	}
	if ah != nil {
		r.Get("/admin/auto-response", ah.GetAutoResponse)
		r.Post("/admin/auto-response", ah.SetAutoResponse)
		r.Get("/admin/budget", ah.GetBudget)
		r.Get("/admin/conversions", ah.GetConversions)
		r.Get("/admin/turn/{post_id}", ah.GetTurnByPostID)
		r.Post("/admin/reset", ah.PostReset)
	}
	r.Get("/escalations", h.Escalations)
	r.Get("/healthz", h.Health)
	return r
}
