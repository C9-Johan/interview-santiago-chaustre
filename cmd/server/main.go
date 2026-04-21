// Package main wires the InquiryIQ service: config -> stores -> clients ->
// application -> transport -> HTTP server. Graceful shutdown on SIGINT/SIGTERM
// drains the debouncer and closes durable file writers before exit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/filestore"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

// defaultListingID is the demo Mockoon listing used when the webhook payload
// lacks a reservation to resolve against — pre-booking inquiries arrive before
// a Guesty reservation exists (see GUESTY_WEBHOOK_CONTRACT.md §4).
const defaultListingID = "L1"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := obs.NewLogger(os.Stdout, parseLevel(cfg.LogLevel))
	logRedactedConfig(log, cfg)

	app, err := buildApp(cfg, log)
	if err != nil {
		return err
	}
	defer app.closeStores(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.AutoReplayOnBoot {
		go startAutoReplay(ctx, cfg, app, log)
	}

	return serve(ctx, cfg, app, log)
}

type appBundle struct {
	orch          *processinquiry.UseCase
	debouncer     *debouncer.Timed
	handler       *transporthttp.Handler
	router        nethttp.Handler
	webhookStore  *filestore.Webhooks
	classStore    *filestore.Classifications
	escFileStore  *filestore.Escalations
	fixtureMapper processinquiry.FixtureMapper
	guesty        repository.GuestyClient
}

func buildApp(cfg config.Config, log *slog.Logger) (*appBundle, error) {
	stores, err := buildStores(cfg)
	if err != nil {
		return nil, err
	}
	clk := clock.NewReal()
	gclient := guesty.NewClient(cfg.GuestyBaseURL, cfg.GuestyToken, cfg.GuestyTimeout, cfg.GuestyRetries)
	llmClient := llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey)

	classifier, err := classify.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	if err != nil {
		return nil, fmt.Errorf("classify.New: %w", err)
	}
	generator := generatereply.New(llmClient, gclient, cfg.ModelGenerator, cfg.GeneratorTimeout, cfg.AgentMaxTurns)

	orch := processinquiry.New(processinquiry.Deps{
		Classifier:      classifier,
		Generator:       generator,
		Guesty:          gclient,
		Idempotency:     stores.idempotency,
		Escalations:     stores.escRing,
		Memory:          stores.memory,
		Classifications: stores.classifications,
		Toggles:         domain.Toggles{AutoResponseEnabled: cfg.AutoResponseEnabled},
		Thresholds:      decide.Thresholds{ClassifierMin: cfg.ClassifierMinConf, GeneratorMin: cfg.GeneratorMinConf},
		Log:             log,
	})

	deb := debouncer.NewTimed(cfg.DebounceWindow, cfg.DebounceMaxWait, clk, makeFlushFn(orch, gclient, log))

	handler := transporthttp.NewHandler(transporthttp.Handler{
		Webhooks:         stores.webhooks,
		EscalationsStore: stores.escRing,
		Idempotency:      stores.idempotency,
		Resolver:         identityResolver{},
		Debouncer:        deb,
		SvixSecret:       cfg.GuestyWebhookSecret,
		SvixMaxDrift:     cfg.SvixMaxClockDrift,
		Log:              log,
	})

	return &appBundle{
		orch:          orch,
		debouncer:     deb,
		handler:       handler,
		router:        transporthttp.NewRouter(handler),
		webhookStore:  stores.webhooks,
		classStore:    stores.classifications,
		escFileStore:  stores.escFile,
		fixtureMapper: fixtureMapperFromConfig(),
		guesty:        gclient,
	}, nil
}

type storeBundle struct {
	webhooks        *filestore.Webhooks
	classifications *filestore.Classifications
	escFile         *filestore.Escalations
	escRing         *memstore.EscalationRing
	idempotency     *memstore.Idempotency
	memory          *memstore.ConversationMemory
}

func buildStores(cfg config.Config) (storeBundle, error) {
	webhooks, err := filestore.NewWebhooks(cfg.DataDir)
	if err != nil {
		return storeBundle{}, fmt.Errorf("webhooks store: %w", err)
	}
	classifications, err := filestore.NewClassifications(cfg.DataDir)
	if err != nil {
		return storeBundle{}, fmt.Errorf("classifications store: %w", err)
	}
	escFile, err := filestore.NewEscalations(cfg.DataDir)
	if err != nil {
		return storeBundle{}, fmt.Errorf("escalations store: %w", err)
	}
	return storeBundle{
		webhooks:        webhooks,
		classifications: classifications,
		escFile:         escFile,
		escRing:         memstore.NewEscalationRing(500, escFile),
		idempotency:     memstore.NewIdempotency(),
		memory:          memstore.NewConversationMemory(),
	}, nil
}

