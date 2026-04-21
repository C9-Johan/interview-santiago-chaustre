package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

func TestLogAttrsIncludesTraceID(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := obs.NewLogger(&buf, slog.LevelInfo)
	ctx, tid := obs.WithTraceID(context.Background())
	ctx = obs.With(ctx, slog.String("stage", "classifier"))
	obs.LogAttrs(ctx, l, slog.LevelInfo, "ok", slog.Int("latency_ms", 42))
	got := buf.String()
	if !strings.Contains(got, tid) {
		t.Fatalf("log does not contain trace_id %q: %s", tid, got)
	}
	var m map[string]any // any: json wire boundary
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if m["stage"] != "classifier" || m["latency_ms"].(float64) != 42 {
		t.Fatalf("attrs missing: %v", m)
	}
}
