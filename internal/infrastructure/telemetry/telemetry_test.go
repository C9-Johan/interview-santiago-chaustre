package telemetry_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/telemetry"
)

func TestSetupNoopWhenEndpointEmpty(t *testing.T) {
	t.Parallel()
	p, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName: "test",
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if p.Enabled() {
		t.Fatalf("want disabled when endpoint empty")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestWrapHTTPClientPreservesTimeout(t *testing.T) {
	t.Parallel()
	in := &http.Client{Timeout: 0}
	out := telemetry.WrapHTTPClient(in)
	if out == in {
		t.Fatalf("want wrapped client distinct from input")
	}
	if out.Transport == nil {
		t.Fatalf("want transport set")
	}
}

func TestWrapHTTPClientNilInput(t *testing.T) {
	t.Parallel()
	out := telemetry.WrapHTTPClient(nil)
	if out == nil || out.Transport == nil {
		t.Fatalf("want wrapped client with transport")
	}
}