func (a *appBundle) closeStores(log *slog.Logger) {
	if err := a.webhookStore.Close(); err != nil {
		log.Warn("webhook_store_close_failed", slog.String("err", err.Error()))
	}
	if err := a.classStore.Close(); err != nil {
		log.Warn("classifications_store_close_failed", slog.String("err", err.Error()))
	}
	if err := a.escFileStore.Close(); err != nil {
		log.Warn("escalations_store_close_failed", slog.String("err", err.Error()))
	}
}

func serve(ctx context.Context, cfg config.Config, app *appBundle, log *slog.Logger) error {
	srv := &nethttp.Server{
		Addr:              cfg.Port,
		Handler:           app.router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("http_listen", slog.String("addr", cfg.Port))
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		return shutdown(srv, app, log)
	case err := <-errCh:
		return err
	}
}

func shutdown(srv *nethttp.Server, app *appBundle, log *slog.Logger) error {
	log.Info("shutdown_begin")
	app.debouncer.Stop()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("shutdown_done")
	return nil
}

// makeFlushFn is the bridge from debouncer -> orchestrator. It resolves the
// current conversation snapshot from Guesty (so host-already-replied is
// re-checked), falls back to defaultListingID when the payload has no
// reservation, and invokes orch.Run synchronously on the timer goroutine.
func makeFlushFn(orch *processinquiry.UseCase, g repository.GuestyClient, log *slog.Logger) debouncer.FlushFn {
	return func(ctx context.Context, t domain.Turn) {
		convRawID := convIDFromTurn(t)
		conv, err := g.GetConversation(ctx, convRawID)
		if err != nil {
			log.WarnContext(ctx, "flush_get_conversation_failed",
				slog.String("conversation", convRawID), slog.String("err", err.Error()))
			return
		}
		if hostAlreadyReplied(conv, t) {
			log.InfoContext(ctx, "flush_host_already_replied", slog.String("conversation", convRawID))
			return
		}
		orch.Run(ctx, processinquiry.Input{
			Turn:         t,
			Conversation: conv,
			ListingID:    resolveListingID(conv),
			Now:          time.Now().UTC(),
		})
	}
}

func convIDFromTurn(t domain.Turn) string {
	// ConversationKey is the canonical id; identityResolver uses the raw id
	// directly so the key and Guesty's convID are the same string.
	return string(t.Key)
}

func hostAlreadyReplied(conv domain.Conversation, t domain.Turn) bool {
	if len(t.Messages) == 0 {
		return false
	}
	turnLast := t.Messages[len(t.Messages)-1].CreatedAt
	for i := range conv.Thread {
		m := conv.Thread[i]
		if m.Role == domain.RoleHost && m.CreatedAt.After(turnLast) {
			return true
		}
	}
	return false
}

func resolveListingID(conv domain.Conversation) string {
	if len(conv.Reservations) > 0 && conv.Reservations[0].ID != "" {
		return conv.Reservations[0].ID
	}
	return defaultListingID
}

// identityResolver treats the raw Guesty conversation id as the canonical key.
// A real multi-platform deployment would map (platform, platformID) here.
type identityResolver struct{}

func (identityResolver) Resolve(_ context.Context, c domain.Conversation) (domain.ConversationKey, error) {
	return domain.ConversationKey(c.RawID), nil
}

func fixtureMapperFromConfig() processinquiry.FixtureMapper {
	return func(raw []byte) (processinquiry.Input, error) {
		var dto transporthttp.WebhookRequestDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			return processinquiry.Input{}, fmt.Errorf("parse fixture: %w", err)
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
}

func startAutoReplay(ctx context.Context, cfg config.Config, app *appBundle, log *slog.Logger) {
	err := processinquiry.RunAutoReplay(ctx, processinquiry.AutoReplayConfig{
		Dir:   cfg.AutoReplayFixturesDir,
		Delay: cfg.AutoReplayDelay,
	}, app.orch, app.fixtureMapper, log)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("auto_replay_failed", slog.String("err", err.Error()))
	}
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func logRedactedConfig(log *slog.Logger, cfg config.Config) {
	log.Info("config_loaded",
		slog.String("port", cfg.Port),
		slog.String("log_level", cfg.LogLevel),
		slog.Bool("auto_response_enabled", cfg.AutoResponseEnabled),
		slog.String("guesty_base_url", cfg.GuestyBaseURL),
		slog.String("llm_base_url", cfg.LLMBaseURL),
		slog.String("llm_model_classifier", cfg.ModelClassifier),
		slog.String("llm_model_generator", cfg.ModelGenerator),
		slog.Duration("debounce_window", cfg.DebounceWindow),
		slog.Duration("debounce_max_wait", cfg.DebounceMaxWait),
		slog.Int("agent_max_turns", cfg.AgentMaxTurns),
		slog.String("data_dir", cfg.DataDir),
		slog.Bool("auto_replay_on_boot", cfg.AutoReplayOnBoot),
		slog.String("guesty_webhook_secret", "***"),
		slog.String("llm_api_key", "***"),
		slog.String("guesty_token", "***"),
	)
}
