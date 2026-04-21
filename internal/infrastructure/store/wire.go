// Package store selects concrete persistence backends at boot and groups
// them in a Bundle that cmd/server and cmd/replay share. Selectors come from
// config: STORE_BACKEND=file|memory|mongo and IDEMPOTENCY_BACKEND=memory|redis.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/mongostore"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/rediscache"
)

// Bundle groups every persistence dependency the application layer needs.
// Closers run in reverse registration order during Shutdown so clients close
// before the backends they depend on.
type Bundle struct {
	Webhooks        repository.WebhookStore
	Classifications repository.ClassificationStore
	Escalations     repository.EscalationStore
	Idempotency     repository.IdempotencyStore
	Memory          repository.ConversationMemoryStore

	closers []namedCloser
}

type namedCloser struct {
	name string
	fn   func(ctx context.Context) error
}

// Build resolves every backend according to cfg and returns the wired Bundle.
// It never partially opens resources: any failure closes whatever was already
// created and returns the first error.
func Build(ctx context.Context, cfg *config.Config) (*Bundle, error) {
	b := &Bundle{}
	if err := b.buildDurable(ctx, cfg); err != nil {
		_ = b.Close(ctx)
		return nil, err
	}
	if err := b.buildIdempotency(ctx, cfg); err != nil {
		_ = b.Close(ctx)
		return nil, err
	}
	return b, nil
}

// Close runs every registered closer and returns the first error.
func (b *Bundle) Close(ctx context.Context) error {
	var firstErr error
	for i := len(b.closers) - 1; i >= 0; i-- {
		c := b.closers[i]
		if err := c.fn(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", c.name, err)
		}
	}
	b.closers = nil
	return firstErr
}

// LogClosers closes every store and logs per-store failures at WARN. Callers
// that do not care about the first error use this during shutdown.
func (b *Bundle) LogClosers(ctx context.Context, log *slog.Logger) {
	for i := len(b.closers) - 1; i >= 0; i-- {
		c := b.closers[i]
		if err := c.fn(ctx); err != nil {
			log.Warn("store_close_failed", slog.String("store", c.name), slog.String("err", err.Error()))
		}
	}
	b.closers = nil
}

func (b *Bundle) buildDurable(ctx context.Context, cfg *config.Config) error {
	if strings.EqualFold(cfg.StoreBackend, "mongo") {
		return b.buildMongo(ctx, cfg)
	}
	return b.buildFile(cfg)
}

func (b *Bundle) buildFile(cfg *config.Config) error {
	webhooks, err := filestore.NewWebhooks(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("webhooks store: %w", err)
	}
	b.add("webhooks_file", func(_ context.Context) error { return webhooks.Close() })
	classifications, err := filestore.NewClassifications(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("classifications store: %w", err)
	}
	b.add("classifications_file", func(_ context.Context) error { return classifications.Close() })
	escFile, err := filestore.NewEscalations(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("escalations store: %w", err)
	}
	b.add("escalations_file", func(_ context.Context) error { return escFile.Close() })
	memory, memoryCloser, err := buildFileMemory(cfg)
	if err != nil {
		return err
	}
	b.add("conversation_memory_file", func(_ context.Context) error { return memoryCloser() })
	b.Webhooks = webhooks
	b.Classifications = classifications
	b.Escalations = memstore.NewEscalationRing(500, escFile)
	b.Memory = memory
	return nil
}

func (b *Bundle) buildMongo(ctx context.Context, cfg *config.Config) error {
	client, err := mongostore.Connect(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	b.add("mongo_client", client.Close)
	webhooks, err := mongostore.NewWebhooks(ctx, client)
	if err != nil {
		return err
	}
	classifications, err := mongostore.NewClassifications(ctx, client)
	if err != nil {
		return err
	}
	escalations, err := mongostore.NewEscalations(ctx, client)
	if err != nil {
		return err
	}
	memory, err := mongostore.NewConversationMemory(ctx, client)
	if err != nil {
		return err
	}
	b.Webhooks = webhooks
	b.Classifications = classifications
	b.Escalations = escalations
	b.Memory = memory
	return nil
}

func (b *Bundle) buildIdempotency(ctx context.Context, cfg *config.Config) error {
	if !strings.EqualFold(cfg.IdempotencyBackend, "redis") {
		b.Idempotency = memstore.NewIdempotency()
		return nil
	}
	client, err := rediscache.Connect(ctx, cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	b.add("redis_client", func(_ context.Context) error { return client.Close() })
	b.Idempotency = rediscache.NewIdempotency(client, "inquiryiq:idem", cfg.RedisIdemTTL)
	return nil
}

func (b *Bundle) add(name string, fn func(ctx context.Context) error) {
	b.closers = append(b.closers, namedCloser{name: name, fn: fn})
}

func buildFileMemory(cfg *config.Config) (repository.ConversationMemoryStore, func() error, error) {
	if strings.EqualFold(cfg.StoreBackend, "memory") {
		return memstore.NewConversationMemory(), func() error { return nil }, nil
	}
	fs, err := filestore.NewConversationMemory(cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("conversation memory store: %w", err)
	}
	return fs, fs.Close, nil
}
