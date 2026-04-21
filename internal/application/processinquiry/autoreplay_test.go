package processinquiry_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// autoReplayDeps builds a minimal Deps whose collaborators satisfy every
// interface the orchestrator touches. Classifier always errors so Run hits
// the classify-failure branch and returns quickly without needing a generator
// or Guesty call; that's sufficient for verifying RunAutoReplay's loop.
func autoReplayDeps(t *testing.T) processinquiry.Deps {
	t.Helper()
	return processinquiry.Deps{
		Classifier:      mustClassifierErr(t),
		Generator:       mustGenerator(t, domain.Reply{}, false),
		Guesty:          &fakeGuesty{},
		Idempotency:     newFakeIdempotency(),
		Escalations:     &fakeEscalations{},
		Memory:          newFakeMemory(),
		Classifications: newFakeClassifications(),
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: true},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Log:             discardLogger(),
	}
}

func TestRunAutoReplayReadsJSONFixtures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.json", "b.json", "skip.txt"} {
		writeFixture(t, dir, name, `{}`)
	}
	var hits int
	mapper := func(_ []byte) (processinquiry.Input, error) {
		hits++
		return processinquiry.Input{}, nil
	}
	orch := processinquiry.New(autoReplayDeps(t))
	cfg := processinquiry.AutoReplayConfig{Dir: dir, Delay: 0}
	if err := processinquiry.RunAutoReplay(context.Background(), cfg, orch, mapper, discardLogger()); err != nil {
		t.Fatalf("RunAutoReplay: %v", err)
	}
	if hits != 2 {
		t.Fatalf("mapper hits: got %d, want 2 (non-json should be ignored)", hits)
	}
}

func TestRunAutoReplayMissingDirReturnsError(t *testing.T) {
	t.Parallel()
	orch := processinquiry.New(autoReplayDeps(t))
	err := processinquiry.RunAutoReplay(context.Background(), processinquiry.AutoReplayConfig{Dir: "/does/not/exist/12345"}, orch, nil, discardLogger())
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestRunAutoReplayMapperErrorIsLoggedNotFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "a.json", `{}`)
	writeFixture(t, dir, "b.json", `{}`)
	mapper := func(raw []byte) (processinquiry.Input, error) {
		if string(raw) == `{}` {
			return processinquiry.Input{}, errors.New("always bad")
		}
		return processinquiry.Input{}, nil
	}
	orch := processinquiry.New(autoReplayDeps(t))
	cfg := processinquiry.AutoReplayConfig{Dir: dir, Delay: 0}
	if err := processinquiry.RunAutoReplay(context.Background(), cfg, orch, mapper, discardLogger()); err != nil {
		t.Fatalf("mapper errors must not abort replay: %v", err)
	}
}

func TestRunAutoReplayRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range 5 {
		writeFixture(t, dir, namedFixture(i), `{}`)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hits int
	mapper := func(_ []byte) (processinquiry.Input, error) {
		hits++
		cancel()
		return processinquiry.Input{}, nil
	}
	orch := processinquiry.New(autoReplayDeps(t))
	cfg := processinquiry.AutoReplayConfig{Dir: dir, Delay: 10 * time.Millisecond}
	err := processinquiry.RunAutoReplay(ctx, cfg, orch, mapper, discardLogger())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want ctx.Canceled, got %v", err)
	}
	if hits > 2 {
		t.Fatalf("ctx cancel should stop replay early; processed %d", hits)
	}
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func namedFixture(i int) string {
	return fmt.Sprintf("f%02d.json", i)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
