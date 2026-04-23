// Package summarize implements the rolling conversation summarizer — one LLM
// call that compresses an older segment of a conversation thread into a
// bounded plain-text summary. Invoked by the orchestrator when a conversation
// memory record's Thread crosses the configured cap so long-running
// conversations retain signal without unbounded prompt growth.
package summarize

import (
	"context"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

// llmClient is the narrow unexported contract — the same shape classify and
// generatereply depend on, so the production wiring reuses one *llm.Client.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// maxSummaryChars caps the output we trust back from the model. The system
// prompt asks for <=600 chars; this is a belt-and-braces server-side cap so
// a chatty model cannot grow the memory record unboundedly.
const maxSummaryChars = 1200

// UseCase is the rolling summarizer. One instance per server; concurrent
// Summarize calls are safe because the type holds no mutable state.
type UseCase struct {
	llm     llmClient
	model   string
	timeout time.Duration
}

// New constructs a UseCase. Returns a pointer so the orchestrator's
// nil-summarizer fallback path stays idiomatic.
func New(llm llmClient, model string, timeout time.Duration) *UseCase {
	return &UseCase{llm: llm, model: model, timeout: timeout}
}

// Summarize folds in.OlderEntries into in.ExistingSummary and returns the
// compressed result. Empty string return is treated by the orchestrator as
// "fall back to plain truncation" — the summarizer is best-effort context,
// not a correctness invariant.
func (u *UseCase) Summarize(ctx context.Context, in processinquiry.SummarizeInput) (string, error) {
	if len(in.OlderEntries) == 0 {
		return in.ExistingSummary, nil
	}
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	ctx = llm.WithStage(ctx, llm.StageSummarizer)
	resp, err := u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model:               u.model,
		Temperature:         0.1,
		MaxCompletionTokens: 400,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
		},
	})
	if err != nil {
		return "", fmt.Errorf("summarizer chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("summarizer: empty choices")
	}
	out := strings.TrimSpace(resp.Choices[0].Message.Content)
	if len(out) > maxSummaryChars {
		out = out[:maxSummaryChars]
	}
	return out, nil
}

func buildUserMessage(in processinquiry.SummarizeInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n\n", in.Now.UTC().Format("2006-01-02"))
	b.WriteString(promptsafety.Wrap("previous_summary", in.ExistingSummary))
	b.WriteString("\n\n")
	b.WriteString(promptsafety.Wrap("older_entries", renderEntries(in.OlderEntries)))
	b.WriteString("\n")
	return b.String()
}

func renderEntries(entries []domain.Message) string {
	if len(entries) == 0 {
		return "(empty)"
	}
	var b strings.Builder
	for i := range entries {
		m := entries[i]
		fmt.Fprintf(&b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
	return b.String()
}
