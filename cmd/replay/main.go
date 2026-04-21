// Package main is the replay CLI (spec §13.1). It reruns previously-seen
// webhooks through the full application pipeline without invoking Svix
// verification and, by default, without touching Guesty (PostNote is swapped
// for a no-op logger). Three target modes: single postId, a `--since` window
// pulled from WebhookStore, or a directory of fixture JSONs.
//
// Operator use cases: debug a specific escalation (`replay <postId>`), smoke
// test after a prompt/config change (`replay --since 1h`), reproduce a demo
// without booting the HTTP server (`replay --fixtures-dir ./fixtures/webhooks`).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

type flags struct {
	trace           bool
	execute         bool
	escalationsOnly bool
	since           time.Duration
	fixturesDir     string
	postID          string
}

func parseFlags() (flags, error) {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	var f flags
	fs.BoolVar(&f.trace, "trace", false, "log each LLM request/response frame")
	fs.BoolVar(&f.execute, "execute", false, "send PostNote to Guesty for real; default wraps it in a no-op logger")
	fs.BoolVar(&f.escalationsOnly, "escalations-only", false, "with --since, restrict to post IDs that previously escalated")
	fs.DurationVar(&f.since, "since", 0, "replay every webhook received within this duration from now")
	fs.StringVar(&f.fixturesDir, "fixtures-dir", "", "replay *.json fixtures from this directory in lexical order")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return flags{}, err
	}
	if fs.NArg() > 0 {
		f.postID = fs.Arg(0)
	}
	return f, validateFlags(f)
}

func validateFlags(f flags) error {
	count := 0
	if f.postID != "" {
		count++
	}
	if f.since > 0 {
		count++
	}
	if f.fixturesDir != "" {
		count++
	}
	if count != 1 {
		return fmt.Errorf("exactly one target is required: <postId> OR --since OR --fixtures-dir")
	}
	if f.escalationsOnly && f.since == 0 {
		return fmt.Errorf("--escalations-only requires --since")
	}
	return nil
}

func run() error {
	f, err := parseFlags()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := obs.NewLogger(os.Stdout, slog.LevelInfo)

	deps, err := buildDeps(cfg, log, f)
	if err != nil {
		return err
	}
	defer deps.close(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch {
	case f.fixturesDir != "":
		return runFixtures(ctx, cfg, deps, f, log)
	case f.since > 0:
		return runSince(ctx, deps, f, log)
	default:
		return runPostID(ctx, deps, f, log)
	}
}
