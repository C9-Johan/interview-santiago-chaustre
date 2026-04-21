// Command eval runs the golden-set regression harness against the Stage A
// classifier and exits non-zero if any case fails. Intended to run pre-merge
// (make eval) and in CI so a prompt or model change that regresses a labeled
// intent lights up before it ships.
//
// Default path is eval/golden_set.json; override with -set. The classifier is
// wired against LLM_BASE_URL / LLM_API_KEY / LLM_MODEL_CLASSIFIER so dev and
// eval runs hit the same endpoint as the server. No other config is required —
// this command does not touch Guesty, stores, or telemetry.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/eval"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

func main() {
	setPath := flag.String("set", "eval/golden_set.json", "path to a single golden set JSON (ignored when -dir is set)")
	setDir := flag.String("dir", "", "directory of per-language golden sets; enables multi-set mode")
	failUnder := flag.Float64("min-accuracy", 1.0, "fail if primary_accuracy < this threshold (use <1 to allow triage runs)")
	flag.Parse()

	baseURL := envOr("LLM_BASE_URL", "https://api.deepseek.com/v1")
	model := envOr("LLM_MODEL_CLASSIFIER", "deepseek-chat")
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "LLM_API_KEY must be set")
		os.Exit(2)
	}
	timeout := envDurMs("LLM_CLASSIFIER_TIMEOUT_MS", 30_000)

	llmClient := llm.NewClient(baseURL, apiKey)
	classifier, err := classify.New(llmClient, model, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "classify.New: %v\n", err)
		os.Exit(2)
	}

	if *setDir != "" {
		runMultiSet(classifier, *setDir, *failUnder)
		return
	}

	set, err := eval.LoadGoldenSet(*setPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load golden set: %v\n", err)
		os.Exit(2)
	}
	report := eval.Run(context.Background(), classifier, set, time.Now())
	eval.PrintReport(os.Stdout, report)
	if report.PrimaryAccuracy < *failUnder {
		fmt.Fprintf(os.Stderr, "\nprimary_accuracy %.3f < threshold %.3f\n", report.PrimaryAccuracy, *failUnder)
		os.Exit(1)
	}
	if report.Failed > 0 {
		os.Exit(1)
	}
}

// runMultiSet loads every golden set under dir and runs each separately so
// operators can tell which locale regressed. Exits non-zero if ANY set falls
// below the accuracy threshold or has a failing case — don't average across
// locales, a 100%-en + 50%-fr average would hide a real problem.
func runMultiSet(classifier *classify.UseCase, dir string, failUnder float64) {
	sets, err := eval.LoadGoldenSetDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load golden set dir: %v\n", err)
		os.Exit(2)
	}
	reports := eval.RunMany(context.Background(), classifier, sets, time.Now())
	fail := false
	for _, s := range sets {
		name := s.Description
		r := reports[name]
		fmt.Fprintf(os.Stdout, "\n=== %s ===\n", name)
		eval.PrintReport(os.Stdout, r)
		if r.PrimaryAccuracy < failUnder || r.Failed > 0 {
			fail = true
		}
	}
	if fail {
		fmt.Fprintln(os.Stderr, "\none or more locale sets failed the threshold")
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDurMs(k string, defMs int) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return time.Duration(defMs) * time.Millisecond
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return time.Duration(defMs) * time.Millisecond
	}
	return time.Duration(n) * time.Millisecond
}
