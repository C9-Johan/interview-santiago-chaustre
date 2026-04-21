package dispatch

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func makeTurn(id string) domain.Turn {
	return domain.Turn{
		Key:        domain.ConversationKey(id),
		LastPostID: id,
		Messages:   []domain.Message{{PostID: id, Body: "hi"}},
	}
}

func TestEnqueueDrainsThroughWorkers(t *testing.T) {
	var done atomic.Int32
	p := New(Config{Workers: 3, QueueSize: 8}, func(_ context.Context, _ domain.Turn) {
		done.Add(1)
	})
	p.Start()
	defer p.Stop(context.Background())

	for i := range 5 {
		if !p.Enqueue(context.Background(), makeTurn(turnID(i)), "airbnb2") {
			t.Fatalf("enqueue %d dropped unexpectedly", i)
		}
	}
	waitFor(t, func() bool { return done.Load() == 5 }, time.Second)
}

func TestEnqueueDropsWhenQueueFull(t *testing.T) {
	var once sync.Once
	block := make(chan struct{})
	release := make(chan struct{})
	// Single worker; first job closes `block` then waits on `release`. Later
	// jobs arrive only after Stop releases them — all of the buffer is full
	// by the time we attempt the overflow enqueue.
	p := New(Config{Workers: 1, QueueSize: 2}, func(_ context.Context, _ domain.Turn) {
		once.Do(func() { close(block) })
		<-release
	})
	p.Start()
	defer func() {
		close(release)
		p.Stop(context.Background())
	}()

	if !p.Enqueue(context.Background(), makeTurn("a"), "") {
		t.Fatal("first enqueue should succeed")
	}
	<-block // worker has the job
	for i := range 2 {
		if !p.Enqueue(context.Background(), makeTurn(turnID(i+1)), "") {
			t.Fatalf("enqueue into buffer slot %d dropped", i)
		}
	}
	if p.Enqueue(context.Background(), makeTurn("overflow"), "") {
		t.Fatal("enqueue past capacity should return false")
	}
}

func TestStopDrainsPendingJobs(t *testing.T) {
	var handled atomic.Int32
	p := New(Config{Workers: 2, QueueSize: 16}, func(_ context.Context, _ domain.Turn) {
		handled.Add(1)
		time.Sleep(5 * time.Millisecond)
	})
	p.Start()

	for i := range 10 {
		p.Enqueue(context.Background(), makeTurn(turnID(i)), "")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.Stop(ctx)

	// Stop cancels the worker context, but jobs already in-flight complete.
	// The race detector and this assertion guard against leaks.
	if handled.Load() == 0 {
		t.Fatal("expected at least one job to run before Stop cancels workers")
	}
}

func TestEnqueueIsConcurrencySafe(t *testing.T) {
	var handled atomic.Int32
	p := New(Config{Workers: 4, QueueSize: 128}, func(_ context.Context, _ domain.Turn) {
		handled.Add(1)
	})
	p.Start()
	defer p.Stop(context.Background())

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.Enqueue(context.Background(), makeTurn(turnID(i)), "")
		}(i)
	}
	wg.Wait()
	waitFor(t, func() bool { return handled.Load() == 64 }, 2*time.Second)
}

func turnID(i int) string {
	return "t" + strconv.Itoa(i)
}

func waitFor(t *testing.T, cond func() bool, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", limit)
}
