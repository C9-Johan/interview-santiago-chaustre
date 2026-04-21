package filestore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
)

func TestWebhooksAppendAndGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := filestore.NewWebhooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rec := repository.WebhookRecord{SvixID: "sv1", PostID: "p1", RawBody: []byte(`{"x":1}`), ReceivedAt: time.Now()}
	if err := s.Append(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PostID != "p1" {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
