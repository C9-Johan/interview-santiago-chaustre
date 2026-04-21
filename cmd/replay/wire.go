package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/langsmith"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store"
	"github.com/chaustre/inquiryiq/internal/infrastructure/telemetry"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

type deps struct {
	orch   *processinquiry.UseCase
	stores *store.Bundle
	guesty repository.GuestyClient
}

func (d *deps) close(log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.stores.LogClosers(ctx, log)
}

func buildDeps(ctx context.Context, cfg *config.Config, log *slog.Logger, f flags) (*deps, error) {
	stores, err := store.Build(ctx, cfg)
	if err != nil {
		return nil, err
	}

	ls, err := langsmith.Setup(nil, langsmith.Config{
		APIKey:      cfg.LangSmithAPIKey,
		ProjectName: cfg.LangSmithProject,
		ServiceName: cfg.OTELServiceName,
		Endpoint:    cfg.LangSmithEndpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("langsmith: %w", err)
	}
	llmClient := maybeTraceLLM(
		llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey,
			llm.WithHTTPClient(ls.WrapOpenAIHTTPClient(telemetry.WrapHTTPClient(nil))),
		),
		f.trace, log,
	)
	guestyClient := maybeNoopPost(
		guesty.NewClient(
			cfg.GuestyBaseURL, cfg.GuestyToken, cfg.GuestyTimeout, cfg.GuestyRetries,
			guesty.WithHTTPClient(telemetry.WrapHTTPClient(&nethttp.Client{Timeout: cfg.GuestyTimeout})),
		),
		f.execute, log,
	)

	classifier, err := classify.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	if err != nil {
		return nil, fmt.Errorf("classify.New: %w", err)
	}
	generator := generatereply.New(llmClient, guestyClient, cfg.ModelGenerator, cfg.GeneratorTimeout, cfg.AgentMaxTurns)

	orch := processinquiry.New(processinquiry.Deps{
		Classifier:      classifier,
		Generator:       generator,
		Guesty:          guestyClient,
		Idempotency:     stores.Idempotency,
		Escalations:     stores.Escalations,
		Memory:          stores.Memory,
		Classifications: stores.Classifications,
		Toggles:         processinquiry.StaticToggles{AutoResponseEnabled: cfg.AutoResponseEnabled},
		Thresholds:      decide.Thresholds{ClassifierMin: cfg.ClassifierMinConf, GeneratorMin: cfg.GeneratorMinConf},
		Log:             log,
	})
	return &deps{orch: orch, stores: stores, guesty: guestyClient}, nil
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
