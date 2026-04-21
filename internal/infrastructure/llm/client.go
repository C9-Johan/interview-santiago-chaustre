// Package llm wraps sashabaranov/go-openai with a configurable BaseURL so the
// same code runs against OpenAI, DeepSeek (default in v1), or any other
// OpenAI-compatible endpoint. The concrete type Client satisfies
// repository.LLMClient.
package llm

import (
	"context"
	"net/http"

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

// Option mutates the openai SDK config before the client is built. Use
// WithHTTPClient to inject a pre-wrapped transport (e.g. otelhttp for OTEL,
// or the LangSmith traceopenai decorator).
type Option func(*openai.ClientConfig)

// WithHTTPClient installs hc as the SDK's underlying HTTP client. Passing nil
// is a no-op.
func WithHTTPClient(hc *http.Client) Option {
	return func(cfg *openai.ClientConfig) {
		if hc != nil {
			cfg.HTTPClient = hc
		}
	}
}

// NewClient constructs a Client against the given BaseURL and API key.
// BaseURL examples: "https://api.deepseek.com/v1", "https://api.openai.com/v1".
func NewClient(baseURL, apiKey string, opts ...Option) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	for _, o := range opts {
		o(&cfg)
	}
	return &Client{c: openai.NewClientWithConfig(cfg)}
}

// Chat forwards the request to the underlying SDK.
func (c *Client) Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return c.c.CreateChatCompletion(ctx, req)
}
