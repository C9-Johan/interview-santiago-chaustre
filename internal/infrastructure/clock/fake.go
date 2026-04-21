package clock

import (
	"sync"
	"time"
)

// Fake is a test clock whose time advances only when the test calls Advance.
// Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake returns a Fake clock initialized to start.
func NewFake(start time.Time) *Fake { return &Fake{t: start} }

// Now returns the current fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Since returns the elapsed fake duration since t.
func (f *Fake) Since(t time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t.Sub(t)
}

// Advance moves the fake clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
