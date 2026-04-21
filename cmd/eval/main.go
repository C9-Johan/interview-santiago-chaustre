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
	setPath := flag.String("set", "eval/golden_set.json", "path to the golden set JSON")
	failUnder := flag.Float64("min-accuracy", 1.0, "fail if primary_accuracy < this threshold (use <1 to allow triage runs)")
	flag.Parse()

	set, err := eval.LoadGoldenSet(*setPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load golden set: %v\n", err)
		os.Exit(2)
	}

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
