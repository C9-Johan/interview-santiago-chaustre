package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/trackconversion"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

var errMissingSvixHeaders = errors.New("missing svix headers")

// ReservationTracker is the narrow application contract the reservation
// webhook calls on. Satisfied by *trackconversion.UseCase.
type ReservationTracker interface {
	ReservationUpdated(ctx context.Context, in trackconversion.UpdatedInput)
}

// ReservationHandler is the companion to Handler for Guesty reservation
// events. Signing and verification rules are identical to the message
// webhook, so the same Svix secret applies.
type ReservationHandler struct {
	Tracker      ReservationTracker
	SvixSecret   string
	SvixMaxDrift time.Duration
	Log          *slog.Logger
	Now          func() time.Time
}

// reservationEventDTO is the subset of the Guesty reservation.updated payload
// we consume. Every other field is ignored so producers can add fields
// without breaking us.
type reservationEventDTO struct {
	Event       string `json:"event"`
	Reservation struct {
		ID     string `json:"_id"`
		Status string `json:"status"`
	} `json:"reservation"`
	ReservationID string `json:"reservationId"`
	Status        string `json:"status"`
}

// Updated is the reservation.updated entry point. Returns 202 once the event
// is recorded; conversion tracking is best-effort and logged on failure.
func (h *ReservationHandler) Updated(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, err := readBody(r)
	if err != nil {
		nethttp.Error(w, "body too large", nethttp.StatusRequestEntityTooLarge)
		return
	}
	ctx, traceID := obs.WithTraceID(r.Context())
	ctx = obs.With(ctx, slog.String("trace_id", traceID))
	if err := verifyReservationSignature(h.SvixSecret, r, body, h.SvixMaxDrift, h.Now()); err != nil {
		h.Log.WarnContext(ctx, "reservation_signature_invalid", slog.String("err", err.Error()))
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return
	}
	var dto reservationEventDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		h.Log.WarnContext(ctx, "reservation_bad_dto", slog.String("err", err.Error()))
		nethttp.Error(w, "bad request", nethttp.StatusBadRequest)
		return
	}
	in := reservationUpdatedInput(dto)
	h.Log.InfoContext(ctx, "reservation_updated_received",
		slog.String("reservation_id", in.ReservationID),
		slog.String("status", in.Status),
	)
	h.Tracker.ReservationUpdated(ctx, in)
	writeAccepted(w)
}

func reservationUpdatedInput(dto reservationEventDTO) trackconversion.UpdatedInput {
	id := dto.ReservationID
	if id == "" {
		id = dto.Reservation.ID
	}
	status := dto.Status
	if status == "" {
		status = dto.Reservation.Status
	}
	return trackconversion.UpdatedInput{ReservationID: id, Status: status}
}

func verifyReservationSignature(secret string, r *nethttp.Request, body []byte, maxDrift time.Duration, now time.Time) error {
	id := r.Header.Get("svix-id")
	ts := r.Header.Get("svix-timestamp")
	sig := r.Header.Get("svix-signature")
	if id == "" || ts == "" || sig == "" {
		return errMissingSvixHeaders
	}
	return VerifySignature(secret, id, ts, body, sig, maxDrift, now)
}
