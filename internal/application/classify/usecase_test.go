package classify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type fakeLLM struct {
	responses []string
	idx       int
	calls     int
}

func (f *fakeLLM) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	f.calls++
	if f.idx >= len(f.responses) {
		return openai.ChatCompletionResponse{}, errors.New("no more responses")
	}
	r := f.responses[f.idx]
	f.idx++
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Content: r}},
		},
	}, nil
}

func TestClassifyHappyPath(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{
		`{"primary_code":"G1","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"clear booking intent"}`,
	}}
	u, err := classify.New(f, "deepseek-chat", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cls, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if cls.PrimaryCode != domain.G1 {
		t.Fatalf("primary: got %q want G1", cls.PrimaryCode)
	}
	if cls.Confidence != 0.9 {
		t.Fatalf("conf: got %v want 0.9", cls.Confidence)
	}
	if cls.NextAction != domain.ActionGenerate {
		t.Fatalf("action: got %q want generate_reply", cls.NextAction)
	}
	if f.calls != 1 {
		t.Fatalf("want 1 call, got %d", f.calls)
	}
}

func TestClassifyExtractsTypedEntities(t *testing.T) {
	t.Parallel()
	raw := `{
"primary_code":"Y6","confidence":0.8,"risk_flag":false,"next_action":"generate_reply","reasoning":"date availability",
"extracted_entities":{"check_in":"2026-04-24","check_out":"2026-04-26","guest_count":4,"pets":false}
}`
	f := &fakeLLM{responses: []string{raw}}
	u, _ := classify.New(f, "m", time.Second)
	cls, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if cls.ExtractedEntities.CheckIn == nil || cls.ExtractedEntities.CheckIn.Day() != 24 {
		t.Fatalf("check_in: %+v", cls.ExtractedEntities.CheckIn)
	}
	if cls.ExtractedEntities.GuestCount == nil || *cls.ExtractedEntities.GuestCount != 4 {
		t.Fatalf("guest_count: %+v", cls.ExtractedEntities.GuestCount)
	}
	if cls.ExtractedEntities.Pets == nil || *cls.ExtractedEntities.Pets {
		t.Fatalf("pets: %+v", cls.ExtractedEntities.Pets)
	}
}

func TestClassifyRetriesOnInvalidThenSucceeds(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{
		"not json",
		`{"primary_code":"Y1","confidence":0.75,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"parking question"}`,
	}}
	u, _ := classify.New(f, "m", time.Second)
	cls, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if cls.PrimaryCode != domain.Y1 {
		t.Fatalf("got %q", cls.PrimaryCode)
	}
	if f.calls != 2 {
		t.Fatalf("want 2 calls (retry), got %d", f.calls)
	}
}

func TestClassifyFailsAfterTwoInvalid(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{"still not json", "also still not json"}}
	u, _ := classify.New(f, "m", time.Second)
	_, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if !errors.Is(err, domain.ErrClassificationInvalid) {
		t.Fatalf("want ErrClassificationInvalid, got %v", err)
	}
	if f.calls != 2 {
		t.Fatalf("want 2 calls, got %d", f.calls)
	}
}

func TestClassifyRejectsSchemaViolation(t *testing.T) {
	t.Parallel()
	// primary_code outside enum
	f := &fakeLLM{responses: []string{
		`{"primary_code":"ZZ","confidence":0.8,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"bad"}`,
		`{"primary_code":"ZZ","confidence":0.8,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"bad"}`,
	}}
	u, _ := classify.New(f, "m", time.Second)
	_, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if !errors.Is(err, domain.ErrClassificationInvalid) {
		t.Fatalf("want ErrClassificationInvalid, got %v", err)
	}
}

func TestClassifyPropagatesChatError(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{} // empty responses -> returns error
	u, _ := classify.New(f, "m", time.Second)
	_, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err == nil || errors.Is(err, domain.ErrClassificationInvalid) {
		t.Fatalf("want wrapped transport err, got %v", err)
	}
}
