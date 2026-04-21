package budget

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeInner struct {
	calls int64
}

func (f *fakeInner) Record(_ context.Context, _, _, _ string, _, _, _ int) {
	atomic.AddInt64(&f.calls, 1)
}

type fakeToggles struct {
	mu    sync.Mutex
	flips []string
	now   bool
}

func (f *fakeToggles) SetAutoResponse(_ context.Context, enabled bool, actor string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev := f.now
	f.now = enabled
	f.flips = append(f.flips, actor)
	return prev, enabled
}

type fakeEvents struct {
	mu      sync.Mutex
	publish []any
}

func (f *fakeEvents) Publish(_ context.Context, _ string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publish = append(f.publish, payload)
}

func newWatcher(t *testing.T, cap float64) (*Watcher, *fakeInner, *fakeToggles, *fakeEvents) {
	t.Helper()
	inner := &fakeInner{}
	toggles := &fakeToggles{now: true}
	events := &fakeEvents{}
	w := New(
		Config{
			CapUSD: cap,
			UnknownModel: ModelPrice{
				PromptPer1K:     1.0,
				CompletionPer1K: 2.0,
			},
			Actor: "test_actor",
		},
		inner,
		toggles,
		events,
		nil,
		nil,
	)
	return w, inner, toggles, events
}

func TestRecordDelegatesToInnerRecorder(t *testing.T) {
	w, inner, _, _ := newWatcher(t, 0) // cap disabled
	w.Record(context.Background(), "m", "classifier", "ok", 10, 10, 20)
	w.Record(context.Background(), "m", "classifier", "error", 0, 0, 0)
	if got := atomic.LoadInt64(&inner.calls); got != 2 {
		t.Fatalf("inner.Record should be called every time, got %d", got)
	}
}

func TestRecordIgnoresNonOkOutcomesForTally(t *testing.T) {
	w, _, toggles, _ := newWatcher(t, 0.01)
	// 1M prompt tokens at $1/1K would be $1000; if "error" tallied we'd trip.
	w.Record(context.Background(), "m", "classifier", "error", 1_000_000, 0, 1_000_000)
	if got := w.Status().SpentUSD; got != 0 {
		t.Fatalf("error outcomes must not tally, spent=%v", got)
	}
	if len(toggles.flips) != 0 {
		t.Fatalf("error outcomes must not trip kill-switch, flips=%d", len(toggles.flips))
	}
}

func TestTripsWhenCapExceeded(t *testing.T) {
	w, _, toggles, events := newWatcher(t, 0.05)
	// 30 prompt tokens = 0.030, 10 completion tokens = 0.020 -> 0.050 total -> trip.
	w.Record(context.Background(), "m", "classifier", "ok", 30, 10, 40)

	if len(toggles.flips) != 1 {
		t.Fatalf("want exactly one flip, got %d", len(toggles.flips))
	}
	if toggles.flips[0] != "test_actor" {
		t.Fatalf("flip actor=%q, want test_actor", toggles.flips[0])
	}
	if toggles.now {
		t.Fatal("auto-response must be flipped off after budget trip")
	}
	if len(events.publish) != 1 {
		t.Fatalf("want exactly one budget.exceeded event, got %d", len(events.publish))
	}
	if !w.Status().Tripped {
		t.Fatal("Status.Tripped should be true after trip")
	}
}

func TestTripFiresOnceUntilDayRollover(t *testing.T) {
	w, _, toggles, _ := newWatcher(t, 0.01)
	day1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 0, 5, 0, 0, time.UTC)
	currentNow := day1
	w.WithClock(func() time.Time { return currentNow })

	// First three calls all exceed the cap; only the first should flip.
	for range 3 {
		w.Record(context.Background(), "m", "classifier", "ok", 20, 0, 20)
	}
	if len(toggles.flips) != 1 {
		t.Fatalf("want single flip within a day, got %d", len(toggles.flips))
	}

	// Advance to next UTC day; tally resets and next breach flips again.
	currentNow = day2
	w.Record(context.Background(), "m", "classifier", "ok", 20, 0, 20)
	if len(toggles.flips) != 2 {
		t.Fatalf("want a second flip after day rollover, got %d", len(toggles.flips))
	}
	if w.Status().SpentUSD < 0.019 || w.Status().SpentUSD > 0.021 {
		t.Fatalf("spend after rollover should be fresh day's bucket, got %v", w.Status().SpentUSD)
	}
}

func TestDisabledCapNeverTrips(t *testing.T) {
	w, _, toggles, events := newWatcher(t, 0)
	for range 100 {
		w.Record(context.Background(), "m", "classifier", "ok", 1000, 1000, 2000)
	}
	if len(toggles.flips) != 0 || len(events.publish) != 0 {
		t.Fatalf("cap<=0 must not trip; flips=%d events=%d", len(toggles.flips), len(events.publish))
	}
	if w.Status().SpentUSD <= 0 {
		t.Fatal("accounting must still run when cap is disabled")
	}
}

func TestNilWatcherIsSafe(t *testing.T) {
	var w *Watcher
	w.Record(context.Background(), "m", "s", "ok", 1, 1, 2)
	if got := w.Status(); got.SpentUSD != 0 || got.Tripped {
		t.Fatalf("nil watcher must return zero Status, got %+v", got)
	}
}

func TestConcurrentRecordSerialized(t *testing.T) {
	w, _, toggles, _ := newWatcher(t, 1.0) // $1 cap
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			// 100 prompt tokens = $0.10 each; 50 goroutines × $0.10 = $5.00.
			w.Record(context.Background(), "m", "classifier", "ok", 100, 0, 100)
		})
	}
	wg.Wait()
	if len(toggles.flips) != 1 {
		t.Fatalf("under concurrent record the watcher must flip exactly once, got %d", len(toggles.flips))
	}
}
