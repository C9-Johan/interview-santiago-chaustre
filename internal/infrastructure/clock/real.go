// Package clock provides Clock implementations used by the debouncer and by
// any code that needs deterministic time in tests.
package clock

import "time"

// Real is the production Clock backed by stdlib time.
type Real struct{}

// NewReal returns a Real clock.
func NewReal() Real { return Real{} }

// Now returns time.Now().
func (Real) Now() time.Time { return time.Now() }

// Since returns time.Since(t).
func (Real) Since(t time.Time) time.Duration { return time.Since(t) }
