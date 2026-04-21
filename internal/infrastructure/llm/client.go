// Package llm wraps sashabaranov/go-openai with a configurable BaseURL so the
// same code runs against OpenAI, DeepSeek (default in v1), or any other
// OpenAI-compatible endpoint. The concrete type Client satisfies
// repository.LLMClient.
package llm

import (
	"context"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Compile-time assertion that *Client satisfies the exported LLMClient
// contract; a signature drift on either side becomes a build error.
var _ repository.LLMClient = (*Client)(nil)

// Client is the production LLMClient.
type Client struct {
	c *openai.Client
}

// NewClient constructs a Client against the given BaseURL and API key.
// BaseURL examples: "https://api.deepseek.com/v1", "https://api.openai.com/v1".
func NewClient(baseURL, apiKey string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Client{c: openai.NewClientWithConfig(cfg)}
}

// Chat forwards the request to the underlying SDK.
func (c *Client) Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return c.c.CreateChatCompletion(ctx, req)
}
