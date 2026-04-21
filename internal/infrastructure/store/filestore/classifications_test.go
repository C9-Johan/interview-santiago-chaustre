package filestore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
)

func TestClassificationsPutAndGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := filestore.NewClassifications(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	cls := domain.Classification{
		PrimaryCode: domain.G1,
		Confidence:  0.9,
		Reasoning:   "ready to book",
	}
	if err := s.Put(context.Background(), "p1", cls); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PrimaryCode != domain.G1 || got.Confidence != 0.9 || got.Reasoning != "ready to book" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
