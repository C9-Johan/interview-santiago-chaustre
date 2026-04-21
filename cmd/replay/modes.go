package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
)

func runPostID(ctx context.Context, d *deps, f flags, log *slog.Logger) error {
	rec, err := d.stores.Webhooks.Get(ctx, f.postID)
	if err != nil {
		return fmt.Errorf("lookup postID %q: %w", f.postID, err)
	}
	return replayRecord(ctx, d, rec, log)
}

func runSince(ctx context.Context, d *deps, f flags, log *slog.Logger) error {
	records, err := d.stores.Webhooks.Since(ctx, f.since)
	if err != nil {
		return fmt.Errorf("list since %s: %w", f.since, err)
	}
	filter, err := buildSinceFilter(ctx, d.stores.Escalations, f.escalationsOnly)
	if err != nil {
		return err
	}
	log.Info("replay_since_begin", slog.Int("count", len(records)), slog.Duration("window", f.since))
	for i := range records {
		if !filter(records[i].PostID) {
			continue
		}
		if err := replayRecord(ctx, d, records[i], log); err != nil {
			log.Warn("replay_record_failed", slog.String("post_id", records[i].PostID), slog.String("err", err.Error()))
		}
	}
	return nil
}

func buildSinceFilter(ctx context.Context, esc repository.EscalationStore, only bool) (func(string) bool, error) {
	if !only {
		return func(string) bool { return true }, nil
	}
	set, err := previouslyEscalated(ctx, esc)
	if err != nil {
		return nil, err
	}
	return func(postID string) bool { return set[postID] }, nil
}

func runFixtures(ctx context.Context, cfg *config.Config, d *deps, f flags, log *slog.Logger) error {
	return processinquiry.RunAutoReplay(ctx, processinquiry.AutoReplayConfig{
		Dir:   f.fixturesDir,
		Delay: cfg.AutoReplayDelay,
	}, d.orch, rawToInput, log)
}

func replayRecord(ctx context.Context, d *deps, rec repository.WebhookRecord, log *slog.Logger) error {
	in, err := rawToInput(rec.RawBody)
	if err != nil {
		return fmt.Errorf("map webhook postID=%s: %w", rec.PostID, err)
	}
	in.Now = time.Now().UTC()
	log.InfoContext(ctx, "replay_record",
		slog.String("post_id", rec.PostID),
		slog.String("conversation", rec.ConvRawID),
		slog.Time("received_at", rec.ReceivedAt),
	)
	d.orch.Run(ctx, in)
	return nil
}
