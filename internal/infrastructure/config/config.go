// Package config loads environment variables into a typed Config struct,
// applying the demo-oriented defaults documented in spec §12.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the single wiring source for the service.
type Config struct {
	Port                string
	LogLevel            string
	AutoResponseEnabled bool

	GuestyWebhookSecret string
	SvixMaxClockDrift   time.Duration

	DebounceWindow  time.Duration
	DebounceMaxWait time.Duration

	GuestyBaseURL string
	GuestyToken   string
	GuestyTimeout time.Duration
	GuestyRetries int

	LLMBaseURL        string
	LLMAPIKey         string
	ModelClassifier   string
	ModelGenerator    string
	ClassifierTimeout time.Duration
	GeneratorTimeout  time.Duration
	AgentMaxTurns     int

	ClassifierMinConf   float64
	GeneratorMinConf    float64
	ThreadContextWindow int
	GuestMemoryLimit    int

	DataDir            string
	StoreBackend       string
	IdempotencyBackend string

	MongoURI      string
	MongoDatabase string

	RedisAddr     string
	RedisPassword string
	RedisIdemTTL  time.Duration

	OTELServiceName    string
	OTELServiceVersion string
	OTELEndpoint       string
	OTELInsecure       bool

	LangSmithAPIKey   string
	LangSmithProject  string
	LangSmithEndpoint string

	AutoReplayOnBoot      bool
	AutoReplayFixturesDir string
	AutoReplayDelay       time.Duration
	AutoReplayExecute     bool
}

// ErrMissingRequired is returned by Load when a required env var is unset.
var ErrMissingRequired = errors.New("missing required env var")

// Load reads environment variables, applying defaults. Required vars without
// defaults return ErrMissingRequired wrapped with the variable name.
func Load() (Config, error) {
	c := Config{
		Port:                getenv("PORT", ":8080"),
		LogLevel:            getenv("LOG_LEVEL", "info"),
		AutoResponseEnabled: getBool("AUTO_RESPONSE_ENABLED", true),

		GuestyWebhookSecret: os.Getenv("GUESTY_WEBHOOK_SECRET"),
		SvixMaxClockDrift:   getDurationSec("SVIX_MAX_CLOCK_DRIFT_SECONDS", 300),

		DebounceWindow:  getDurationMs("DEBOUNCE_WINDOW_MS", 15_000),
		DebounceMaxWait: getDurationMs("DEBOUNCE_MAX_WAIT_MS", 60_000),

		GuestyBaseURL: getenv("GUESTY_BASE_URL", "http://localhost:3001"),
		GuestyToken:   getenv("GUESTY_TOKEN", "dev"),
		GuestyTimeout: getDurationMs("GUESTY_TIMEOUT_MS", 3_000),
		GuestyRetries: getInt("GUESTY_RETRIES", 3),

		LLMBaseURL:        getenv("LLM_BASE_URL", "https://api.deepseek.com/v1"),
		LLMAPIKey:         os.Getenv("LLM_API_KEY"),
		ModelClassifier:   getenv("LLM_MODEL_CLASSIFIER", "deepseek-chat"),
		ModelGenerator:    getenv("LLM_MODEL_GENERATOR", "deepseek-chat"),
		ClassifierTimeout: getDurationMs("LLM_CLASSIFIER_TIMEOUT_MS", 30_000),
		GeneratorTimeout:  getDurationMs("LLM_GENERATOR_TIMEOUT_MS", 45_000),
		AgentMaxTurns:     getInt("LLM_AGENT_MAX_TURNS", 4),

		ClassifierMinConf:   getFloat("CONFIDENCE_CLASSIFIER_MIN", 0.65),
		GeneratorMinConf:    getFloat("CONFIDENCE_GENERATOR_MIN", 0.70),
		ThreadContextWindow: getInt("THREAD_CONTEXT_WINDOW", 10),
		GuestMemoryLimit:    getInt("GUEST_MEMORY_LIMIT", 5),

		DataDir:            getenv("DATA_DIR", "./data"),
		StoreBackend:       getenv("STORE_BACKEND", "file"),
		IdempotencyBackend: getenv("IDEMPOTENCY_BACKEND", "memory"),

		MongoURI:      os.Getenv("MONGO_URI"),
		MongoDatabase: getenv("MONGO_DATABASE", "inquiryiq"),

		RedisAddr:     os.Getenv("REDIS_ADDR"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisIdemTTL:  getDurationSec("REDIS_IDEMPOTENCY_TTL_SECONDS", 48*60*60),

		OTELServiceName:    getenv("OTEL_SERVICE_NAME", "inquiryiq"),
		OTELServiceVersion: getenv("OTEL_SERVICE_VERSION", "dev"),
		OTELEndpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OTELInsecure:       getBool("OTEL_EXPORTER_OTLP_INSECURE", true),

		LangSmithAPIKey:   os.Getenv("LANGSMITH_API_KEY"),
		LangSmithProject:  getenv("LANGSMITH_PROJECT", "inquiryiq"),
		LangSmithEndpoint: os.Getenv("LANGSMITH_ENDPOINT"),

		AutoReplayOnBoot:      getBool("AUTO_REPLAY_ON_BOOT", false),
		AutoReplayFixturesDir: getenv("AUTO_REPLAY_FIXTURES_DIR", "./fixtures/webhooks"),
		AutoReplayDelay:       getDurationMs("AUTO_REPLAY_DELAY_MS", 500),
		AutoReplayExecute:     getBool("AUTO_REPLAY_EXECUTE", false),
	}
	if c.GuestyWebhookSecret == "" {
		return c, fmt.Errorf("%w: GUESTY_WEBHOOK_SECRET", ErrMissingRequired)
	}
	if c.LLMAPIKey == "" {
		return c, fmt.Errorf("%w: LLM_API_KEY", ErrMissingRequired)
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getFloat(k string, def float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getDurationMs(k string, defMs int) time.Duration {
	return time.Duration(getInt(k, defMs)) * time.Millisecond
}

func getDurationSec(k string, defSec int) time.Duration {
	return time.Duration(getInt(k, defSec)) * time.Second
}
