package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

// TestChatSpanAttributes verifies that the llm.chat span carries every
// attribute LangSmith and Grafana rely on (model, temperature, token counts,
// finish reason, tool-call events). Uses an in-memory span recorder so the
// test never touches a real exporter.
func TestChatSpanAttributes(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(openai.ChatCompletionResponse{
			ID: "resp-42",
			Choices: []openai.ChatCompletionChoice{{
				FinishReason: openai.FinishReasonToolCalls,
				Message: openai.ChatCompletionMessage{
					ToolCalls: []openai.ToolCall{{
						ID: "call_1",
						Function: openai.FunctionCall{
							Name:      "check_availability",
							Arguments: `{"listing_id":"L1"}`,
						},
					}},
				},
			}},
			Usage: openai.Usage{PromptTokens: 120, CompletionTokens: 35, TotalTokens: 155},
		})
	}))
	t.Cleanup(srv.Close)

	c := llm.NewClient(srv.URL, "fake-key")
	_, err := c.Chat(context.Background(), openai.ChatCompletionRequest{
		Model:               "deepseek-chat",
		Temperature:         0.1,
		MaxCompletionTokens: 500,
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
		Tools: []openai.Tool{{Type: openai.ToolTypeFunction}},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "llm.chat" {
		t.Fatalf("span name: got %q, want llm.chat", span.Name())
	}
	got := attrMap(span.Attributes())
	wantStr := map[string]string{
		"llm.model":           "deepseek-chat",
		"llm.finish_reason":   string(openai.FinishReasonToolCalls),
		"llm.id":              "resp-42",
		"llm.response_format": string(openai.ChatCompletionResponseFormatTypeJSONObject),
	}
	for k, v := range wantStr {
		if got[k].AsString() != v {
			t.Errorf("%s = %q, want %q", k, got[k].AsString(), v)
		}
	}
	wantInt := map[string]int64{
		"llm.prompt_tokens":     120,
		"llm.completion_tokens": 35,
		"llm.total_tokens":      155,
		"llm.tool_count":        1,
		"llm.tool_calls.count":  1,
		"llm.message_count":     2,
	}
	for k, v := range wantInt {
		if got[k].AsInt64() != v {
			t.Errorf("%s = %d, want %d", k, got[k].AsInt64(), v)
		}
	}

	events := span.Events()
	if len(events) != 1 || events[0].Name != "llm.tool_call" {
		t.Fatalf("want one llm.tool_call event, got %d", len(events))
	}
	evAttrs := attrMap(events[0].Attributes)
	if evAttrs["tool.name"].AsString() != "check_availability" {
		t.Errorf("tool.name = %q", evAttrs["tool.name"].AsString())
	}
}

// TestChatRecordsErrorOnFailure confirms transport-level failures annotate the
// span with Error status + a recorded exception, so LangSmith shows the run as
// failed rather than dropping it silently.
func TestChatRecordsErrorOnFailure(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := llm.NewClient(srv.URL, "fake-key")
	_, err := c.Chat(context.Background(), openai.ChatCompletionRequest{
		Model:    "deepseek-chat",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 500")
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	if got := spans[0].Status().Code.String(); got != "Error" {
		t.Fatalf("status = %s, want Error", got)
	}
	if len(spans[0].Events()) == 0 {
		t.Fatalf("expected an exception event on failure")
	}
}

func attrMap(kvs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value
	}
	return out
}
