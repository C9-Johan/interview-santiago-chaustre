package llm

import "context"

// Stage identifies which application-level LLM call is in progress. The llm
// client attaches this label to token metrics so dashboards can slice
// spend by orchestrator stage (classifier, generator, critic). Unlabeled
// calls appear under "unknown" rather than silently ruining aggregates.
type Stage string

const (
	StageUnknown    Stage = "unknown"
	StageClassifier Stage = "classifier"
	StageGenerator  Stage = "generator"
	StageCritic     Stage = "critic"
)

type stageKey struct{}

// WithStage returns a context carrying the stage label. Call sites set this
// before invoking Client.Chat so the recorded token metrics know which stage
// consumed the budget.
func WithStage(ctx context.Context, s Stage) context.Context {
	return context.WithValue(ctx, stageKey{}, s)
}

// StageFromContext returns the stage stored on ctx, falling back to
// StageUnknown so metric aggregates always group under a defined label.
func StageFromContext(ctx context.Context) Stage {
	if v, ok := ctx.Value(stageKey{}).(Stage); ok && v != "" {
		return v
	}
	return StageUnknown
}
