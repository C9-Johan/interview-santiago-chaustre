package domain

import "encoding/json"

// CloserBeats records which C.L.O.S.E.R. beats the generator claims it covered.
type CloserBeats struct {
	Clarify       bool
	Label         bool
	Overview      bool
	SellCertainty bool
	Explain       bool
	Request       bool
}

// ToolCall is the audit record for one tool invocation inside the agent loop.
type ToolCall struct {
	Name      string
	Arguments json.RawMessage
	Result    json.RawMessage
	LatencyMs int64
	Error     string
}

// Reply is the typed output of Stage B (possibly aborted).
type Reply struct {
	Body             string
	UsedTools        []ToolCall
	CloserBeats      CloserBeats
	Confidence       float64
	AbortReason      string
	ReflectionReason string
	MissingInfo      []string
	PartialFindings  string
}
