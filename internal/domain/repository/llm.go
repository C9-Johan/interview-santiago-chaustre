package repository

import (
	"context"

	openai "github.com/sashabaranov/go-openai"
)

// LLMClient is a narrow wrapper around go-openai so we can swap provider
// (DeepSeek vs OpenAI vs other compatible backends) and inject a fake in
// unit tests. We expose the go-openai request/response types directly
// because they are the SDK's public surface; translating them would add
// zero value at the cost of a wider seam.
type LLMClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}
