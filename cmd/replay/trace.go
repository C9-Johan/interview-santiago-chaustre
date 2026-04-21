package main

import (
	"context"
	"log/slog"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// maybeTraceLLM wraps inner with a request/response logger when trace=true.
// The wrapper emits one structured log line per Chat call with model, tool
// names, and response snippets — enough to audit a replay session without
// dumping full prompts.
func maybeTraceLLM(inner repository.LLMClient, trace bool, log *slog.Logger) repository.LLMClient {
	if !trace {
		return inner
	}
	return &tracedLLM{inner: inner, log: log}
}

type tracedLLM struct {
	inner repository.LLMClient
	log   *slog.Logger
}

func (t *tracedLLM) Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	t.log.InfoContext(ctx, "llm_request",
		slog.String("model", req.Model),
		slog.Int("messages", len(req.Messages)),
		slog.Int("tools", len(req.Tools)),
	)
	resp, err := t.inner.Chat(ctx, req)
	if err != nil {
		t.log.WarnContext(ctx, "llm_response_error", slog.String("err", err.Error()))
		return resp, err
	}
	t.log.InfoContext(ctx, "llm_response",
		slog.Int("choices", len(resp.Choices)),
		slog.Int("prompt_tokens", resp.Usage.PromptTokens),
		slog.Int("completion_tokens", resp.Usage.CompletionTokens),
	)
	return resp, nil
}

// maybeNoopPost wraps inner so PostNote logs instead of sending when
// execute=false (the default). Every other GuestyClient method passes through
// unchanged so tool calls (listing/availability) still hit Mockoon/Guesty.
func maybeNoopPost(inner repository.GuestyClient, execute bool, log *slog.Logger) repository.GuestyClient {
	if execute {
		return inner
	}
	return &noopPostGuesty{GuestyClient: inner, log: log}
}

type noopPostGuesty struct {
	repository.GuestyClient
	log *slog.Logger
}

func (n *noopPostGuesty) PostNote(ctx context.Context, conversationID, body string) error {
	n.log.InfoContext(ctx, "replay_post_note_suppressed",
		slog.String("conversation_id", conversationID),
		slog.Int("body_len", len(body)),
	)
	return nil
}
