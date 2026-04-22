package config_test

import (
	"errors"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
)

// clearConfigEnv wipes every config-reading env var for the duration of a
// test so the test exercises the library's true defaults rather than what
// the caller's shell/mise/direnv happens to inject. Keep this in sync with
// the vars Load() reads.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PORT", "LOG_LEVEL", "AUTO_RESPONSE_ENABLED",
		"SVIX_MAX_CLOCK_DRIFT_SECONDS",
		"DEBOUNCE_WINDOW_MS", "DEBOUNCE_MAX_WAIT_MS",
		"GUESTY_BASE_URL", "GUESTY_TOKEN", "GUESTY_TIMEOUT_MS", "GUESTY_RETRIES",
		"LLM_BASE_URL", "LLM_MODEL_CLASSIFIER", "LLM_MODEL_GENERATOR",
		"LLM_CLASSIFIER_TIMEOUT_MS", "LLM_GENERATOR_TIMEOUT_MS", "LLM_AGENT_MAX_TURNS",
		"LLM_BUDGET_DAILY_USD",
		"CONFIDENCE_CLASSIFIER_MIN", "CONFIDENCE_GENERATOR_MIN",
		"THREAD_CONTEXT_WINDOW", "GUEST_MEMORY_LIMIT",
		"AUTO_REPLAY_ON_BOOT",
		"ADMIN_TOKEN",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("GUESTY_WEBHOOK_SECRET", "shh")
	t.Setenv("LLM_API_KEY", "sk-x")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.AutoResponseEnabled {
		t.Fatalf("AutoResponseEnabled should default to true")
	}
	if c.DebounceWindow.Milliseconds() != 15_000 {
		t.Fatalf("DebounceWindow: got %v, want 15s", c.DebounceWindow)
	}
	if c.AgentMaxTurns != 4 {
		t.Fatalf("AgentMaxTurns: got %d, want 4", c.AgentMaxTurns)
	}
	if c.GuestMemoryLimit != 5 {
		t.Fatalf("GuestMemoryLimit: got %d, want 5", c.GuestMemoryLimit)
	}
}

func TestLoadMissingSecret(t *testing.T) {
	t.Setenv("GUESTY_WEBHOOK_SECRET", "")
	t.Setenv("LLM_API_KEY", "sk-x")
	_, err := config.Load()
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("want ErrMissingRequired, got %v", err)
	}
}
