package config_test

import (
	"errors"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
)

func TestLoadDefaults(t *testing.T) {
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
