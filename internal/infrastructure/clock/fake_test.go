package clock_test

import (
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
)

func TestFake(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	c := clock.NewFake(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now: got %v, want %v", c.Now(), start)
	}
	c.Advance(5 * time.Second)
	if got := c.Since(start); got != 5*time.Second {
		t.Fatalf("Since: got %v, want 5s", got)
	}
}
