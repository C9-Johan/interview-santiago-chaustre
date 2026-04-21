package http

import (
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
)

// ToDomain projects the transport DTO into the domain types the pipeline uses
// downstream. Missing reservations and empty threads are handled gracefully —
// inquiries can arrive before a Guesty reservation exists, and first messages
// have no prior thread.
func ToDomain(dto WebhookRequestDTO) (domain.Message, domain.Conversation) {
	msg := mappers.MessageFromGuesty(mappers.GuestyMessageDTO{
		PostID:    dto.Message.PostID,
		Body:      dto.Message.Body,
		CreatedAt: dto.Message.CreatedAt,
		Type:      dto.Message.Type,
		Module:    dto.Message.Module,
	})
	conv := domain.Conversation{
		RawID:        dto.Conversation.ID,
		GuestID:      dto.Conversation.GuestID,
		GuestName:    dto.Conversation.Meta.GuestName,
		Language:     dto.Conversation.Language,
		Integration:  domain.Integration{Platform: dto.Conversation.Integration.Platform},
		Reservations: reservationsFromDTO(dto.Conversation.Meta.Reservations),
		Thread:       threadFromDTO(dto.Conversation.Thread),
	}
	return msg, conv
}

func reservationsFromDTO(in []WebhookReservationMeta) []domain.Reservation {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.Reservation, 0, len(in))
	for i := range in {
		out = append(out, domain.Reservation{
			ID:               in[i].ID,
			CheckIn:          in[i].CheckIn,
			CheckOut:         in[i].CheckOut,
			ConfirmationCode: in[i].ConfirmationCode,
		})
	}
	return out
}

func threadFromDTO(in []WebhookMessage) []domain.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.Message, 0, len(in))
	for i := range in {
		out = append(out, mappers.MessageFromGuesty(mappers.GuestyMessageDTO{
			PostID:    in[i].PostID,
			Body:      in[i].Body,
			CreatedAt: in[i].CreatedAt,
			Type:      in[i].Type,
			Module:    in[i].Module,
		}))
	}
	return out
}
