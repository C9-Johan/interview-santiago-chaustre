// Package repository declares the exported contract interfaces InquiryIQ's
// application layer depends on. Concrete implementations live in
// internal/infrastructure. Interfaces here are exported because every contract
// has multiple runtime impls (real + fake/mock + v2 swap paths).
package repository

import "time"

// Clock abstracts time so debounce, memory snapshots, and idempotency TTLs
// can be tested deterministically with a fake clock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the duration elapsed since t.
	Since(t time.Time) time.Duration
}
