// Package obs provides structured logging (slog JSON handler) with a
// trace_id carried through context, plus lightweight helpers used across
// the pipeline's stages.
package obs

import (
	"context"
	"io"
	"log/slog"

	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxKeyTraceID ctxKey = iota + 1
	ctxKeyAttrs
)

// NewLogger builds a JSON slog.Logger at the given level written to w.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// WithTraceID returns ctx with a fresh trace_id and the id itself.
func WithTraceID(ctx context.Context) (context.Context, string) {
	id := uuid.NewString()
	return context.WithValue(ctx, ctxKeyTraceID, id), id
}

// TraceIDFrom returns the trace_id set by WithTraceID, or "".
func TraceIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		return v
	}
	return ""
}

// With returns a context carrying extra log attributes that LogAttrs will merge.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	existing, _ := ctx.Value(ctxKeyAttrs).([]slog.Attr)
	// any: slog.Attr's Value is already typed; we keep the slice typed too.
	merged := make([]slog.Attr, 0, len(existing)+len(attrs))
	merged = append(merged, existing...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxKeyAttrs, merged)
}

// LogAttrs logs via l with merged context attrs plus the trace_id attr.
func LogAttrs(ctx context.Context, l *slog.Logger, level slog.Level, msg string, attrs ...slog.Attr) {
	merged := make([]slog.Attr, 0, len(attrs)+8)
	if tid := TraceIDFrom(ctx); tid != "" {
		merged = append(merged, slog.String("trace_id", tid))
	}
	if existing, ok := ctx.Value(ctxKeyAttrs).([]slog.Attr); ok {
		merged = append(merged, existing...)
	}
	merged = append(merged, attrs...)
	l.LogAttrs(ctx, level, msg, merged...)
}
