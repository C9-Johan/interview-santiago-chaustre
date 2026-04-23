// Package generatereply implements the Stage B generator — an agent loop that
// calls Guesty tools to gather real listing and availability facts, then emits
// a structured C.L.O.S.E.R. reply JSON. On maxTurns exhaustion a reflection
// prompt is used instead of erroring so the orchestrator always gets a Reply.
package generatereply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

// llmClient is the narrow unexported contract this use case depends on.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the Stage B generator.
type UseCase struct {
	llm      llmClient
	guesty   repository.GuestyClient
	model    string
	timeout  time.Duration
	maxTurns int
}

// New constructs a UseCase. The agent loop budget maxTurns caps the number of
// LLM calls (including the initial call) so a single turn bounds spend.
func New(l llmClient, g repository.GuestyClient, model string, timeout time.Duration, maxTurns int) *UseCase {
	return &UseCase{llm: l, guesty: g, model: model, timeout: timeout, maxTurns: maxTurns}
}

// Input is the generator contract. All fields are required for a high-quality
// reply; zero-valued fields are accepted so the LLM can self-assess.
type Input struct {
	Turn           domain.Turn
	Classification domain.Classification
	Prior          domain.PriorContext
	ConversationID string
	ListingID      string
	Now            time.Time
}

// Generate runs the agent loop and returns a Reply. Never returns
// ErrAgentMaxTurnsExhausted — on max_turns it returns a Reply with
// AbortReason="max_turns" populated via the reflection prompt so the
// orchestrator's record stays uniform.
func (u *UseCase) Generate(ctx context.Context, in Input) (domain.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	ctx = llm.WithStage(ctx, llm.StageGenerator)
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
	}
	toolLog := make([]domain.ToolCall, 0, 4)
	for range u.maxTurns {
		resp, err := u.callWithTools(ctx, messages)
		if err != nil {
			return domain.Reply{}, fmt.Errorf("generator chat: %w", err)
		}
		if len(resp.Choices) == 0 {
			return domain.Reply{}, fmt.Errorf("%w: empty choices", domain.ErrReplyInvalid)
		}
		msg := resp.Choices[0].Message
		messages = append(messages, msg)
		if len(msg.ToolCalls) == 0 {
			return parseFinal(msg.Content, toolLog)
		}
		for _, tc := range msg.ToolCalls {
			rec := runTool(ctx, u.guesty, tc, in.ListingID)
			toolLog = append(toolLog, rec)
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: tc.ID,
				Content:    string(rec.Result),
			})
		}
	}
	return u.reflectOnFailure(ctx, messages, toolLog), nil
}

func (u *UseCase) callWithTools(ctx context.Context, messages []openai.ChatCompletionMessage) (openai.ChatCompletionResponse, error) {
	return u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Tools:               llm.AllTools,
		ToolChoice:          "auto",
		ParallelToolCalls:   true,
		Temperature:         0.3,
		MaxCompletionTokens: 600,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
}

func (u *UseCase) reflectOnFailure(ctx context.Context, messages []openai.ChatCompletionMessage, toolLog []domain.ToolCall) domain.Reply {
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: reflectionSystemPrompt,
	})
	resp, err := u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Temperature:         0.4,
		MaxCompletionTokens: 400,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return domain.Reply{
			AbortReason:      "max_turns",
			ReflectionReason: "reflection call failed: " + err.Error(),
			UsedTools:        toolLog,
		}
	}
	if len(resp.Choices) == 0 {
		return domain.Reply{
			AbortReason:      "max_turns",
			ReflectionReason: "reflection returned no choices",
			UsedTools:        toolLog,
		}
	}
	content := resp.Choices[0].Message.Content
	var r domain.Reply
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return domain.Reply{
			AbortReason:      "max_turns",
			ReflectionReason: "reflection unparseable: " + content,
			UsedTools:        toolLog,
		}
	}
	r.AbortReason = "max_turns"
	r.UsedTools = toolLog
	return r
}

func parseFinal(content string, toolLog []domain.ToolCall) (domain.Reply, error) {
	var r domain.Reply
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return domain.Reply{}, fmt.Errorf("%w: %s", domain.ErrReplyInvalid, content)
	}
	r.UsedTools = toolLog
	return r, nil
}
