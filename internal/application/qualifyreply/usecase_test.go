package qualifyreply

import (
	"context"
	"errors"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
)

type fakeLLM struct {
	resp openai.ChatCompletionResponse
	err  error
}

func (f *fakeLLM) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return f.resp, f.err
}

func respWith(content string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content},
		}},
	}
}

func TestGenerate_HappyPath(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{resp: respWith(`{
		"body": "Hi Sarah — when are you thinking of staying? How many guests will be with you?",
		"questions_asked": ["dates", "guests"],
		"confidence": 0.9
	}`)}
	uc := New(llm, "test-model", time.Second)
	r, err := uc.Generate(context.Background(), Input{
		Classification: domain.Classification{PrimaryCode: domain.X1, Confidence: 0.95},
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Body == "" {
		t.Errorf("body empty")
	}
	if r.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", r.Confidence)
	}
	if len(r.MissingInfo) != 2 || r.MissingInfo[0] != "dates" {
		t.Errorf("MissingInfo = %v, want [dates guests]", r.MissingInfo)
	}
}

func TestGenerate_LLMError(t *testing.T) {
	t.Parallel()
	uc := New(&fakeLLM{err: errors.New("timeout")}, "test-model", time.Second)
	_, err := uc.Generate(context.Background(), Input{Now: time.Now().UTC()})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerate_UnparseableJSON(t *testing.T) {
	t.Parallel()
	uc := New(&fakeLLM{resp: respWith("not json")}, "test-model", time.Second)
	_, err := uc.Generate(context.Background(), Input{Now: time.Now().UTC()})
	if !errors.Is(err, domain.ErrReplyInvalid) {
		t.Fatalf("want ErrReplyInvalid, got %v", err)
	}
}

func TestGenerate_EmptyChoices(t *testing.T) {
	t.Parallel()
	uc := New(&fakeLLM{resp: openai.ChatCompletionResponse{}}, "test-model", time.Second)
	_, err := uc.Generate(context.Background(), Input{Now: time.Now().UTC()})
	if !errors.Is(err, domain.ErrReplyInvalid) {
		t.Fatalf("want ErrReplyInvalid, got %v", err)
	}
}

func TestGenerate_AbortReason(t *testing.T) {
	t.Parallel()
	llm := &fakeLLM{resp: respWith(`{"body":"","confidence":0.3,"abort_reason":"policy_decline"}`)}
	uc := New(llm, "test-model", time.Second)
	r, err := uc.Generate(context.Background(), Input{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.AbortReason != "policy_decline" {
		t.Errorf("AbortReason = %q, want policy_decline", r.AbortReason)
	}
}

func TestMissingSlots(t *testing.T) {
	t.Parallel()
	checkIn := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	checkOut := checkIn.Add(48 * time.Hour)
	guests := 4
	hint := "Soho 2BR"

	cases := []struct {
		name   string
		entity domain.ExtractedEntities
		empty  bool
		want   []string
	}{
		{"nothing known (first contact)", domain.ExtractedEntities{}, true, []string{"dates", "guests", "listing"}},
		{"only dates known", domain.ExtractedEntities{CheckIn: &checkIn, CheckOut: &checkOut}, true, []string{"guests", "listing"}},
		{"everything known", domain.ExtractedEntities{CheckIn: &checkIn, CheckOut: &checkOut, GuestCount: &guests, ListingHint: &hint}, true, nil},
		{"listing not asked when prior thread exists", domain.ExtractedEntities{}, false, []string{"dates", "guests"}},
	}
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := missingSlots(domain.Classification{ExtractedEntities: tc.entity}, tc.empty)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for j := range got {
				if got[j] != tc.want[j] {
					t.Errorf("slot[%d] = %q, want %q", j, got[j], tc.want[j])
				}
			}
		})
	}
}
