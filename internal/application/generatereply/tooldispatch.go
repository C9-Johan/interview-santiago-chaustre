package generatereply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

type getListingArgs struct {
	ListingID string `json:"listing_id"`
}

type checkAvailArgs struct {
	ListingID string `json:"listing_id"`
	From      string `json:"from"`
	To        string `json:"to"`
}

type historyArgs struct {
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
	BeforePostID   string `json:"before_post_id"`
}

type holdReservationArgs struct {
	ListingID  string `json:"listing_id"`
	CheckIn    string `json:"check_in"`
	CheckOut   string `json:"check_out"`
	GuestCount int    `json:"guest_count"`
	Status     string `json:"status"`
}

const dateFormat = "2006-01-02"

// runTool dispatches a single tool call against the real Guesty client and
// returns a domain.ToolCall audit record. Errors are encoded into Result as
// {"error":"..."} so the LLM sees them and can adapt (classic agent-loop
// error feedback pattern) instead of the loop aborting on the first failure.
//
// expectedListingID is the listing the orchestrator resolved for THIS turn.
// Tools that take a listing_id reject mismatches deterministically — the
// model has been observed passing the reservation id (e.g. "res_test_001")
// instead of the listing id, which would silently hold the wrong calendar.
func runTool(ctx context.Context, guesty repository.GuestyClient, tc openai.ToolCall, expectedListingID string) domain.ToolCall {
	start := time.Now()
	rec := domain.ToolCall{
		Name:      tc.Function.Name,
		Arguments: json.RawMessage(tc.Function.Arguments),
	}
	switch tc.Function.Name {
	case "get_listing":
		rec = runGetListing(ctx, guesty, tc, rec, expectedListingID)
	case "check_availability":
		rec = runCheckAvailability(ctx, guesty, tc, rec, expectedListingID)
	case "get_conversation_history":
		rec = runGetConversationHistory(ctx, guesty, tc, rec)
	case "hold_reservation":
		rec = runHoldReservation(ctx, guesty, tc, rec, expectedListingID)
	default:
		rec.Result, rec.Error = encodeErr("unknown_tool", tc.Function.Name)
	}
	rec.LatencyMs = time.Since(start).Milliseconds()
	return rec
}

// rejectIfWrongListing returns a populated rec with the canonical
// invalid_listing_id error when the model passed the wrong listing id, or
// the unchanged rec to signal the caller may proceed. Empty expected means
// the orchestrator did not resolve a listing — skip the check rather than
// fail every call.
func rejectIfWrongListing(got, expected string, rec domain.ToolCall) (domain.ToolCall, bool) {
	if expected == "" || got == expected {
		return rec, false
	}
	rec.Result, rec.Error = encodeErr("invalid_listing_id",
		fmt.Sprintf("listing_id must be %q for this turn, got %q — use the listing id from get_listing, not the reservation id", expected, got))
	return rec, true
}

func runGetListing(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall, expectedListingID string) domain.ToolCall {
	var args getListingArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
	}
	if r, rejected := rejectIfWrongListing(args.ListingID, expectedListingID, rec); rejected {
		return r
	}
	res, err := g.GetListing(ctx, args.ListingID)
	if err != nil {
		rec.Result, rec.Error = encodeErr("get_listing_failed", err.Error())
		return rec
	}
	rec.Result = mustMarshal(res)
	return rec
}

func runCheckAvailability(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall, expectedListingID string) domain.ToolCall {
	var args checkAvailArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
	}
	if r, rejected := rejectIfWrongListing(args.ListingID, expectedListingID, rec); rejected {
		return r
	}
	from, ferr := time.Parse(dateFormat, args.From)
	to, terr := time.Parse(dateFormat, args.To)
	if ferr != nil || terr != nil {
		rec.Result, rec.Error = encodeErr("invalid_dates", fmt.Sprintf("from=%v to=%v", ferr, terr))
		return rec
	}
	res, err := g.CheckAvailability(ctx, args.ListingID, from, to)
	if err != nil {
		rec.Result, rec.Error = encodeErr("check_availability_failed", err.Error())
		return rec
	}
	rec.Result = mustMarshal(res)
	return rec
}

func runGetConversationHistory(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall) domain.ToolCall {
	var args historyArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
	}
	res, err := g.GetConversationHistory(ctx, args.ConversationID, args.Limit, args.BeforePostID)
	if err != nil {
		rec.Result, rec.Error = encodeErr("history_failed", err.Error())
		return rec
	}
	rec.Result = mustMarshal(res)
	return rec
}

func runHoldReservation(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall, expectedListingID string) domain.ToolCall {
	var args holdReservationArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
	}
	if r, rejected := rejectIfWrongListing(args.ListingID, expectedListingID, rec); rejected {
		return r
	}
	checkIn, cinErr := time.Parse(dateFormat, args.CheckIn)
	checkOut, coutErr := time.Parse(dateFormat, args.CheckOut)
	if cinErr != nil || coutErr != nil {
		rec.Result, rec.Error = encodeErr("invalid_dates", fmt.Sprintf("check_in=%v check_out=%v", cinErr, coutErr))
		return rec
	}
	status := normalizeHoldStatus(args.Status)
	if status == "" {
		rec.Result, rec.Error = encodeErr("invalid_status", "status must be inquiry or reserved")
		return rec
	}
	res, err := g.CreateReservation(ctx, domain.ReservationHoldInput{
		ListingID:  args.ListingID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		GuestCount: args.GuestCount,
		Status:     status,
	})
	if err != nil {
		rec.Result, rec.Error = encodeErr("hold_reservation_failed", err.Error())
		return rec
	}
	rec.Result = mustMarshal(res)
	return rec
}

func normalizeHoldStatus(s string) domain.ReservationHoldStatus {
	switch s {
	case string(domain.ReservationInquiry):
		return domain.ReservationInquiry
	case string(domain.ReservationReserved):
		return domain.ReservationReserved
	}
	return ""
}

func encodeErr(code, msg string) (json.RawMessage, string) {
	b, err := json.Marshal(map[string]string{"error": code, "detail": msg})
	if err != nil {
		// json.Marshal on a map[string]string never fails; fall back to a raw string.
		return json.RawMessage(fmt.Sprintf(`{"error":%q,"detail":%q}`, code, msg)), code + ": " + msg
	}
	return b, code + ": " + msg
}

func mustMarshal[T any](v T) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"error":"marshal_failed","detail":%q}`, err.Error()))
	}
	return b
}
