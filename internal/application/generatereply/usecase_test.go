package generatereply_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type scriptedLLM struct {
	steps []openai.ChatCompletionResponse
	idx   int
	calls int
}

func (s *scriptedLLM) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	s.calls++
	if s.idx >= len(s.steps) {
		return openai.ChatCompletionResponse{}, errors.New("no more responses")
	}
	r := s.steps[s.idx]
	s.idx++
	return r, nil
}

type stubGuesty struct {
	listingErr error
}

func (g stubGuesty) GetListing(_ context.Context, id string) (domain.Listing, error) {
	if g.listingErr != nil {
		return domain.Listing{}, g.listingErr
	}
	return domain.Listing{ID: id, Title: "Soho 2BR", MaxGuests: 4, Bedrooms: 2, Beds: 3}, nil
}

func (stubGuesty) CheckAvailability(_ context.Context, _ string, _, _ time.Time) (domain.Availability, error) {
	return domain.Availability{Available: true, Nights: 2, TotalUSD: 480}, nil
}

func (stubGuesty) GetConversationHistory(_ context.Context, _ string, _ int, _ string) ([]domain.Message, error) {
	return nil, nil
}

func (stubGuesty) GetConversation(_ context.Context, _ string) (domain.Conversation, error) {
	return domain.Conversation{}, nil
}

func (stubGuesty) PostNote(_ context.Context, _, _ string) error { return nil }

func finalReplyJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(domain.Reply{
		Body:       "Hi Sarah — quick city weekend for 4. Soho 2BR sleeps 4 with self check-in. Those dates are open and the total is $480 all-in. Courtyard bedroom is the quietest sleep in Manhattan. Want me to hold it?",
		Confidence: 0.85,
		CloserBeats: domain.CloserBeats{
			Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func toolCall(id, name, args string) openai.ToolCall {
	return openai.ToolCall{
		ID:       id,
		Type:     openai.ToolTypeFunction,
		Function: openai.FunctionCall{Name: name, Arguments: args},
	}
}

func TestGenerateHappyPath(t *testing.T) {
	t.Parallel()
	s := &scriptedLLM{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			ToolCalls: []openai.ToolCall{
				toolCall("c1", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`),
			},
		}}}},
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			Content: finalReplyJSON(t),
		}}}},
	}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{
		Turn:      domain.Turn{Messages: []domain.Message{{Body: "open for Fri-Sun, 4 adults?"}}},
		ListingID: "L1",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Body == "" || r.Confidence < 0.7 {
		t.Fatalf("bad reply: %+v", r)
	}
	if len(r.UsedTools) != 1 || r.UsedTools[0].Name != "check_availability" {
		t.Fatalf("tool log: %+v", r.UsedTools)
	}
	if r.UsedTools[0].LatencyMs < 0 {
		t.Fatalf("negative latency: %d", r.UsedTools[0].LatencyMs)
	}
}

func TestGenerateParallelToolCalls(t *testing.T) {
	t.Parallel()
	s := &scriptedLLM{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			ToolCalls: []openai.ToolCall{
				toolCall("c1", "get_listing", `{"listing_id":"L1"}`),
				toolCall("c2", "check_availability", `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`),
			},
		}}}},
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			Content: finalReplyJSON(t),
		}}}},
	}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{ListingID: "L1", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.UsedTools) != 2 {
		t.Fatalf("want 2 tool calls, got %d", len(r.UsedTools))
	}
}

func TestGenerateMaxTurnsTriggersReflection(t *testing.T) {
	t.Parallel()
	loop := openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
		ToolCalls: []openai.ToolCall{toolCall("c1", "get_listing", `{"listing_id":"L1"}`)},
	}}}}
	reflectJSON := `{"body":"","closer_beats":{"clarify":false,"label":false,"overview":false,"sell_certainty":false,"explain":false,"request":false},"confidence":0.2,"reflection_reason":"I kept fetching the listing but never closed the loop","missing_info":["explicit check-out date"],"partial_findings":"Listing confirmed"}`
	s := &scriptedLLM{steps: []openai.ChatCompletionResponse{
		loop, loop, loop, loop,
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: reflectJSON}}}},
	}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{ListingID: "L1", Now: time.Now()})
	if err != nil {
		t.Fatalf("max_turns must not error: %v", err)
	}
	if r.AbortReason != "max_turns" {
		t.Fatalf("want AbortReason=max_turns, got %q", r.AbortReason)
	}
	if r.ReflectionReason == "" {
		t.Fatalf("want ReflectionReason populated, got %+v", r)
	}
	if len(r.MissingInfo) == 0 {
		t.Fatalf("want MissingInfo populated, got %+v", r.MissingInfo)
	}
}

func TestGenerateReturnsErrorOnInvalidFinalJSON(t *testing.T) {
	t.Parallel()
	s := &scriptedLLM{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "not json"}}}},
	}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	_, err := u.Generate(context.Background(), generatereply.Input{ListingID: "L1", Now: time.Now()})
	if !errors.Is(err, domain.ErrReplyInvalid) {
		t.Fatalf("want ErrReplyInvalid, got %v", err)
	}
}

func TestGenerateSurfacesToolErrorToLLM(t *testing.T) {
	t.Parallel()
	// First call: LLM tries get_listing, which fails. Second call: LLM emits final reply.
	s := &scriptedLLM{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{
			ToolCalls: []openai.ToolCall{toolCall("c1", "get_listing", `{"listing_id":"L1"}`)},
		}}}},
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: finalReplyJSON(t)}}}},
	}}
	g := stubGuesty{listingErr: errors.New("boom")}
	u := generatereply.New(s, g, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{ListingID: "L1", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if r.UsedTools[0].Error == "" {
		t.Fatalf("want tool error recorded, got %+v", r.UsedTools[0])
	}
}
