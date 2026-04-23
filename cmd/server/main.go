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

	"github.com/google/uuid"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/dispatch"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/application/qualifyreply"
	"github.com/chaustre/inquiryiq/internal/application/reviewreply"
	"github.com/chaustre/inquiryiq/internal/application/summarize"
	"github.com/chaustre/inquiryiq/internal/application/trackconversion"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/budget"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
	"github.com/chaustre/inquiryiq/internal/infrastructure/eventbus"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/langsmith"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store"
	"github.com/chaustre/inquiryiq/internal/infrastructure/telemetry"
	"github.com/chaustre/inquiryiq/internal/infrastructure/togglesource"
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
	logRedactedConfig(log, &cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tel, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:    cfg.OTELServiceName,
		ServiceVersion: cfg.OTELServiceVersion,
		Endpoint:       cfg.OTELEndpoint,
		Insecure:       cfg.OTELInsecure,
	})
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer shutdownTelemetry(tel, log)

	ls, err := langsmith.Setup(tel.SDKProvider(), langsmith.Config{
		APIKey:      cfg.LangSmithAPIKey,
		ProjectName: cfg.LangSmithProject,
		ServiceName: cfg.OTELServiceName,
		Endpoint:    cfg.LangSmithEndpoint,
	})
	if err != nil {
		return fmt.Errorf("langsmith: %w", err)
	}
	defer shutdownLangsmith(ls, log)

	app, err := buildApp(ctx, &cfg, log, tel, ls)
	if err != nil {
		return err
	}
	defer app.closeStores(log)

	if cfg.AutoReplayOnBoot {
		go startAutoReplay(ctx, &cfg, app, log)
	}

	return serve(ctx, &cfg, app, log)
}

type appBundle struct {
	orch          *processinquiry.UseCase
	debouncer     *debouncer.Timed
	pool          *dispatch.Pool
	handler       *transporthttp.Handler
	router        nethttp.Handler
	stores        *store.Bundle
	fixtureMapper processinquiry.FixtureMapper
	guesty        repository.GuestyClient
	bus           *eventbus.Bus
	budget        *budget.Watcher
	shutdownCfg   shutdownConfig
}

type shutdownConfig struct {
	dispatchTimeout time.Duration
}

func buildApp(ctx context.Context, cfg *config.Config, log *slog.Logger, tel *telemetry.Provider, ls *langsmith.Provider) (*appBundle, error) {
	stores, err := store.Build(ctx, cfg)
	if err != nil {
		return nil, err
	}
	clk := clock.NewReal()
	_ = tel // otelhttp picks up the global tracer provider set by telemetry.Setup.
	gclient := guesty.NewClient(
		cfg.GuestyBaseURL, cfg.GuestyToken, cfg.GuestyTimeout, cfg.GuestyRetries,
		guesty.WithHTTPClient(telemetry.WrapHTTPClient(&nethttp.Client{Timeout: cfg.GuestyTimeout})),
	)

	bus := eventbus.New(log, 1024)
	startLogSubscribers(ctx, bus, log)

	toggles := togglesource.New(
		domain.Toggles{AutoResponseEnabled: cfg.AutoResponseEnabled},
		log,
		tel.Counters().ToggleFlips,
	).WithEvents(bus)

	watcher := buildBudgetWatcher(cfg, tel, toggles, bus, log)
	llmClient := llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey,
		llm.WithHTTPClient(ls.WrapOpenAIHTTPClient(telemetry.WrapHTTPClient(nil))),
		llm.WithTokenRecorder(watcher),
	)

	useCases, err := buildUseCases(llmClient, gclient, cfg)
	if err != nil {
		return nil, err
	}

	tracker := trackconversion.New(stores.Conversions, telemetry.NewConversionRecorder(tel.Counters()), log)
	orch := processinquiry.New(buildOrchDeps(cfg, stores, useCases, gclient, toggles, bus, tel, tracker, log))

	pool := dispatch.New(dispatch.Config{
		Workers:      cfg.DispatchWorkers,
		QueueSize:    cfg.DispatchQueueSize,
		Log:          log,
		Accepted:     tel.Counters().DispatchAccepted,
		Dropped:      tel.Counters().DispatchDropped,
		QueueDepth:   tel.UpDowns().DispatchQueueDepth,
		QueueLatency: tel.Histograms().DispatchQueueLatency,
	}, makeFlushFn(orch, gclient, log))
	pool.Start()

	deb := debouncer.NewTimed(cfg.DebounceWindow, cfg.DebounceMaxWait, clk, enqueueFlushFn(pool, stores.Escalations, bus, log))

	handler := transporthttp.NewHandler(transporthttp.Handler{
		Webhooks:         stores.Webhooks,
		EscalationsStore: stores.Escalations,
		Idempotency:      stores.Idempotency,
		Resolver:         identityResolver{},
		Debouncer:        deb,
		SvixSecret:       cfg.GuestyWebhookSecret,
		SvixMaxDrift:     cfg.SvixMaxClockDrift,
		Log:              log,
	})
	reservationHandler := &transporthttp.ReservationHandler{
		Tracker:      tracker,
		SvixSecret:   cfg.GuestyWebhookSecret,
		SvixMaxDrift: cfg.SvixMaxClockDrift,
		Log:          log,
		Now:          func() time.Time { return time.Now().UTC() },
	}
	adminHandler := &transporthttp.AdminHandler{
		Source:          toggles,
		Budget:          watcher,
		Conversions:     stores.Conversions,
		Classifications: stores.Classifications,
		Replies:         stores.Replies,
		Escalations:     stores.Escalations,
		Reset:           stores,
		Token:           cfg.AdminToken,
		Log:             log,
	}

	return &appBundle{
		orch:          orch,
		debouncer:     deb,
		pool:          pool,
		handler:       handler,
		router:        telemetry.HTTPMiddleware(cfg.OTELServiceName, transporthttp.NewRouter(handler, reservationHandler, adminHandler)),
		stores:        stores,
		fixtureMapper: fixtureMapperFromConfig(),
		guesty:        gclient,
		bus:           bus,
		budget:        watcher,
		shutdownCfg:   shutdownConfig{dispatchTimeout: cfg.DispatchShutdownTimeout},
	}, nil
}

