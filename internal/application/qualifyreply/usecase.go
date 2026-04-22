package qualifyreply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

// llmClient is the narrow unexported contract this use case depends on. Same
// shape as classify/generatereply — production wiring reuses one *llm.Client.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the X1 qualifier generator. One instance per server; concurrent
// Generate calls are safe because the type holds no per-call mutable state.
type UseCase struct {
	llm     llmClient
	model   string
	timeout time.Duration
}

// New constructs a UseCase.
func New(l llmClient, model string, timeout time.Duration) *UseCase {
	return &UseCase{llm: l, model: model, timeout: timeout}
}

// Input is the qualifier contract. The orchestrator supplies the guest turn,
// the classifier verdict (used to derive missing_slots), the prior context
// for personalisation, and the guest's display name if known.
type Input struct {
	Turn           domain.Turn
	Classification domain.Classification
	Prior          domain.PriorContext
	GuestName      string
	ConversationID string
	ListingID      string
	Now            time.Time
}

// wireReply mirrors the JSON the qualifier prompt instructs the model to
// emit. Kept separate from domain.Reply so the orchestrator can normalise
// (questions_asked → missing_info, empty closer_beats) before handing off.
type wireReply struct {
	Body           string   `json:"body"`
	QuestionsAsked []string `json:"questions_asked,omitempty"`
	Confidence     float64  `json:"confidence"`
	AbortReason    string   `json:"abort_reason,omitempty"`
}

// Generate runs one LLM call and returns a domain.Reply shaped for the
// existing escalation/persistence code paths. CloserBeats is left zero-valued
// — the qualifier does not follow the CLOSER scaffold, and the dedicated
// ValidateQualifier gate does not require those beats.
func (u *UseCase) Generate(ctx context.Context, in Input) (domain.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	ctx = llm.WithStage(ctx, llm.StageGenerator)

	resp, err := u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model: u.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
		},
		Temperature:         0.3,
		MaxCompletionTokens: 260,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return domain.Reply{}, fmt.Errorf("qualifier chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return domain.Reply{}, fmt.Errorf("%w: empty choices", domain.ErrReplyInvalid)
	}
	var w wireReply
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &w); err != nil {
		return domain.Reply{}, fmt.Errorf("%w: %s", domain.ErrReplyInvalid, resp.Choices[0].Message.Content)
	}
	return domain.Reply{
		Body:        w.Body,
		Confidence:  w.Confidence,
		AbortReason: w.AbortReason,
		MissingInfo: w.QuestionsAsked,
	}, nil
}
