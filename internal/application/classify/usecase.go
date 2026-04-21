// Package classify implements the Stage A classifier — one LLM call, no tools,
// structured JSON output validated against a strict schema in Go.
package classify

import (
	"context"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/xeipuuv/gojsonschema"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// llmClient is the narrow unexported contract Classify depends on. The
// production wiring passes in infrastructure/llm.Client, which satisfies it
// structurally; tests pass a fake.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the Stage A classifier. It issues one LLM call, validates the
// response against a strict JSON schema, and retries once on malformed output
// with an appended corrective system message.
type UseCase struct {
	llm     llmClient
	model   string
	timeout time.Duration
	schema  *gojsonschema.Schema
}

// New constructs a UseCase. Returns an error only if the embedded schema
// fails to compile (should never happen at runtime given the static string).
func New(llm llmClient, model string, timeout time.Duration) (*UseCase, error) {
	s, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(classificationSchema))
	if err != nil {
		return nil, fmt.Errorf("compile classifier schema: %w", err)
	}
	return &UseCase{llm: llm, model: model, timeout: timeout, schema: s}, nil
}

// Input is what the orchestrator passes in — the current turn plus prior context.
type Input struct {
	Turn  domain.Turn
	Prior domain.PriorContext
	Now   time.Time
}

// Classify returns a typed domain.Classification. Retries once on unparseable
// or schema-invalid output with an appended corrective system message. Wraps
// domain.ErrClassificationInvalid when the second attempt also fails.
func (u *UseCase) Classify(ctx context.Context, in Input) (domain.Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
	}
	for range 2 {
		resp, err := u.llm.Chat(ctx, u.request(messages))
		if err != nil {
			return domain.Classification{}, fmt.Errorf("classifier chat: %w", err)
		}
		if len(resp.Choices) == 0 {
			return domain.Classification{}, domain.ErrClassificationInvalid
		}
		msg := resp.Choices[0].Message
		raw := strings.TrimSpace(msg.Content)
		if parsed, ok := u.validateAndParse(raw); ok {
			return parsed, nil
		}
		messages = append(messages,
			msg,
			openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Your previous response was not valid JSON per the schema. Return only the object, no prose.",
			},
		)
	}
	return domain.Classification{}, domain.ErrClassificationInvalid
}

func (u *UseCase) request(messages []openai.ChatCompletionMessage) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Temperature:         0.1,
		MaxCompletionTokens: 500,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
}

func (u *UseCase) validateAndParse(raw string) (domain.Classification, bool) {
	if raw == "" {
		return domain.Classification{}, false
	}
	loader := gojsonschema.NewStringLoader(raw)
	result, err := u.schema.Validate(loader)
	if err != nil || !result.Valid() {
		return domain.Classification{}, false
	}
	wire, err := unmarshalClassification([]byte(raw))
	if err != nil {
		return domain.Classification{}, false
	}
	cls, err := wire.toDomain()
	if err != nil {
		return domain.Classification{}, false
	}
	return cls, true
}

func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format("2006-01-02"))
	if in.Prior.GuestProfile != "" {
		fmt.Fprintf(&b, "guest_profile: %q\n", in.Prior.GuestProfile)
	}
	if in.Prior.Summary != "" {
		fmt.Fprintf(&b, "prior_thread_summary: %q\n", in.Prior.Summary)
	}
	fmt.Fprintf(&b, "prior_thread (last %d):\n", len(in.Prior.Thread))
	for i := range in.Prior.Thread {
		m := in.Prior.Thread[i]
		fmt.Fprintf(&b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
	b.WriteString("\n---\nguest_turn (classify THIS):\n")
	for i := range in.Turn.Messages {
		fmt.Fprintf(&b, "%s\n", in.Turn.Messages[i].Body)
	}
	return b.String()
}
