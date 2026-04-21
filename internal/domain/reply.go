package domain

import "encoding/json"

// CloserBeats records which C.L.O.S.E.R. beats the generator claims it covered.
type CloserBeats struct {
	Clarify       bool `json:"clarify"`
	Label         bool `json:"label"`
	Overview      bool `json:"overview"`
	SellCertainty bool `json:"sell_certainty"`
	Explain       bool `json:"explain"`
	Request       bool `json:"request"`
}

// ToolCall is the audit record for one tool invocation inside the agent loop.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	LatencyMs int64           `json:"latency_ms"`
	Error     string          `json:"error,omitempty"`
}

// Reply is the typed output of Stage B (possibly aborted). JSON tags mirror
// the contract the generator LLM is instructed to emit; see spec §6.1.
type Reply struct {
	Body             string      `json:"body"`
	UsedTools        []ToolCall  `json:"used_tools,omitempty"`
	CloserBeats      CloserBeats `json:"closer_beats"`
	Confidence       float64     `json:"confidence"`
	AbortReason      string      `json:"abort_reason,omitempty"`
	ReflectionReason string      `json:"reflection_reason,omitempty"`
	MissingInfo      []string    `json:"missing_info,omitempty"`
	PartialFindings  string      `json:"partial_findings,omitempty"`
}
