package debouncer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
)

func TestDebouncerFlushesBurst(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake(time.Unix(0, 0))
	var (
		mu     sync.Mutex
		flush  []domain.Turn
		signal = make(chan struct{}, 10)
	)
	d := debouncer.NewTimed(100*time.Millisecond, time.Second, fc, func(_ context.Context, turn domain.Turn) {
		mu.Lock()
		flush = append(flush, turn)
		mu.Unlock()
		signal <- struct{}{}
	})
	defer d.Stop()

	k := domain.ConversationKey("c1")
	d.Push(context.Background(), k, domain.Message{PostID: "p1", Body: "hi"})
	d.Push(context.Background(), k, domain.Message{PostID: "p2", Body: "is it open?"})
	d.Push(context.Background(), k, domain.Message{PostID: "p1", Body: "hi"}) // dup

	select {
	case <-signal:
		t.Fatal("flush must NOT fire before the window elapses")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case <-signal:
		// Timer fires via time.AfterFunc (real wall clock), then the handler runs.
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not fire within 2s of real wall clock (timer relies on real time)")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flush) != 1 {
		t.Fatalf("want 1 flush, got %d", len(flush))
	}
	if len(flush[0].Messages) != 2 {
		t.Fatalf("want 2 messages (dedup of p1), got %d", len(flush[0].Messages))
	}
}

func TestDebouncerHostCancels(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake(time.Unix(0, 0))
	var mu sync.Mutex
	flushes := 0
	d := debouncer.NewTimed(time.Second, 10*time.Second, fc, func(_ context.Context, _ domain.Turn) {
		mu.Lock()
		flushes++
		mu.Unlock()
	})
	defer d.Stop()
	k := domain.ConversationKey("c1")
	d.Push(context.Background(), k, domain.Message{PostID: "p1"})
	d.CancelIfHostReplied(k, domain.RoleHost)
	// Give the timer a chance to fire (it must not).
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 0 {
		t.Fatalf("host cancellation should drop buffer: flushes=%d", flushes)
	}
}