func (a *appBundle) closeStores(log *slog.Logger) {
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.stores.LogClosers(sctx, log)
}

func serve(ctx context.Context, cfg *config.Config, app *appBundle, log *slog.Logger) error {
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
	if app.pool != nil {
		poolCtx, poolCancel := context.WithTimeout(context.Background(), app.shutdownCfg.dispatchTimeout)
		app.pool.Stop(poolCtx)
		poolCancel()
	}
	if app.bus != nil {
		if err := app.bus.Close(); err != nil {
			log.WarnContext(context.Background(), "eventbus_close_failed", slog.String("err", err.Error()))
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("shutdown_done")
	return nil
}

// useCaseBundle groups the LLM-backed use cases so buildApp passes them by
// value instead of threading positional returns through a separate helper.
// qualifier is nil when X1_AUTOREPLY_ENABLED is false — the orchestrator
// then falls back to the spec-faithful escalate-on-X1 path.
type useCaseBundle struct {
	classifier *classify.UseCase
	generator  *generatereply.UseCase
	critic     *reviewreply.UseCase
	qualifier  *qualifyreply.UseCase
	summarizer *summarize.UseCase
}

// buildBudgetWatcher assembles the cost accountant that rides in front of
// the raw TokenRecorder. Split out of buildApp so the wiring site stays
// below the funlen gate; the watcher is entirely dependency composition.
func buildBudgetWatcher(
	cfg *config.Config,
	tel *telemetry.Provider,
	toggles *togglesource.Source,
	bus *eventbus.Bus,
	log *slog.Logger,
) *budget.Watcher {
	return budget.New(
		budget.Config{
			CapUSD: cfg.LLMBudgetDailyUSD,
			UnknownModel: budget.ModelPrice{
				PromptPer1K:     cfg.LLMPricePromptPer1K,
				CompletionPer1K: cfg.LLMPriceCompletionPer1K,
			},
			Actor: "budget_watcher",
		},
		telemetry.NewTokenRecorder(tel.Counters()),
		toggles,
		bus,
		log,
		tel.Counters().BudgetFlips,
	)
}

// buildOrchDeps assembles processinquiry.Deps from the already-constructed
// use cases, stores, and infrastructure collaborators. Extracted out of
// buildApp to keep that function under the funlen gate while keeping the
// Deps literal readable in one place.
func buildOrchDeps(
	cfg *config.Config,
	stores *store.Bundle,
	useCases useCaseBundle,
	gclient repository.GuestyClient,
	toggles *togglesource.Source,
	bus *eventbus.Bus,
	tel *telemetry.Provider,
	tracker *trackconversion.UseCase,
	log *slog.Logger,
) processinquiry.Deps {
	// Assign the qualifier via a concrete nil check so the orchestrator's
	// `Qualifier != nil` guard sees a true nil interface when the feature
	// flag is off — assigning a typed-nil *qualifyreply.UseCase directly
	// would fail that check and panic on first X1 turn.
	deps := processinquiry.Deps{
		Classifier:      useCases.classifier,
		Generator:       useCases.generator,
		Guesty:          gclient,
		Idempotency:     stores.Idempotency,
		Escalations:     stores.Escalations,
		Memory:          stores.Memory,
		Classifications: stores.Classifications,
		Replies:         stores.Replies,
		Conversions:     tracker,
		Confidence:      telemetry.NewConfidenceRecorder(tel.Histograms()),
		Critic:          useCases.critic,
		Toggles:         toggles,
		Events:          bus,
		Summarizer:      useCases.summarizer,
		MemoryLimits:    processinquiry.MemoryLimits{Cap: cfg.MemoryThreadCap, Keep: cfg.MemoryThreadKeep},
		Thresholds:      decide.Thresholds{ClassifierMin: cfg.ClassifierMinConf, GeneratorMin: cfg.GeneratorMinConf},
		Log:             log,
	}
	if useCases.qualifier != nil {
		deps.Qualifier = useCases.qualifier
	}
	return deps
}

func buildUseCases(llmClient *llm.Client, gclient repository.GuestyClient, cfg *config.Config) (useCaseBundle, error) {
	classifier, err := classify.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	if err != nil {
		return useCaseBundle{}, fmt.Errorf("classify.New: %w", err)
	}
	generator := generatereply.New(llmClient, gclient, cfg.ModelGenerator, cfg.GeneratorTimeout, cfg.AgentMaxTurns)
	critic, err := reviewreply.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	if err != nil {
		return useCaseBundle{}, fmt.Errorf("reviewreply.New: %w", err)
	}
	var qualifier *qualifyreply.UseCase
	if cfg.X1AutoReplyEnabled {
		qualifier = qualifyreply.New(llmClient, cfg.ModelGenerator, cfg.GeneratorTimeout)
	}
	summarizer := summarize.New(llmClient, cfg.ModelClassifier, cfg.ClassifierTimeout)
	return useCaseBundle{
		classifier: classifier,
		generator:  generator,
		critic:     critic,
		qualifier:  qualifier,
		summarizer: summarizer,
	}, nil
}

// enqueueFlushFn is the debouncer-facing flush callback. Instead of running
// the heavy orchestrator inline (which would block the debouncer's timer
// goroutine for the duration of every LLM call), it hands the turn to the
// worker pool. When the pool refuses the job (queue saturated) the turn is
// recorded as a backpressure_drop escalation so operators see the spike and
// every turn still terminates in exactly one of {auto-send, escalation}.
func enqueueFlushFn(pool *dispatch.Pool, escalations repository.EscalationStore, bus *eventbus.Bus, log *slog.Logger) debouncer.FlushFn {
	return func(ctx context.Context, t domain.Turn) {
		if pool.Enqueue(ctx, t, "") {
			return
		}
		if err := escalations.Record(ctx, domain.Escalation{
			ID:              uuid.NewString(),
			TraceID:         obs.TraceIDFrom(ctx),
			PostID:          t.LastPostID,
			ConversationKey: t.Key,
			CreatedAt:       time.Now().UTC(),
			Reason:          "backpressure_drop",
			Detail:          []string{"orchestrator queue saturated"},
		}); err != nil {
			log.ErrorContext(ctx, "backpressure_escalation_persist_failed",
				slog.String("err", err.Error()))
		}
		bus.Publish(ctx, eventbus.TopicBackpressureDrop, eventbus.BackpressureDropEvent{
			ConversationKey: string(t.Key),
			PostID:          t.LastPostID,
			At:              time.Now().UTC(),
		})
	}
}

// makeFlushFn is the bridge from debouncer -> orchestrator. It resolves the
// current conversation snapshot from Guesty (so host-already-replied is
// re-checked), falls back to defaultListingID when the payload has no
// reservation, and invokes orch.Run synchronously on the timer goroutine.
func makeFlushFn(orch *processinquiry.UseCase, g repository.GuestyClient, log *slog.Logger) dispatch.Handler {
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
	if len(conv.Reservations) > 0 && conv.Reservations[0].ListingID != "" {
		return conv.Reservations[0].ListingID
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

func startAutoReplay(ctx context.Context, cfg *config.Config, app *appBundle, log *slog.Logger) {
	err := processinquiry.RunAutoReplay(ctx, processinquiry.AutoReplayConfig{
		Dir:   cfg.AutoReplayFixturesDir,
		Delay: cfg.AutoReplayDelay,
	}, app.orch, app.fixtureMapper, log)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("auto_replay_failed", slog.String("err", err.Error()))
	}
}

func shutdownTelemetry(tel *telemetry.Provider, log *slog.Logger) {
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tel.Shutdown(sctx); err != nil {
		log.Warn("telemetry_shutdown_failed", slog.String("err", err.Error()))
	}
}

func shutdownLangsmith(ls *langsmith.Provider, log *slog.Logger) {
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ls.Shutdown(sctx); err != nil {
		log.Warn("langsmith_shutdown_failed", slog.String("err", err.Error()))
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

func logRedactedConfig(log *slog.Logger, cfg *config.Config) {
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
		slog.String("store_backend", cfg.StoreBackend),
		slog.String("otel_endpoint", cfg.OTELEndpoint),
		slog.String("otel_service_name", cfg.OTELServiceName),
		slog.String("langsmith_project", cfg.LangSmithProject),
		slog.Bool("langsmith_enabled", cfg.LangSmithAPIKey != ""),
		slog.Bool("auto_replay_on_boot", cfg.AutoReplayOnBoot),
		slog.String("guesty_webhook_secret", "***"),
		slog.String("llm_api_key", "***"),
		slog.String("guesty_token", "***"),
	)
}
