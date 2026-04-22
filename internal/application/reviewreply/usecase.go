// Package reviewreply implements the Stage B+ reply critic — a second LLM
// call that scores a just-generated reply against the C.L.O.S.E.R. contract,
// restricted-content rules, and factual consistency with tool output. Its
// verdict is advisory: the orchestrator feeds it into GATE 2, which decides
// auto-send vs escalate using the full context (classifier + generator +
// critic). The critic runs at low temperature with a strict JSON schema so
// the verdict is cheap and reproducible.
package reviewreply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/xeipuuv/gojsonschema"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

// llmClient is the narrow unexported contract — the same shape classify and
// generatereply depend on, so the production wiring reuses one *llm.Client.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the reply critic. One instance per server; concurrent Review
// calls are safe because the type holds no per-call mutable state.
type UseCase struct {
	llm     llmClient
	model   string
	timeout time.Duration
	schema  *gojsonschema.Schema
}

// New constructs a UseCase. Returns an error only if the embedded schema
// fails to compile (static string — should never happen at runtime).
func New(llm llmClient, model string, timeout time.Duration) (*UseCase, error) {
	s, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(verdictSchema))
	if err != nil {
		return nil, fmt.Errorf("compile critic schema: %w", err)
	}
	return &UseCase{llm: llm, model: model, timeout: timeout, schema: s}, nil
}

// Input is what the orchestrator passes in: the guest turn's key facts, the
// classifier verdict, the reply-to-score, and the tool outputs the generator
// actually saw. Tool outputs matter because the critic's factual-consistency
// check compares reply claims against what the tools actually returned.
type Input struct {
	GuestBody      string
	Classification domain.Classification
	Reply          domain.Reply
	ToolOutputs    []ToolObservation
	Now            time.Time
}

// ToolObservation is the minimal shape the critic needs to audit a tool call:
// which tool, the request (JSON-ish), and the response (JSON-ish). The
// generator already logs these as domain.ToolUse; the orchestrator maps them
// into this narrower shape so the critic never imports generatereply.
type ToolObservation struct {
	Name     string
	Request  string
	Response string
}

// Verdict is the critic's structured output. Pass is the single boolean GATE
// 2 inspects; Issues is an ordered list of short reason tags for audit logs
// and escalation detail fields. Confidence is the critic's own self-rating.
type Verdict struct {
	Pass       bool
	Issues     []string
	Confidence float64
	Reasoning  string
}

// Review runs the critic on reply and returns its verdict. On schema-invalid
// output or LLM failure, Review returns a fail-closed verdict (Pass=false,
// Issues=[critic_error:...]) rather than an error — the orchestrator must
// always get a decision so it can escalate rather than silently auto-send.
func (u *UseCase) Review(ctx context.Context, in Input) Verdict {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	ctx = llm.WithStage(ctx, llm.StageCritic)
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
	}
	resp, err := u.llm.Chat(ctx, u.request(messages))
	if err != nil {
		return failClosed("critic_call_failed", err)
	}
	if len(resp.Choices) == 0 {
		return failClosed("critic_empty_response", nil)
	}
	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	verdict, reason, ok := u.validateAndParse(raw)
	if !ok {
		return failClosedWithRaw("critic_schema_invalid", reason, raw)
	}
	return verdict
}

func (u *UseCase) request(messages []openai.ChatCompletionMessage) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Temperature:         0.0,
		MaxCompletionTokens: 400,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	}
}

// validateAndParse returns the parsed verdict, a human-readable reason
// when ok is false (e.g. "schema: reasoning is required"), and the ok flag.
func (u *UseCase) validateAndParse(raw string) (Verdict, string, bool) {
	if raw == "" {
		return Verdict{}, "empty content", false
	}
	result, err := u.schema.Validate(gojsonschema.NewStringLoader(raw))
	if err != nil {
		return Verdict{}, "invalid JSON: " + err.Error(), false
	}
	if !result.Valid() {
		errs := result.Errors()
		if len(errs) > 0 {
			return Verdict{}, "schema: " + errs[0].String(), false
		}
		return Verdict{}, "schema: unknown violation", false
	}
	var wire verdictWire
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return Verdict{}, "unmarshal: " + err.Error(), false
	}
	return Verdict(wire), "", true
}

// failClosed builds the verdict returned when the critic itself misbehaves.
// Pass=false guarantees the orchestrator escalates rather than auto-sending a
// reply whose quality the critic never assessed.
func failClosed(tag string, err error) Verdict {
	issue := tag
	if err != nil {
		issue = tag + ":" + err.Error()
	}
	return Verdict{
		Pass:       false,
		Issues:     []string{issue},
		Confidence: 0,
		Reasoning:  "critic unavailable — failing closed to escalation",
	}
}

// failClosedWithRaw is failClosed that also carries the schema-violation
// reason and a truncated raw body in a diagnostic issue tag, so the
// escalation record and service log pinpoint what the model produced.
func failClosedWithRaw(tag, reason, raw string) Verdict {
	v := failClosed(tag, nil)
	if reason != "" {
		v.Issues = append(v.Issues, "diag:"+reason)
	}
	if raw != "" {
		v.Issues = append(v.Issues, "raw:"+truncate(raw, 300))
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
