package http_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

const minFixture = `{
  "event": "reservation.messageReceived",
  "reservationId": "res_test_001",
  "message": {
    "postId": "msg_test_001",
    "body": "Is the Soho 2BR available Fri-Sun for 4 adults? What's the total?",
    "createdAt": "2026-04-20T14:31:09Z",
    "type": "fromGuest",
    "module": "airbnb2"
  },
  "conversation": {
    "_id": "conv_test_001",
    "guestId": "guest_test_001",
    "language": "en",
    "status": "OPEN",
    "integration": { "platform": "airbnb2" },
    "meta": {
      "guestName": "Sarah",
      "reservations": [{
        "_id": "res_test_001",
        "checkIn":  "2026-04-24T22:00:00.000Z",
        "checkOut": "2026-04-26T16:00:00.000Z",
        "confirmationCode": "TESTCODE1"
      }]
    },
    "thread": []
  },
  "meta": { "eventId": "evt_test_001", "messageId": "msgid_test_001" }
}`

func TestToDomainMinimalFixture(t *testing.T) {
	t.Parallel()
	var dto transporthttp.WebhookRequestDTO
	if err := json.Unmarshal([]byte(minFixture), &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg, conv := transporthttp.ToDomain(dto)

	if msg.PostID != "msg_test_001" {
		t.Fatalf("post id: %q", msg.PostID)
	}
	if msg.Role != domain.RoleGuest {
		t.Fatalf("role: %q", msg.Role)
	}
	if msg.Module != "airbnb2" {
		t.Fatalf("module: %q", msg.Module)
	}
	if conv.RawID != "conv_test_001" {
		t.Fatalf("raw id: %q", conv.RawID)
	}
	if conv.GuestID != "guest_test_001" {
		t.Fatalf("guest id: %q", conv.GuestID)
	}
	if conv.GuestName != "Sarah" {
		t.Fatalf("guest name: %q", conv.GuestName)
	}
	if conv.Integration.Platform != "airbnb2" {
		t.Fatalf("platform: %q", conv.Integration.Platform)
	}
	if len(conv.Reservations) != 1 || conv.Reservations[0].ConfirmationCode != "TESTCODE1" {
		t.Fatalf("reservations: %+v", conv.Reservations)
	}
	if !conv.Reservations[0].CheckIn.Equal(time.Date(2026, 4, 24, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("check in: %v", conv.Reservations[0].CheckIn)
	}
	if conv.Thread != nil {
		t.Fatalf("empty thread should map to nil slice, got %+v", conv.Thread)
	}
}

func TestToDomainMapsHostRole(t *testing.T) {
	t.Parallel()
	dto := transporthttp.WebhookRequestDTO{
		Message: transporthttp.WebhookMessage{PostID: "p1", Type: "fromHost"},
	}
	msg, _ := transporthttp.ToDomain(dto)
	if msg.Role != domain.RoleHost {
		t.Fatalf("got %q", msg.Role)
	}
}

func TestToDomainNoReservationsYieldsEmptySlice(t *testing.T) {
	t.Parallel()
	dto := transporthttp.WebhookRequestDTO{}
	_, conv := transporthttp.ToDomain(dto)
	if conv.Reservations != nil {
		t.Fatalf("expected nil reservations, got %+v", conv.Reservations)
	}
}

func TestToDomainThreadMapsEachMessage(t *testing.T) {
	t.Parallel()
	dto := transporthttp.WebhookRequestDTO{
		Conversation: transporthttp.WebhookConv{
			Thread: []transporthttp.WebhookMessage{
				{PostID: "p1", Type: "fromGuest", Body: "hi"},
				{PostID: "p2", Type: "fromHost", Body: "welcome"},
			},
		},
	}
	_, conv := transporthttp.ToDomain(dto)
	if len(conv.Thread) != 2 {
		t.Fatalf("thread len: %d", len(conv.Thread))
	}
	if conv.Thread[0].Role != domain.RoleGuest || conv.Thread[1].Role != domain.RoleHost {
		t.Fatalf("thread roles: %+v", conv.Thread)
	}
}
