// Package trackconversion records bot-managed reservations and detects when
// Guesty promotes them to confirmed bookings. Conversion rate is the ratio of
// the two counters it emits (managed → converted).
package trackconversion

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// StatusConfirmed is the Guesty reservation status that counts as a booking.
const StatusConfirmed = "confirmed"

// Metrics is the narrow interface the use case calls on the telemetry
// counters. A nil Metrics is treated as disabled.
type Metrics interface {
	RecordManaged(ctx context.Context, platform, primaryCode string)
	RecordConverted(ctx context.Context, platform, primaryCode string)
}

// UseCase tracks bot-managed reservations. Safe for concurrent callers.
type UseCase struct {
	store   repository.ConversionStore
	metrics Metrics
	log     *slog.Logger
	now     func() time.Time
}

// New returns a UseCase. store may not be nil; metrics may be.
func New(store repository.ConversionStore, metrics Metrics, log *slog.Logger) *UseCase {
	return &UseCase{store: store, metrics: metrics, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// MarkManaged is called by the orchestrator right after it successfully posts
// an auto-reply. When the conversation has a reservation attached the record
// is persisted and the managed counter ticks. With no reservation the counter
// still ticks so pre-booking inquiries show in conversion-rate denominators.
func (u *UseCase) MarkManaged(ctx context.Context, in ManagedInput) {
	primary := string(in.PrimaryCode)
	if u.metrics != nil {
		u.metrics.RecordManaged(ctx, in.Platform, primary)
	}
	if in.ReservationID == "" {
		return
	}
	rec := domain.ManagedReservation{
		ReservationID:   in.ReservationID,
		ConversationKey: in.ConversationKey,
		GuestID:         in.GuestID,
		Platform:        in.Platform,
		PrimaryCode:     primary,
		ManagedAt:       u.now(),
		Status:          "managed",
	}
	if err := u.store.MarkManaged(ctx, rec); err != nil {
		u.logErr(ctx, "conversion_mark_managed_failed", err)
	}
}

// ReservationUpdated is called by the reservation.updated webhook. When the
// reservation is bot-managed and the new status is confirmed, it records the
// conversion and ticks the converted counter.
func (u *UseCase) ReservationUpdated(ctx context.Context, in UpdatedInput) {
	if in.ReservationID == "" {
		return
	}
	if !strings.EqualFold(in.Status, StatusConfirmed) {
		return
	}
	managed, err := u.store.GetManaged(ctx, in.ReservationID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return // not bot-managed; nothing to record.
		}
		u.logErr(ctx, "conversion_lookup_failed", err)
		return
	}
	if managed.ConvertedAt != nil {
		return // already counted; idempotent.
	}
	at := u.now()
	if err := u.store.RecordConversion(ctx, in.ReservationID, StatusConfirmed, at); err != nil {
		u.logErr(ctx, "conversion_record_failed", err)
		return
	}
	if u.metrics != nil {
		u.metrics.RecordConverted(ctx, managed.Platform, managed.PrimaryCode)
	}
}

// ManagedInput is the orchestrator → tracker projection captured right after
// PostNote succeeds.
type ManagedInput struct {
	ReservationID   string
	ConversationKey domain.ConversationKey
	GuestID         string
	Platform        string
	PrimaryCode     domain.PrimaryCode
}

// UpdatedInput is the transport → tracker projection of a Guesty
// reservation.updated webhook.
type UpdatedInput struct {
	ReservationID string
	Status        string
}

func (u *UseCase) logErr(ctx context.Context, msg string, err error) {
	if u.log == nil {
		return
	}
	u.log.ErrorContext(ctx, msg, slog.String("err", err.Error()))
}
