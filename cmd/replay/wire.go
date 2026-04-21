package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

// TODO: this wiring duplicates cmd/server/main.go. Extract into
// internal/infrastructure/wire once a third call site appears.

type deps struct {
	orch         *processinquiry.UseCase
	webhooks     *filestore.Webhooks
	classes      *filestore.Classifications
	escFile      *filestore.Escalations
	escRing      *memstore.EscalationRing
	memoryCloser func() error
	guesty       repository.GuestyClient
}

func (d *deps) close(log *slog.Logger) {
	if err := d.webhooks.Close(); err != nil {
		log.Warn("webhook_store_close_failed", slog.String("err", err.Error()))
	}
	if err := d.classes.Close(); err != nil {
		log.Warn("classifications_store_close_failed", slog.String("err", err.Error()))
	}
	if err := d.escFile.Close(); err != nil {
		log.Warn("escalations_store_close_failed", slog.String("err", err.Error()))
	}
	if d.memoryCloser != nil {
		if err := d.memoryCloser(); err != nil {
			log.Warn("memory_store_close_failed", slog.String("err", err.Error()))
		}
	}
}

func buildDeps(cfg config.Config, log *slog.Logger, f flags) (*deps, error) {
	webhooks, err := filestore.NewWebhooks(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("webhooks store: %w", err)
	}
	classes, err := filestore.NewClassifications(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("classifications store: %w", err)
	}
	escFile, err := filestore.NewEscalations(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("escalations store: %w", err)
	}
	escRing := memstore.NewEscalationRing(500, escFile)
	memory, memoryCloser, err := buildMemory(cfg)
	if err != nil {
		return nil, err
	}

	llmClient := maybeTraceLLM(llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey), f.trace, log)
	guestyClient := maybeNoopPost(guesty.NewClient(cfg.GuestyBaseURL, cfg.GuestyToken, cfg.GuestyTimeout, cfg.GuestyRetries), f.execute, log)

	classifier, err := classify.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	if err != nil {
		return nil, fmt.Errorf("classify.New: %w", err)
	}
	generator := generatereply.New(llmClient, guestyClient, cfg.ModelGenerator, cfg.GeneratorTimeout, cfg.AgentMaxTurns)

	orch := processinquiry.New(processinquiry.Deps{
		Classifier:      classifier,
		Generator:       generator,
		Guesty:          guestyClient,
		Idempotency:     memstore.NewIdempotency(),
		Escalations:     escRing,
		Memory:          memory,
		Classifications: classes,
		Toggles:         domain.Toggles{AutoResponseEnabled: cfg.AutoResponseEnabled},
		Thresholds:      decide.Thresholds{ClassifierMin: cfg.ClassifierMinConf, GeneratorMin: cfg.GeneratorMinConf},
		Log:             log,
	})
	return &deps{
		orch:         orch,
		webhooks:     webhooks,
		classes:      classes,
		escFile:      escFile,
		escRing:      escRing,
		memoryCloser: memoryCloser,
		guesty:       guestyClient,
	}, nil
}

func buildMemory(cfg config.Config) (repository.ConversationMemoryStore, func() error, error) {
	if strings.EqualFold(cfg.StoreBackend, "memory") {
		return memstore.NewConversationMemory(), func() error { return nil }, nil
	}
	fs, err := filestore.NewConversationMemory(cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("conversation memory store: %w", err)
	}
	return fs, fs.Close, nil
}

func rawToInput(raw []byte) (processinquiry.Input, error) {
	var dto transporthttp.WebhookRequestDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return processinquiry.Input{}, fmt.Errorf("unmarshal webhook: %w", err)
	}
	msg, conv := transporthttp.ToDomain(dto)
	return processinquiry.Input{
		Turn: domain.Turn{
			Key:        domain.ConversationKey(conv.RawID),
			Messages:   []domain.Message{msg},
			LastPostID: msg.PostID,
		},
		Conversation: conv,
		ListingID:    resolveListingID(conv),
	}, nil
}

func resolveListingID(conv domain.Conversation) string {
	if len(conv.Reservations) > 0 && conv.Reservations[0].ID != "" {
		return conv.Reservations[0].ID
	}
	return "L1"
}

// previouslyEscalated returns the set of post IDs with a prior escalation
// record, so `--escalations-only` can filter a since-window replay.
func previouslyEscalated(ctx context.Context, escRing repository.EscalationStore) (map[string]bool, error) {
	list, err := escRing.List(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("list escalations: %w", err)
	}
	out := make(map[string]bool, len(list))
	for i := range list {
		out[list[i].PostID] = true
	}
	return out, nil
}
