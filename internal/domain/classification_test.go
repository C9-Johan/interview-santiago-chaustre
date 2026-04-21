package domain_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestEnforcePriority(t *testing.T) {
	t.Parallel()
	code := func(c domain.PrimaryCode) *domain.PrimaryCode { return &c }

	cases := []struct {
		name        string
		primary     domain.PrimaryCode
		secondary   *domain.PrimaryCode
		wantPrimary domain.PrimaryCode
		wantSec     *domain.PrimaryCode
		wantSwap    bool
	}{
		{
			name:        "discount outranks parking",
			primary:     domain.Y1,
			secondary:   code(domain.R1),
			wantPrimary: domain.R1,
			wantSec:     code(domain.Y1),
			wantSwap:    true,
		},
		{
			name:        "pets rule outranks availability",
			primary:     domain.Y6,
			secondary:   code(domain.Y5),
			wantPrimary: domain.Y5,
			wantSec:     code(domain.Y6),
			wantSwap:    true,
		},
		{
			name:        "refund outranks timing",
			primary:     domain.Y4,
			secondary:   code(domain.Y2),
			wantPrimary: domain.Y2,
			wantSec:     code(domain.Y4),
			wantSwap:    true,
		},
		{
			name:        "budget outranks greeting",
			primary:     domain.X1,
			secondary:   code(domain.R2),
			wantPrimary: domain.R2,
			wantSec:     code(domain.X1),
			wantSwap:    true,
		},
		{
			name:        "ready-to-book outranks greeting",
			primary:     domain.X1,
			secondary:   code(domain.G1),
			wantPrimary: domain.G1,
			wantSec:     code(domain.X1),
			wantSwap:    true,
		},
		{
			name:        "primary already wins — parking over availability",
			primary:     domain.Y1,
			secondary:   code(domain.Y6),
			wantPrimary: domain.Y1,
			wantSec:     code(domain.Y6),
			wantSwap:    false,
		},
		{
			name:        "primary already wins — discount over parking",
			primary:     domain.R1,
			secondary:   code(domain.Y1),
			wantPrimary: domain.R1,
			wantSec:     code(domain.Y1),
			wantSwap:    false,
		},
		{
			name:        "same rank keeps order — R1 over R2",
			primary:     domain.R1,
			secondary:   code(domain.R2),
			wantPrimary: domain.R1,
			wantSec:     code(domain.R2),
			wantSwap:    false,
		},
		{
			name:        "nil secondary is no-op",
			primary:     domain.Y1,
			secondary:   nil,
			wantPrimary: domain.Y1,
			wantSec:     nil,
			wantSwap:    false,
		},
		{
			name:        "unknown secondary never promotes",
			primary:     domain.Y1,
			secondary:   code(domain.PrimaryCode("Z9")),
			wantPrimary: domain.Y1,
			wantSec:     code(domain.PrimaryCode("Z9")),
			wantSwap:    false,
		},
		{
			name:        "unknown primary demoted below any known secondary",
			primary:     domain.PrimaryCode("Z9"),
			secondary:   code(domain.Y6),
			wantPrimary: domain.Y6,
			wantSec:     code(domain.PrimaryCode("Z9")),
			wantSwap:    true,
		},
	}

	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, swapped := domain.Classification{
				PrimaryCode:   tc.primary,
				SecondaryCode: tc.secondary,
			}.EnforcePriority()
			if swapped != tc.wantSwap {
				t.Fatalf("swapped = %v, want %v", swapped, tc.wantSwap)
			}
			if got.PrimaryCode != tc.wantPrimary {
				t.Fatalf("primary = %q, want %q", got.PrimaryCode, tc.wantPrimary)
			}
			if (got.SecondaryCode == nil) != (tc.wantSec == nil) {
				t.Fatalf("secondary nil mismatch: got %v, want %v", got.SecondaryCode, tc.wantSec)
			}
			if got.SecondaryCode != nil && *got.SecondaryCode != *tc.wantSec {
				t.Fatalf("secondary = %q, want %q", *got.SecondaryCode, *tc.wantSec)
			}
		})
	}
}

// TestEnforcePriorityDoesNotMutate confirms the method returns a new value and
// leaves the caller's classification untouched — a pointer-to-secondary shared
// with callers must not alias after swap.
func TestEnforcePriorityDoesNotMutate(t *testing.T) {
	t.Parallel()
	sec := domain.R1
	orig := domain.Classification{
		PrimaryCode:   domain.Y1,
		SecondaryCode: &sec,
	}
	got, swapped := orig.EnforcePriority()
	if !swapped {
		t.Fatalf("expected swap for Y1/R1")
	}
	if orig.PrimaryCode != domain.Y1 {
		t.Fatalf("orig primary mutated: %q", orig.PrimaryCode)
	}
	if *orig.SecondaryCode != domain.R1 {
		t.Fatalf("orig secondary mutated: %q", *orig.SecondaryCode)
	}
	if got.PrimaryCode != domain.R1 || *got.SecondaryCode != domain.Y1 {
		t.Fatalf("swap result wrong: primary=%q secondary=%q",
			got.PrimaryCode, *got.SecondaryCode)
	}
}
