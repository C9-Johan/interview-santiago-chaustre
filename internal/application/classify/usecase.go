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

	"github.com/chaustre/inquiryiq/internal/application/promptsafety"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
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
// domain.ErrClassificationInvalid when the second attempt also fails; the
// returned error carries the last raw output and the first schema violation
// so the caller's log pinpoints what the model actually produced.
func (u *UseCase) Classify(ctx context.Context, in Input) (domain.Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	ctx = llm.WithStage(ctx, llm.StageClassifier)
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
	}
	var lastRaw, lastReason string
	for range 2 {
		resp, err := u.llm.Chat(ctx, u.request(messages))
		if err != nil {
			return domain.Classification{}, fmt.Errorf("classifier chat: %w", err)
		}
		if len(resp.Choices) == 0 {
			return domain.Classification{}, fmt.Errorf("%w: empty choices", domain.ErrClassificationInvalid)
		}
		msg := resp.Choices[0].Message
		raw := strings.TrimSpace(msg.Content)
		parsed, reason, ok := u.validateAndParse(raw)
		if ok {
			return parsed, nil
		}
		lastRaw, lastReason = raw, reason
		messages = append(messages,
			msg,
			openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Your previous response was not valid JSON per the schema. Return only the object, no prose.",
			},
		)
	}
	return domain.Classification{}, fmt.Errorf(
		"%w: %s (raw=%s)",
		domain.ErrClassificationInvalid,
		lastReason,
		truncate(lastRaw, 400),
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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

// validateAndParse returns the parsed domain object, the human-readable
// reason the input didn't pass (empty on success), and an ok flag.
func (u *UseCase) validateAndParse(raw string) (domain.Classification, string, bool) {
	if raw == "" {
		return domain.Classification{}, "empty content", false
	}
	loader := gojsonschema.NewStringLoader(raw)
	result, err := u.schema.Validate(loader)
	if err != nil {
		return domain.Classification{}, "invalid JSON: " + err.Error(), false
	}
	if !result.Valid() {
		errs := result.Errors()
		if len(errs) > 0 {
			return domain.Classification{}, "schema: " + errs[0].String(), false
		}
		return domain.Classification{}, "schema: unknown violation", false
	}
	wire, err := unmarshalClassification([]byte(raw))
	if err != nil {
		return domain.Classification{}, "unmarshal: " + err.Error(), false
	}
	cls, err := wire.toDomain()
	if err != nil {
		return domain.Classification{}, "domain: " + err.Error(), false
	}
	return cls, "", true
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
	if known := in.Prior.RenderKnownEntities(); known != "" {
		b.WriteString("known_from_prior_turns:\n")
		b.WriteString(known)
	}
	b.WriteString("\n")
	b.WriteString(promptsafety.Wrap("prior_thread", priorThreadText(in.Prior)))
	b.WriteString("\n\n")
	b.WriteString(promptsafety.Wrap("guest_turn", guestTurnText(in.Turn)))
	b.WriteString("\n")
	return b.String()
}

func priorThreadText(p domain.PriorContext) string {
	if len(p.Thread) == 0 {
		return "(empty)"
	}
	var b strings.Builder
	for i := range p.Thread {
		m := p.Thread[i]
		fmt.Fprintf(&b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
	return b.String()
}

func guestTurnText(t domain.Turn) string {
	var b strings.Builder
	for i := range t.Messages {
		fmt.Fprintf(&b, "%s\n", t.Messages[i].Body)
	}
	return b.String()
}
