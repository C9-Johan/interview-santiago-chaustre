package summarize_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/summarize"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type fakeLLM struct {
	gotReq openai.ChatCompletionRequest
	resp   openai.ChatCompletionResponse
	err    error
}

func (f *fakeLLM) Chat(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	f.gotReq = req
	return f.resp, f.err
}

func sampleEntries() []domain.Message {
	t0 := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	return []domain.Message{
		{PostID: "p1", Body: "Is the Soho 2BR free Fri-Sun for 4?", Role: domain.RoleGuest, CreatedAt: t0},
		{PostID: "p2", Body: "Yes, $480 for 2 nights. Want me to hold it?", Role: domain.RoleHost, CreatedAt: t0.Add(time.Minute)},
	}
}

func TestSummarizeReturnsExistingOnEmptyEntries(t *testing.T) {
	llm := &fakeLLM{}
	uc := summarize.New(llm, "model", time.Second)
	out, err := uc.Summarize(context.Background(), processinquiry.SummarizeInput{
		ExistingSummary: "prior text",
		OlderEntries:    nil,
		Now:             time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "prior text" {
		t.Fatalf("expected existing summary to be returned unchanged, got %q", out)
	}
	if llm.gotReq.Model != "" {
		t.Fatal("LLM must not be called when no older entries are provided")
	}
}

func TestSummarizeHappyPath(t *testing.T) {
	llm := &fakeLLM{resp: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Content: "  The guest asked about a 4-guest Soho stay Fri-Sun. The bot confirmed $480 and offered to hold the dates. "},
		}},
	}}
	uc := summarize.New(llm, "deepseek-chat", time.Second)
	out, err := uc.Summarize(context.Background(), processinquiry.SummarizeInput{
		ExistingSummary: "",
		OlderEntries:    sampleEntries(),
		Now:             time.Date(2026, 4, 22, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(out, "The guest asked") {
		t.Fatalf("output should be trimmed, got %q", out)
	}
	if llm.gotReq.Model != "deepseek-chat" {
		t.Fatalf("model should flow through, got %q", llm.gotReq.Model)
	}
	if len(llm.gotReq.Messages) != 2 {
		t.Fatalf("expected system+user messages, got %d", len(llm.gotReq.Messages))
	}
	userMsg := llm.gotReq.Messages[1].Content
	if !strings.Contains(userMsg, "Soho 2BR") {
		t.Fatalf("user message should include guest body, got:\n%s", userMsg)
	}
	if !strings.Contains(userMsg, "<older_entries>") {
		t.Fatalf("older entries should be wrapped for prompt safety, got:\n%s", userMsg)
	}
}

func TestSummarizeTrimsOversizeOutput(t *testing.T) {
	huge := strings.Repeat("X", 5_000)
	llm := &fakeLLM{resp: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Content: huge},
		}},
	}}
	uc := summarize.New(llm, "model", time.Second)
	out, err := uc.Summarize(context.Background(), processinquiry.SummarizeInput{
		OlderEntries: sampleEntries(),
		Now:          time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(out) > 1200 {
		t.Fatalf("expected output capped at 1200 chars, got %d", len(out))
	}
}

func TestSummarizePropagatesLLMError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("boom")}
	uc := summarize.New(llm, "model", time.Second)
	_, err := uc.Summarize(context.Background(), processinquiry.SummarizeInput{
		OlderEntries: sampleEntries(),
		Now:          time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "summarizer chat") {
		t.Fatalf("expected wrapped summarizer chat error, got %v", err)
	}
}

func TestSummarizeEmptyChoices(t *testing.T) {
	llm := &fakeLLM{}
	uc := summarize.New(llm, "model", time.Second)
	_, err := uc.Summarize(context.Background(), processinquiry.SummarizeInput{
		OlderEntries: sampleEntries(),
		Now:          time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "empty choices") {
		t.Fatalf("expected empty-choices error, got %v", err)
	}
}
