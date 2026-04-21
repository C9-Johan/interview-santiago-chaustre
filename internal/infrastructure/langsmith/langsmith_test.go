package langsmith_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/langsmith"
)

func TestSetupDisabledWhenAPIKeyMissing(t *testing.T) {
	t.Parallel()
	p, err := langsmith.Setup(nil, langsmith.Config{})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if p.Enabled() {
		t.Fatalf("want disabled when api key empty")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestWrapOpenAIHTTPClientPassthroughWhenDisabled(t *testing.T) {
	t.Parallel()
	p, err := langsmith.Setup(nil, langsmith.Config{})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	base := &http.Client{}
	out := p.WrapOpenAIHTTPClient(base)
	if out != base {
		t.Fatalf("want passthrough when disabled")
	}
}
