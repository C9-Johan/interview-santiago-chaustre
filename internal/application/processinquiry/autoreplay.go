package processinquiry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// AutoReplayConfig shapes the auto-replay-on-boot behavior (spec §13.2). A
// directory of *.json fixtures is replayed through the orchestrator on
// startup to give reviewers a one-command demo.
type AutoReplayConfig struct {
	Dir   string
	Delay time.Duration
}

// FixtureMapper converts a raw webhook JSON blob into the Input the
// orchestrator expects. Provided by transport/http so autoreplay shares the
// live-webhook mapping instead of duplicating it.
type FixtureMapper func(raw []byte) (Input, error)

// RunAutoReplay reads *.json fixtures in cfg.Dir in lexical order, maps each
// via mapper, and invokes orch.Run per fixture with cfg.Delay spacing between
// runs. Respects ctx.Done — returns its error immediately. Fixtures that fail
// to read or map are logged and skipped so a single bad file does not abort
// the run. Never panics.
func RunAutoReplay(ctx context.Context, cfg AutoReplayConfig, orch *UseCase, mapper FixtureMapper, log *slog.Logger) error {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return fmt.Errorf("auto-replay: read dir %s: %w", cfg.Dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		replayOne(ctx, cfg.Dir, e.Name(), orch, mapper, log)
		if err := sleepOrCancel(ctx, cfg.Delay); err != nil {
			return err
		}
	}
	return nil
}

func replayOne(ctx context.Context, dir, name string, orch *UseCase, mapper FixtureMapper, log *slog.Logger) {
	path := filepath.Join(filepath.Clean(dir), filepath.Base(name))
	raw, err := os.ReadFile(path)
	if err != nil {
		log.WarnContext(ctx, "auto_replay_read_failed",
			slog.String("file", path), slog.String("err", err.Error()))
		return
	}
	in, err := mapper(raw)
	if err != nil {
		log.WarnContext(ctx, "auto_replay_map_failed",
			slog.String("file", path), slog.String("err", err.Error()))
		return
	}
	in.Now = time.Now().UTC()
	log.InfoContext(ctx, "auto_replay_fixture",
		slog.String("file", path), slog.String("post_id", in.Turn.LastPostID))
	orch.Run(ctx, in)
}

func sleepOrCancel(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
