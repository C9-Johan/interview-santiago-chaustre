package memstore_test

import (
	"context"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
)

func TestIdempotency(t *testing.T) {
	t.Parallel()
	s := memstore.NewIdempotency()
	ctx := context.Background()
	k := domain.ConversationKey("conv1")
	already, err := s.SeenOrClaim(ctx, k, "p1")
	if err != nil || already {
		t.Fatalf("first claim should succeed: already=%v err=%v", already, err)
	}
	already, err = s.SeenOrClaim(ctx, k, "p1")
	if err != nil || !already {
		t.Fatalf("second claim should report already=true: already=%v err=%v", already, err)
	}
	if err := s.Complete(ctx, k, "p1"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	already, _ = s.SeenOrClaim(ctx, k, "p1")
	if !already {
		t.Fatal("completed entry must still be already=true")
	}
	// different key, same postID -> separate tracking
	already, _ = s.SeenOrClaim(ctx, domain.ConversationKey("conv2"), "p1")
	if already {
		t.Fatal("distinct (key, postID) tuple must be treated independently")
	}
}
