package domain

import "errors"

// Sentinel errors surfaced across package boundaries. Callers MUST compare
// with errors.Is, never ==.
var (
	ErrClassificationInvalid   = errors.New("classification output failed schema validation")
	ErrReplyInvalid            = errors.New("reply output failed schema validation")
	ErrGeneratorAborted        = errors.New("generator returned a non-empty abort_reason")
	ErrAgentMaxTurnsExhausted  = errors.New("agent loop exhausted maxTurns without a final answer")
	ErrDuplicateWebhook        = errors.New("webhook already processed")
	ErrWebhookSignatureInvalid = errors.New("svix signature verification failed")
	ErrWebhookClockDrift       = errors.New("svix timestamp outside allowed drift")
	ErrEmptyMessageBody        = errors.New("message body is empty or whitespace-only")
)
