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

const dateFormat = "2006-01-02"

// runTool dispatches a single tool call against the real Guesty client and
// returns a domain.ToolCall audit record. Errors are encoded into Result as
// {"error":"..."} so the LLM sees them and can adapt (classic agent-loop
// error feedback pattern) instead of the loop aborting on the first failure.
func runTool(ctx context.Context, guesty repository.GuestyClient, tc openai.ToolCall) domain.ToolCall {
	start := time.Now()
	rec := domain.ToolCall{
		Name:      tc.Function.Name,
		Arguments: json.RawMessage(tc.Function.Arguments),
	}
	switch tc.Function.Name {
	case "get_listing":
		rec = runGetListing(ctx, guesty, tc, rec)
	case "check_availability":
		rec = runCheckAvailability(ctx, guesty, tc, rec)
	case "get_conversation_history":
		rec = runGetConversationHistory(ctx, guesty, tc, rec)
	default:
		rec.Result, rec.Error = encodeErr("unknown_tool", tc.Function.Name)
	}
	rec.LatencyMs = time.Since(start).Milliseconds()
	return rec
}

func runGetListing(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall) domain.ToolCall {
	var args getListingArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
	}
	res, err := g.GetListing(ctx, args.ListingID)
	if err != nil {
		rec.Result, rec.Error = encodeErr("get_listing_failed", err.Error())
		return rec
	}
	rec.Result = mustMarshal(res)
	return rec
}

func runCheckAvailability(ctx context.Context, g repository.GuestyClient, tc openai.ToolCall, rec domain.ToolCall) domain.ToolCall {
	var args checkAvailArgs
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
		return rec
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
