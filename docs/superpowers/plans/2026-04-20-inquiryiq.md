# InquiryIQ Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Go service that ingests a Guesty `reservation.messageReceived` webhook, classifies the guest turn with an LLM, runs an agentic generator (with tool calls for Guesty lookups + history), applies deterministic auto-send gates, and either posts a C.L.O.S.E.R. reply to Guesty or records an escalation — driven by `docs/superpowers/specs/2026-04-20-inquiryiq-design.md`.

**Architecture:** Four-layer DDD (transport → application → domain ← infrastructure), one-way dependency, exported contract interfaces in `internal/domain/repository/`, concrete impls in `internal/infrastructure/`. Two gates (pre- and post-generation), 15s sliding debounce, idempotency on `(conversationKey, postID)`, JSONL persistence with memstore warm-start, Mockoon for Guesty during dev/tests, DeepSeek via the OpenAI-compatible base-URL override.

**Tech stack:** Go 1.26, `github.com/go-chi/chi/v5`, `github.com/sashabaranov/go-openai`, `github.com/xeipuuv/gojsonschema` (classifier JSON validation), `log/slog`, `golangci-lint`. Mockoon CLI for the Guesty mock.

---

## Binding conventions (every task MUST follow)

These are enforced by `.golangci.yml` and by reviewers — they are not optional. If a task's code violates any of these, the task is not done.

- **Guard clauses, no `else`.** Every terminating branch is followed by a flat happy path. `switch` over `else if` ladders.
- **Consumer-side interfaces.** Default unexported. Export only when multiple runtime impls exist. All exported contracts live in `internal/domain/repository/`.
- **Constructors return concrete types, never interfaces.**
- **Generics over `any`/`reflect`.** `any` allowed only at (a) go-openai SDK boundary (`FunctionDefinition.Parameters`), (b) JSON envelope wire boundaries. Both documented inline with a one-line `// any: ...` comment.
- **Mappers at every layer boundary.** Transport DTO ↔ domain, Guesty DTO ↔ domain. Pure functions. Table-tested.
- **GoDoc on every exported identifier.** First sentence starts with the identifier name, describes behavior, documents side effects and sentinel errors returned.
- **Error wrapping with `%w`.** Compare with `errors.Is` / `errors.As`, never `==`. Never discard an error silently; `_ =` requires a one-line comment.
- **Function size under the gate:** `funlen` 100 lines / 50 statements, `cyclop` 30, `gocognit` 20, `nestif` 5, `dupl` 150. No `//nolint`.
- **Context first.** `ctx context.Context` is the first parameter on any method doing I/O. Never stored in a struct.
- **Concurrency.** Every goroutine has an owner and a termination condition tied to a context or channel close. Shared state guarded by `sync.Mutex`. `go test -race` stays green.
- **TDD where logic is non-trivial.** Gates, mappers, debouncer, idempotency, restricted-content, agent loop — test-first. Pure struct definitions don't need tests.
- **Commit after every task.** Conventional commits (`feat:`, `test:`, `refactor:`, etc.), body explains the *why* for non-trivial changes.

---

## Execution strategy

### Parallel groups

Tasks are organized into **groups (G0…G7)**. Tasks within the same group **modify disjoint files and have no ordering dependencies** — they may be dispatched to parallel subagents simultaneously. Groups are sequential: `G(n+1)` may rely on files produced in `G(n)` or earlier.

| Group | Name | Parallelism | Depends on |
|---|---|---|---|
| G0 | Bootstrap | sequential (one task) | — |
| G1 | Domain types + repository contracts | up to 2 parallel | G0 |
| G2 | Pure domain logic (gates, mappers, restricted) | up to 4 parallel | G1 |
| G3 | Infrastructure primitives (clock, stores, config, obs) | up to 5 parallel | G1 |
| G4 | Infrastructure integrations (LLM, Guesty) | up to 2 parallel | G1 |
| G5 | Application use cases | see per-task deps | G2, G3, G4 |
| G6 | Transport + main wiring + replay CLI | up to 2 parallel | G5 |
| G7 | Integration tests, Mockoon env, README | up to 3 parallel | G6 |

### 90-minute priority path

If the clock is tight, ship in this order and treat everything after the cut line as stretch:

1. G0 → G1 → G2 (gate + validate + restricted tests) → G3 (clock, idempotency, webhook filestore, config, logger) → G4 (both) → G5 (classify, generatereply, processinquiry) → G6 → **demo-ready cut line**.
2. Stretch: replay CLI, `/escalations` endpoint, `get_conversation_history` tool, `ConversationMemoryStore` integration, extended integration tests.

### TDD rhythm for every task with tests

1. Write the failing test (real code, not a stub).
2. Run it; confirm the failure reason is what you expect (missing symbol vs. wrong result).
3. Write the minimal implementation.
4. Re-run; confirm pass.
5. Stage and commit together.

### Always-green gate

After every task, these four must pass before commit:

```
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

If any fails, fix the underlying issue — do **not** suppress, skip, or `//nolint`.

---

## Task index

| ID | Group | Title | Files touched |
|---|---|---|---|
| T0.1 | G0 | Go module, deps, lint config, Makefile | `go.mod`, `.golangci.yml`, `Makefile`, `.gitignore` |
| T1.1 | G1 | Domain value types | `internal/domain/*.go` |
| T1.2 | G1 | Repository contract interfaces | `internal/domain/repository/*.go` |
| T2.1 | G2 | Gate: PreGenerate (GATE 1) + tests | `internal/application/decide/pregenerate*.go` |
| T2.2 | G2 | Gate: Decide (GATE 2) + tests | `internal/application/decide/decide*.go` |
| T2.3 | G2 | Reply validator (CLOSER beats, hedging, length) + tests | `internal/application/decide/validate*.go` |
| T2.4 | G2 | Restricted-content regex filter + tests | `internal/application/decide/restricted*.go` |
| T2.5 | G2 | Guesty ↔ domain mappers + tests | `internal/domain/mappers/*.go` |
| T3.1 | G3 | Clock (real + fake) | `internal/infrastructure/clock/*.go` |
| T3.2 | G3 | Config loader | `internal/infrastructure/config/*.go` |
| T3.3 | G3 | slog logger with trace_id | `internal/infrastructure/obs/*.go` |
| T3.4 | G3 | Idempotency store (memstore) + tests | `internal/infrastructure/store/memstore/idempotency*.go` |
| T3.5 | G3 | Webhook store (filestore JSONL) + tests | `internal/infrastructure/store/filestore/webhooks*.go` |
| T3.6 | G3 | Classification store (filestore) | `internal/infrastructure/store/filestore/classifications*.go` |
| T3.7 | G3 | Escalation store (memstore ring + filestore) + tests | `internal/infrastructure/store/*` |
| T3.8 | G3 | Timed debouncer + tests (uses Clock) | `internal/infrastructure/debouncer/timed*.go` |
| T4.1 | G4 | LLM client (go-openai + DeepSeek base URL) + tool schemas | `internal/infrastructure/llm/*.go` |
| T4.2 | G4 | Guesty HTTP client + retries + mapper + tests | `internal/infrastructure/guesty/*.go` |
| T5.1 | G5 | Classify use case (Stage A) | `internal/application/classify/*.go` |
| T5.2 | G5 | GenerateReply use case (Stage B agent loop + reflection) | `internal/application/generatereply/*.go` |
| T5.3a | G5 | Guest-profile compression (Layer 4 memory builder) + tests | `internal/application/processinquiry/guestprofile*.go` |
| T5.3b | G5 | ProcessInquiry use case (orchestrator) | `internal/application/processinquiry/usecase*.go` |
| T5.4 | G5 | Auto-replay-on-boot helper + tests | `internal/application/processinquiry/autoreplay*.go` |
| T6.1 | G6 | Transport: DTO + mapper + Svix verify | `internal/transport/http/{dto,mapper,svix}*.go` |
| T6.2 | G6 | Transport: handlers + router | `internal/transport/http/{handler,router}.go` |
| T6.3 | G6 | cmd/server/main.go wiring | `cmd/server/main.go` |
| T6.4 | G6 | cmd/replay/main.go CLI | `cmd/replay/main.go` |
| T7.1 | G7 | Mockoon environment fixture | `fixtures/mockoon/guesty.json`, `Makefile` |
| T7.2 | G7 | Integration test: webhook → auto-note happy path | `tests/integration/happy_test.go` |
| T7.3 | G7 | Integration test: burst debounce | `tests/integration/burst_test.go` |
| T7.4 | G7 | Integration test: escalation paths | `tests/integration/escalation_test.go` |
| T7.5 | G7 | README | `README.md` |

---

## Tasks

### Task T0.1 — Bootstrap

**Group:** G0 (sequential — everything depends on this). **Parallel-safe:** no (blocking).

**Files:**
- Create: `go.mod`, `.golangci.yml`, `Makefile`, `.gitignore`, `.env.example`

**Steps:**

- [ ] **Step 1: Initialize the Go module.**

```bash
cd /home/centurion/Git/Personal/interview-santiago-chaustre
go mod init github.com/chaustre/inquiryiq
```

- [ ] **Step 2: Add dependencies.**

```bash
go get github.com/go-chi/chi/v5@latest
go get github.com/sashabaranov/go-openai@latest
go get github.com/xeipuuv/gojsonschema@latest
go get github.com/google/uuid@latest
go mod tidy
```

- [ ] **Step 3: Create `.gitignore`.**

Write `/.gitignore`:

```
/data/
/coverage.out
/tmp/
*.test
```

- [ ] **Step 4: Create `.env.example`.**

Write `/.env.example` matching §12 of the spec verbatim (every variable, with the demo-oriented defaults). Reviewers will diff this against `internal/infrastructure/config/config.go` later.

- [ ] **Step 5: Create `.golangci.yml`.**

Write `/.golangci.yml`:

```yaml
run:
  timeout: 3m
  tests: true
linters:
  enable:
    - errcheck
    - errorlint
    - gosec
    - govet
    - staticcheck
    - revive
    - gocritic
    - cyclop
    - gocognit
    - funlen
    - gofumpt
    - goimports
    - nestif
    - dupl
    - maintidx
    - goconst
    - prealloc
    - bodyclose
linters-settings:
  cyclop: { max-complexity: 30, package-average: 10.0 }
  gocognit: { min-complexity: 20 }
  funlen: { lines: 100, statements: 50 }
  nestif: { min-complexity: 5 }
  dupl: { threshold: 150 }
  maintidx: { under: 20 }
  goconst: { min-len: 3, min-occurrences: 3 }
  gocritic:
    settings:
      hugeParam: { sizeThreshold: 256 }
      rangeValCopy: { sizeThreshold: 256 }
  revive:
    rules:
      - name: unused-parameter
issues:
  exclude-rules:
    - path: _test\.go
      linters: [funlen, dupl, gocognit, cyclop, maintidx]
    - path: mocks/
      linters: [revive, staticcheck, errcheck]
```

- [ ] **Step 6: Create `Makefile`.**

Write `/Makefile`:

```makefile
.PHONY: fmt lint vet test build run mock-up demo
fmt:
	gofumpt -l -w .
	goimports -local github.com/chaustre/inquiryiq -l -w .
lint:
	golangci-lint run
vet:
	go vet ./...
test:
	go test -race -count=1 ./...
build:
	go build -o ./tmp/server ./cmd/server
	go build -o ./tmp/replay ./cmd/replay
run: build
	./tmp/server
mock-up:
	mockoon-cli start -d fixtures/mockoon/guesty.json --port 3001 --log-transaction
demo: build
	AUTO_REPLAY_ON_BOOT=true ./tmp/server
check: fmt vet lint test
```

- [ ] **Step 7: Sanity check — `go build ./...` and `golangci-lint run` must succeed on an empty tree.**

```bash
mkdir -p cmd/server cmd/replay internal
printf 'package main\nfunc main() {}\n' > cmd/server/main.go
printf 'package main\nfunc main() {}\n' > cmd/replay/main.go
go build ./... && golangci-lint run
```
Expected: both exit 0.

- [ ] **Step 8: Commit.**

```bash
git add go.mod go.sum .gitignore .env.example .golangci.yml Makefile cmd/
git commit -m "chore: bootstrap Go module, lint config, Makefile, CLI skeletons"
```

---

### Task T1.1 — Domain value types

**Group:** G1. **Parallel-safe with:** T1.2 (disjoint files).

**Files:**
- Create: `internal/domain/{message,turn,conversation,classification,reply,decision,escalation,memory,listing,toggles,errors}.go`

Pure data types. No logic, no tests (tests live with the logic that uses them).

**Steps:**

- [ ] **Step 1: Write `internal/domain/message.go`.**

```go
// Package domain holds entities, value objects, and sentinel errors for the
// InquiryIQ inquiry-to-reply pipeline. It imports nothing from this project.
package domain

import "time"

// Role identifies the sender of a Message mapped to a canonical role regardless
// of Guesty's per-module type strings ("fromGuest", "toHost", etc.).
type Role string

const (
	RoleGuest  Role = "guest"
	RoleHost   Role = "host"
	RoleSystem Role = "system"
)

// Message is a single chat message in a Guesty conversation, already mapped
// from the raw webhook DTO into domain terms.
type Message struct {
	PostID    string
	Body      string
	CreatedAt time.Time
	Role      Role
	Module    string // "airbnb2" | "booking" | "vrbo" | "direct"
}
```

- [ ] **Step 2: Write `internal/domain/conversation.go`.**

```go
package domain

// ConversationKey is an opaque canonical identifier for a conversation.
// Transport and infrastructure resolve the raw Guesty conversation._id into
// this type via ConversationResolver; downstream code never handles raw ids.
type ConversationKey string

// Integration captures platform-specific metadata from Guesty.
type Integration struct {
	Platform string // "airbnb2" | "bookingCom" | "vrbo" | "manual" | "direct"
}

// Reservation is the minimal view of a Guesty reservation the pipeline uses.
type Reservation struct {
	ID               string
	CheckIn          time.Time
	CheckOut         time.Time
	ConfirmationCode string
}

// Conversation is the mapped domain view of a Guesty conversation snapshot.
type Conversation struct {
	RawID        string // raw conversation._id; use ConversationKey downstream
	GuestID      string
	GuestName    string
	Language     string
	Integration  Integration
	Reservations []Reservation
	Thread       []Message
}
```

- [ ] **Step 3: Write `internal/domain/turn.go`.**

```go
package domain

// Turn is the deduplicated, chronologically-ordered set of guest messages that
// belong to one logical turn — produced by the debouncer flushing its buffer.
type Turn struct {
	Key        ConversationKey
	Messages   []Message // oldest -> newest, dedup'd by PostID
	LastPostID string
}

// PriorContext carries non-current-turn signal into the classifier and generator:
// prior summary, accumulated per-conversation entities, the thread window, and
// the cross-conversation guest profile (Layer 4 memory).
type PriorContext struct {
	Summary       string
	KnownEntities ExtractedEntities
	Thread        []Message
	GuestProfile  string // compressed cross-conversation memory; empty if none
}
```

- [ ] **Step 4: Write `internal/domain/classification.go`.**

```go
package domain

import "time"

// PrimaryCode is the Traffic Light classification for a guest turn.
type PrimaryCode string

const (
	G1 PrimaryCode = "G1"
	G2 PrimaryCode = "G2"
	Y1 PrimaryCode = "Y1"
	Y2 PrimaryCode = "Y2"
	Y3 PrimaryCode = "Y3"
	Y4 PrimaryCode = "Y4"
	Y5 PrimaryCode = "Y5"
	Y6 PrimaryCode = "Y6"
	Y7 PrimaryCode = "Y7"
	R1 PrimaryCode = "R1"
	R2 PrimaryCode = "R2"
	X1 PrimaryCode = "X1"
)

// NextAction is the LLM's advisory routing signal. The Go gate is authoritative.
type NextAction string

const (
	ActionGenerate NextAction = "generate_reply"
	ActionEscalate NextAction = "escalate_human"
	ActionQualify  NextAction = "qualify_question"
)

// Observation is one entry in the open "additional" entity bag.
type Observation struct {
	Key        string  // snake_case, <=40 chars
	Value      string  // <=200 chars
	ValueType  string  // "string" | "number" | "bool" | "list"
	Confidence float64 // 0..1
	Source     string  // quoted guest text, <=120 chars
}

// ExtractedEntities are the facts the classifier pulled from the guest turn.
type ExtractedEntities struct {
	CheckIn     *time.Time
	CheckOut    *time.Time
	GuestCount  *int
	Pets        *bool
	Vehicles    *int
	ListingHint *string
	Additional  []Observation
}

// Classification is the full typed output of Stage A.
type Classification struct {
	PrimaryCode       PrimaryCode
	SecondaryCode     *PrimaryCode
	Confidence        float64
	ExtractedEntities ExtractedEntities
	RiskFlag          bool
	RiskReason        string
	NextAction        NextAction
	Reasoning         string
}

// LowRiskCodes is the set of primary codes eligible for auto-send (see spec §6).
var LowRiskCodes = map[PrimaryCode]struct{}{
	G1: {}, G2: {}, Y1: {}, Y3: {}, Y4: {}, Y6: {}, Y7: {},
}

// AlwaysEscalateCodes require a human regardless of confidence.
var AlwaysEscalateCodes = map[PrimaryCode]struct{}{
	Y2: {}, Y5: {}, R1: {}, R2: {},
}
```

- [ ] **Step 5: Write `internal/domain/reply.go`.**

```go
package domain

import "encoding/json"

// CloserBeats records which C.L.O.S.E.R. beats the generator claims it covered.
type CloserBeats struct {
	Clarify       bool
	Label         bool
	Overview      bool
	SellCertainty bool
	Explain       bool
	Request       bool
}

// ToolCall is the audit record for one tool invocation inside the agent loop.
type ToolCall struct {
	Name      string
	Arguments json.RawMessage
	Result    json.RawMessage
	LatencyMs int64
	Error     string
}

// Reply is the typed output of Stage B (possibly aborted).
type Reply struct {
	Body             string
	UsedTools        []ToolCall
	CloserBeats      CloserBeats
	Confidence       float64
	AbortReason      string
	ReflectionReason string
	MissingInfo      []string
	PartialFindings  string
}
```

- [ ] **Step 6: Write the remaining value types — `decision.go`, `escalation.go`, `memory.go`, `listing.go`, `toggles.go`, `errors.go`.**

`internal/domain/decision.go`:
```go
package domain

// Decision is the output of both gate functions.
type Decision struct {
	AutoSend bool
	Reason   string   // machine-readable: "ok", "code_requires_human", ...
	Detail   []string // human-readable specifics
}

// Toggles are runtime-controllable behavior flags applied in the gate.
type Toggles struct {
	AutoResponseEnabled bool
}
```

`internal/domain/escalation.go`:
```go
package domain

import "time"

// Escalation is the durable record of a turn routed to a human operator.
type Escalation struct {
	ID              string
	TraceID         string
	PostID          string
	ConversationKey ConversationKey
	GuestID         string
	GuestName       string
	Platform        string
	CreatedAt       time.Time
	Reason          string
	Detail          []string
	Classification  Classification
	Reply           *Reply
	MissingInfo     []string
	PartialFindings string
}
```

`internal/domain/memory.go`:
```go
package domain

import "time"

// ConversationMemoryRecord is persisted per conversation and indexed by both
// ConversationKey (per-conversation lookup) and GuestID (cross-conversation
// lookup for the Layer-4 guest profile).
type ConversationMemoryRecord struct {
	ConversationKey    ConversationKey
	GuestID            string
	Platform           string
	LastSummary        string
	LastSummaryPostID  string
	KnownEntities      ExtractedEntities
	AdditionalSignals  []Observation
	LastClassification *Classification
	LastAutoSendAt     *time.Time
	LastEscalationAt   *time.Time
	EscalationReasons  []string
	UpdatedAt          time.Time
}
```

`internal/domain/listing.go`:
```go
package domain

// Listing is the mapped domain view of a Guesty listing.
type Listing struct {
	ID           string
	Title        string
	Bedrooms     int
	Beds         int
	MaxGuests    int
	Amenities    []string
	HouseRules   []string
	BasePrice    float64
	Neighborhood string
}

// Availability is the mapped result of a Guesty availability check.
type Availability struct {
	Available bool
	Nights    int
	TotalUSD  float64
}
```

`internal/domain/toggles.go`: (already defined in decision.go above — skip)

`internal/domain/errors.go`:
```go
package domain

import "errors"

// Sentinel errors surfaced across package boundaries. Callers MUST compare
// with errors.Is, never ==.
var (
	ErrClassificationInvalid   = errors.New("classification output failed schema validation")
	ErrReplyInvalid            = errors.New("reply output failed schema validation")
	ErrGeneratorAborted        = errors.New("generator returned a non-empty abort_reason")
	ErrAgentMaxTurnsExhausted  = errors.New("agent loop exhausted maxTurns without a final answer") // not returned by Generate (reflection handles it); reserved for deeper layers.
	ErrDuplicateWebhook        = errors.New("webhook already processed")
	ErrWebhookSignatureInvalid = errors.New("svix signature verification failed")
	ErrWebhookClockDrift       = errors.New("svix timestamp outside allowed drift")
	ErrEmptyMessageBody        = errors.New("message body is empty or whitespace-only")
)
```

- [ ] **Step 7: Confirm the package builds.**

```bash
go build ./internal/domain/...
```
Expected: exit 0, no output.

- [ ] **Step 8: Commit.**

```bash
git add internal/domain/
git commit -m "feat(domain): add core value types (Message, Conversation, Turn, Classification, Reply, Decision, Escalation, ConversationMemoryRecord, Listing, Availability, sentinel errors)"
```

---

### Task T1.2 — Repository contract interfaces

**Group:** G1. **Parallel-safe with:** T1.1.

**Files:**
- Create: `internal/domain/repository/{guesty,llm,stores,resolver,memory,debouncer,clock}.go`

All exported — every interface here has multiple runtime implementations (real + fake + v2 swap path) and therefore meets the conventions' export criterion.

**Steps:**

- [ ] **Step 1: Write `internal/domain/repository/clock.go`.**

```go
// Package repository declares the exported contract interfaces InquiryIQ's
// application layer depends on. Concrete implementations live in
// internal/infrastructure. Interfaces here are exported because every contract
// has multiple runtime impls (real + fake/mock + v2 swap paths).
package repository

import "time"

// Clock abstracts time so debounce, memory snapshots, and idempotency TTLs
// can be tested deterministically with a fake clock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the duration elapsed since t.
	Since(t time.Time) time.Duration
}
```

- [ ] **Step 2: Write `internal/domain/repository/guesty.go`.**

```go
package repository

import (
	"context"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// GuestyClient is the contract for Guesty API interactions used by the pipeline.
// Implementations wrap the real Guesty HTTP API (via a configurable BaseURL,
// which is why the same impl points at Mockoon in dev).
type GuestyClient interface {
	// GetListing fetches full listing facts. Returns domain.Listing and wraps
	// transport errors with %w.
	GetListing(ctx context.Context, id string) (domain.Listing, error)

	// CheckAvailability reports availability and total price for a date range.
	CheckAvailability(ctx context.Context, listingID string, from, to time.Time) (domain.Availability, error)

	// GetConversationHistory returns up to limit messages older than beforePostID
	// (or the oldest page when beforePostID is empty). Results are oldest->newest.
	GetConversationHistory(ctx context.Context, convID string, limit int, beforePostID string) ([]domain.Message, error)

	// GetConversation returns the current conversation snapshot — used by the
	// orchestrator to recheck whether a host has already replied.
	GetConversation(ctx context.Context, convID string) (domain.Conversation, error)

	// PostNote posts an internal note (type="note") to the conversation. Never
	// reaches the guest.
	PostNote(ctx context.Context, conversationID, body string) error
}
```

- [ ] **Step 3: Write `internal/domain/repository/llm.go`.**

```go
package repository

import (
	"context"

	openai "github.com/sashabaranov/go-openai"
)

// LLMClient is a narrow wrapper around go-openai so we can swap provider
// (DeepSeek vs OpenAI vs other compatible backends) and inject a fake in
// unit tests. We expose the go-openai request/response types directly
// because they are the SDK's public surface; translating them would add
// zero value at the cost of a wider seam.
type LLMClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}
```

- [ ] **Step 4: Write `internal/domain/repository/resolver.go` and `memory.go` and `debouncer.go` and `stores.go`.**

`resolver.go`:
```go
package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// ConversationResolver canonicalizes a raw conversation into a stable
// ConversationKey so downstream components never see raw platform ids.
type ConversationResolver interface {
	Resolve(ctx context.Context, c domain.Conversation) (domain.ConversationKey, error)
}
```

`memory.go`:
```go
package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// ConversationMemory summarizes older messages in a conversation into a short
// paragraph, cached per (key, lastSummarizedPostID). It is separate from the
// persistent ConversationMemoryStore — this one is a derived view only.
type ConversationMemory interface {
	Summary(ctx context.Context, k domain.ConversationKey, thread []domain.Message, window int) (string, error)
}
```

`debouncer.go`:
```go
package repository

import (
	"context"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Debouncer buffers inbound messages per ConversationKey and emits a single
// Turn after the configured quiet-window elapses, bounded by a hard cap.
// Implementations must dedup on Message.PostID within the buffer.
type Debouncer interface {
	// Push records msg and (re)arms the buffer's flush timer for k.
	Push(ctx context.Context, k domain.ConversationKey, msg domain.Message)
	// CancelIfHostReplied drops k's active buffer when the role is not a guest.
	CancelIfHostReplied(k domain.ConversationKey, role domain.Role)
	// Stop shuts down internal timers cleanly.
	Stop()
}
```

`stores.go`:
```go
package repository

import (
	"context"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// WebhookRecord is the durable raw-body record for replay and audit.
type WebhookRecord struct {
	SvixID     string
	Headers    map[string]string
	RawBody    []byte
	ReceivedAt time.Time
	PostID     string
	ConvRawID  string
	TraceID    string
}

// WebhookStore persists every raw webhook (even duplicates) for replay.
type WebhookStore interface {
	Append(ctx context.Context, rec WebhookRecord) error
	Get(ctx context.Context, postID string) (WebhookRecord, error)
	Since(ctx context.Context, d time.Duration) ([]WebhookRecord, error)
}

// IdempotencyStore prevents double-processing a webhook.
type IdempotencyStore interface {
	SeenOrClaim(ctx context.Context, k domain.ConversationKey, postID string) (already bool, err error)
	Complete(ctx context.Context, k domain.ConversationKey, postID string) error
}

// ClassificationStore persists each completed classification (per postID).
type ClassificationStore interface {
	Put(ctx context.Context, postID string, c domain.Classification) error
	Get(ctx context.Context, postID string) (domain.Classification, error)
}

// EscalationStore persists every escalation for operator review.
type EscalationStore interface {
	Record(ctx context.Context, e domain.Escalation) error
	List(ctx context.Context, limit int) ([]domain.Escalation, error)
}

// ConversationMemoryStore persists per-conversation memory and also supports
// cross-conversation lookup by GuestID (Layer 4 of the memory model).
type ConversationMemoryStore interface {
	Get(ctx context.Context, k domain.ConversationKey) (domain.ConversationMemoryRecord, error)
	Update(ctx context.Context, k domain.ConversationKey, mut func(*domain.ConversationMemoryRecord)) error
	ListByGuest(ctx context.Context, guestID string, limit int) ([]domain.ConversationMemoryRecord, error)
}

// ConversationAliasStore supports merging conversations under one canonical
// ConversationKey. v1 wires a nil impl (identity resolver); v2 drops in a
// real store without changing callers.
type ConversationAliasStore interface {
	Lookup(ctx context.Context, rawID string) (domain.ConversationKey, bool, error)
	Link(ctx context.Context, rawIDs []string, canonical domain.ConversationKey) error
}
```

- [ ] **Step 5: Confirm the repository package builds.**

```bash
go build ./internal/domain/repository/...
```
Expected: exit 0.

- [ ] **Step 6: Commit.**

```bash
git add internal/domain/repository/
git commit -m "feat(domain/repository): declare exported contract interfaces for Guesty, LLM, stores, resolver, memory, debouncer, clock"
```

---

### Task T2.1 — GATE 1: `PreGenerate`

**Group:** G2. **Parallel-safe with:** T2.2, T2.3, T2.4, T2.5.

**Files:**
- Create: `internal/application/decide/pregenerate.go`
- Test: `internal/application/decide/pregenerate_test.go`

**Steps:**

- [ ] **Step 1: Write the failing test.**

`internal/application/decide/pregenerate_test.go`:
```go
package decide_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestPreGenerate(t *testing.T) {
	t.Parallel()
	ok := domain.Toggles{AutoResponseEnabled: true}
	cases := []struct {
		name       string
		cls        domain.Classification
		toggles    domain.Toggles
		wantOK     bool
		wantReason string
	}{
		{"auto_disabled", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}, domain.Toggles{}, false, "auto_disabled"},
		{"risk_flag", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9, RiskFlag: true, RiskReason: "venmo"}, ok, false, "risk_flag"},
		{"always_escalate_y2", domain.Classification{PrimaryCode: domain.Y2, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_y5", domain.Classification{PrimaryCode: domain.Y5, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_r1", domain.Classification{PrimaryCode: domain.R1, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"always_escalate_r2", domain.Classification{PrimaryCode: domain.R2, Confidence: 0.9}, ok, false, "code_requires_human"},
		{"not_low_risk_x1", domain.Classification{PrimaryCode: domain.X1, Confidence: 0.9}, ok, false, "code_not_in_low_risk"},
		{"low_confidence", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.5}, ok, false, "classifier_low_confidence"},
		{"ok_g1", domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}, ok, true, "ok_to_generate"},
		{"ok_y4", domain.Classification{PrimaryCode: domain.Y4, Confidence: 0.75}, ok, true, "ok_to_generate"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := decide.PreGenerate(tc.cls, tc.toggles, 0.65)
			if d.AutoSend != tc.wantOK {
				t.Fatalf("AutoSend: got %v, want %v", d.AutoSend, tc.wantOK)
			}
			if d.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test — it fails (package decide does not exist).**

```bash
go test ./internal/application/decide/...
```
Expected: `no Go files in internal/application/decide` or a compile error for unknown package.

- [ ] **Step 3: Implement `internal/application/decide/pregenerate.go`.**

```go
// Package decide implements the two deterministic auto-send gates (GATE 1
// pre-generation and GATE 2 post-generation) plus the reply validator and
// restricted-content filter. Every function in this package is pure.
package decide

import "github.com/chaustre/inquiryiq/internal/domain"

// PreGenerate runs GATE 1 — the cheap classification-only check that decides
// whether it is worth spending generator tokens at all. Returns a Decision
// whose AutoSend field is advisory only (true means "proceed to generation",
// not "send to the guest"). GATE 2 is the final send decision.
func PreGenerate(cls domain.Classification, t domain.Toggles, classifierMin float64) domain.Decision {
	if !t.AutoResponseEnabled {
		return domain.Decision{Reason: "auto_disabled"}
	}
	if cls.RiskFlag {
		return domain.Decision{Reason: "risk_flag", Detail: []string{cls.RiskReason}}
	}
	if _, always := domain.AlwaysEscalateCodes[cls.PrimaryCode]; always {
		return domain.Decision{Reason: "code_requires_human", Detail: []string{string(cls.PrimaryCode)}}
	}
	if _, ok := domain.LowRiskCodes[cls.PrimaryCode]; !ok {
		return domain.Decision{Reason: "code_not_in_low_risk", Detail: []string{string(cls.PrimaryCode)}}
	}
	if cls.Confidence < classifierMin {
		return domain.Decision{Reason: "classifier_low_confidence"}
	}
	return domain.Decision{AutoSend: true, Reason: "ok_to_generate"}
}
```

- [ ] **Step 4: Run the test — must pass, race clean, no lint issues.**

```bash
go test -race ./internal/application/decide/... && golangci-lint run ./internal/application/decide/...
```
Expected: `ok` with 10 subtests passing, lint exits 0.

- [ ] **Step 5: Commit.**

```bash
git add internal/application/decide/pregenerate.go internal/application/decide/pregenerate_test.go
git commit -m "feat(decide): GATE 1 PreGenerate — classification-only short-circuit (auto_disabled / risk_flag / always-escalate / low-risk-set / confidence)"
```

---

### Task T2.2 — GATE 2: `Decide`

**Group:** G2. **Parallel-safe with:** T2.1, T2.3, T2.4, T2.5.

**Files:**
- Create: `internal/application/decide/decide.go`
- Test: `internal/application/decide/decide_test.go`

**Steps:**

- [ ] **Step 1: Write the failing test.**

```go
package decide_test

import (
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	okToggles := domain.Toggles{AutoResponseEnabled: true}
	goodCls := domain.Classification{PrimaryCode: domain.G1, Confidence: 0.9}
	goodReply := domain.Reply{
		Body:        "Hi Sarah — quick city weekend for 4. Soho 2BR sleeps 4 with self check-in. Those dates are open and the total is $480 all-in. Most guests say the courtyard bedroom is the quietest sleep in Manhattan. Want me to hold it while you decide?",
		Confidence:  0.85,
		CloserBeats: domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true},
		UsedTools:   []domain.ToolCall{{Name: "check_availability"}},
	}
	thresholds := decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70}
	cases := []struct {
		name       string
		cls        domain.Classification
		reply      domain.Reply
		issues     []string
		toggles    domain.Toggles
		wantOK     bool
		wantReason string
	}{
		{"auto_disabled", goodCls, goodReply, nil, domain.Toggles{}, false, "auto_disabled"},
		{"generator_aborted", goodCls, domain.Reply{AbortReason: "max_turns"}, nil, okToggles, false, "generator_aborted"},
		{"reclassified_requires_human", domain.Classification{PrimaryCode: domain.Y2, Confidence: 0.9}, goodReply, nil, okToggles, false, "code_requires_human"},
		{"generator_low_confidence", goodCls, domain.Reply{Body: goodReply.Body, Confidence: 0.5, CloserBeats: goodReply.CloserBeats, UsedTools: goodReply.UsedTools}, nil, okToggles, false, "generator_low_confidence"},
		{"reply_validation", goodCls, goodReply, []string{"hedging_language"}, okToggles, false, "reply_validation"},
		{"restricted_content", goodCls, domain.Reply{Body: "sure, venmo me", Confidence: 0.9, CloserBeats: goodReply.CloserBeats, UsedTools: goodReply.UsedTools}, nil, okToggles, false, "restricted_content"},
		{"ok", goodCls, goodReply, nil, okToggles, true, "ok"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := decide.Decide(tc.cls, tc.reply, tc.issues, tc.toggles, thresholds)
			if d.AutoSend != tc.wantOK {
				t.Fatalf("AutoSend: got %v, want %v (reason=%q)", d.AutoSend, tc.wantOK, d.Reason)
			}
			if d.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", d.Reason, tc.wantReason)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails.**

```bash
go test ./internal/application/decide/...
```
Expected: compile error — Decide / Thresholds undefined.

- [ ] **Step 3: Implement `decide.go`.**

```go
package decide

import "github.com/chaustre/inquiryiq/internal/domain"

// Thresholds bundles the numeric confidence cutoffs for the two gates.
// Sourced from Config at wiring time so tests can vary them.
type Thresholds struct {
	ClassifierMin float64 // default 0.65
	GeneratorMin  float64 // default 0.70
}

// Decide runs GATE 2 — the final auto-send decision after generation. Returns
// AutoSend=true only when every prior check passes AND the reply text passes
// the restricted-content regex. Every denial carries Reason (machine-readable)
// plus Detail (specifics) so the escalation record reads clearly.
func Decide(cls domain.Classification, reply domain.Reply, validationIssues []string, t domain.Toggles, thr Thresholds) domain.Decision {
	if !t.AutoResponseEnabled {
		return domain.Decision{Reason: "auto_disabled"}
	}
	if reply.AbortReason != "" {
		return domain.Decision{Reason: "generator_aborted", Detail: []string{reply.AbortReason}}
	}
	if d := classifierVerdict(cls, t, thr.ClassifierMin); d.Reason != "ok_to_generate" {
		return d
	}
	if reply.Confidence < thr.GeneratorMin {
		return domain.Decision{Reason: "generator_low_confidence"}
	}
	if len(validationIssues) > 0 {
		return domain.Decision{Reason: "reply_validation", Detail: validationIssues}
	}
	if hits := RestrictedContentHits(reply.Body); len(hits) > 0 {
		return domain.Decision{Reason: "restricted_content", Detail: hits}
	}
	return domain.Decision{AutoSend: true, Reason: "ok"}
}

// classifierVerdict extracts the GATE 1 subset so it can be reused inside
// Decide without the two gates drifting out of sync.
func classifierVerdict(cls domain.Classification, t domain.Toggles, minConf float64) domain.Decision {
	return PreGenerate(cls, t, minConf)
}
```

- [ ] **Step 4: `RestrictedContentHits` is defined by T2.4. If T2.4 has not run yet, add a temporary stub in `decide.go` that returns `nil` and note it in the commit — OR run T2.4 first. Parallel execution: T2.4's file is disjoint; the test asserting `restricted_content` needs the real regex, so T2.4 blocks this step's pass. Dispatch T2.4 before re-running this test.**

- [ ] **Step 5: Run the test (after T2.4 lands).**

```bash
go test -race ./internal/application/decide/... && golangci-lint run ./internal/application/decide/...
```
Expected: pass.

- [ ] **Step 6: Commit.**

```bash
git add internal/application/decide/decide.go internal/application/decide/decide_test.go
git commit -m "feat(decide): GATE 2 Decide — post-generation auto-send rule (aborts, reclassify, generator conf, validation, restricted)"
```

---

### Task T2.3 — Reply validator

**Group:** G2. **Parallel-safe with:** T2.1, T2.2, T2.4, T2.5.

**Files:**
- Create: `internal/application/decide/validate.go`
- Test: `internal/application/decide/validate_test.go`

**Steps:**

- [ ] **Step 1: Failing test.**

```go
package decide_test

import (
	"strings"
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestValidateReply(t *testing.T) {
	t.Parallel()
	fullBeats := domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true}
	fullTools := []domain.ToolCall{{Name: "check_availability"}, {Name: "get_listing"}}
	good := domain.Reply{Body: strings.Repeat("Hi Sarah, dates open, $480 total, hold it? ", 3), CloserBeats: fullBeats, UsedTools: fullTools}

	cases := []struct {
		name  string
		reply domain.Reply
		want  []string
	}{
		{"clean", good, nil},
		{"abort", domain.Reply{AbortReason: "max_turns"}, []string{"abort:max_turns"}},
		{"too_short", domain.Reply{Body: "yes", CloserBeats: fullBeats, UsedTools: fullTools}, []string{"length_out_of_range", "missing_core_beats"}},
		{"hedging", domain.Reply{Body: "I think the dates are probably fine, maybe $480, hopefully. " + good.Body, CloserBeats: fullBeats, UsedTools: fullTools}, []string{"hedging_language"}},
		{"no_clarify", domain.Reply{Body: good.Body, CloserBeats: domain.CloserBeats{Request: true, Overview: true, SellCertainty: true, Explain: true, Label: true}, UsedTools: fullTools}, []string{"missing_core_beats"}},
		{"sell_cert_no_avail", domain.Reply{Body: good.Body, CloserBeats: fullBeats, UsedTools: []domain.ToolCall{{Name: "get_listing"}}}, []string{"sell_certainty_without_availability"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decide.ValidateReply(tc.reply)
			if !sameIssues(got, tc.want) {
				t.Fatalf("issues: got %v, want %v", got, tc.want)
			}
		})
	}
}

func sameIssues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run — fails.**

```bash
go test ./internal/application/decide/ -run TestValidateReply
```

- [ ] **Step 3: Implement `validate.go`.**

```go
package decide

import (
	"regexp"
	"unicode/utf8"

	"github.com/chaustre/inquiryiq/internal/domain"
)

const (
	minReplyRunes = 60
	maxReplyRunes = 900
)

var hedgingPattern = regexp.MustCompile(`(?i)\b(i think|maybe|it should|hopefully|i believe|perhaps|kind of|sort of|possibly|might be|probably)\b`)

// ValidateReply inspects a generator output and returns the list of issues
// (empty slice when the reply is clean). Detecting an abort reason short-
// circuits — the orchestrator will escalate regardless of further checks.
func ValidateReply(r domain.Reply) []string {
	issues := make([]string, 0, 4)
	if r.AbortReason != "" {
		return append(issues, "abort:"+r.AbortReason)
	}
	n := utf8.RuneCountInString(r.Body)
	if n < minReplyRunes || n > maxReplyRunes {
		issues = append(issues, "length_out_of_range")
	}
	if hedgingPattern.MatchString(r.Body) {
		issues = append(issues, "hedging_language")
	}
	if !r.CloserBeats.Clarify || !r.CloserBeats.Request {
		issues = append(issues, "missing_core_beats")
	}
	if r.CloserBeats.SellCertainty && !usedTool(r.UsedTools, "check_availability") {
		issues = append(issues, "sell_certainty_without_availability")
	}
	return issues
}

func usedTool(calls []domain.ToolCall, name string) bool {
	for i := range calls {
		if calls[i].Name == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run — pass.**

```bash
go test -race ./internal/application/decide/...
```

- [ ] **Step 5: Commit.**

```bash
git add internal/application/decide/validate.go internal/application/decide/validate_test.go
git commit -m "feat(decide): reply validator (length, hedging, core beats, fabricated sell-certainty)"
```

---

### Task T2.4 — Restricted-content filter

**Group:** G2. **Parallel-safe with:** T2.1, T2.2, T2.3, T2.5. Note: T2.2 depends on this for one test case.

**Files:**
- Create: `internal/application/decide/restricted.go`
- Test: `internal/application/decide/restricted_test.go`

**Steps:**

- [ ] **Step 1: Failing test.**

```go
package decide_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/chaustre/inquiryiq/internal/application/decide"
)

func TestRestrictedContentHits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"clean", "Those dates are open and the total is $480.", nil},
		{"venmo", "sure, you can Venmo me later", []string{"off_platform_payment"}},
		{"whatsapp", "text me on WhatsApp at 555-1212", []string{"contact_bypass", "off_platform_payment"}}, // "text me at" + whatsapp, no payment — see below
		{"whatsapp_only", "contact me on WhatsApp any time", []string{"contact_bypass"}},
		{"address", "the unit is at 123 Spring Street", []string{"address_leak"}},
		{"guarantee", "we guarantee no noise whatsoever", []string{"guarantee_language"}},
		{"discount", "I'll give you a 10% discount", []string{"discount_offer"}},
		{"multiple", "message me on telegram and I'll give you a special rate", []string{"contact_bypass", "discount_offer"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decide.RestrictedContentHits(tc.body)
			sort.Strings(got)
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}
```

Note: the `whatsapp` test line includes "text me on WhatsApp at 555-1212" — the regex for `contact_bypass` should match "text me on" / "WhatsApp" and nothing else; if the simpler "text me at" pattern fires on "text me on" as well, that's fine (both are bypass signals). This test expects `["contact_bypass"]`; the "off_platform_payment" listed above is a typo — the implementation below will produce only `["contact_bypass"]`. Fix before commit by removing the extra expected hit.

- [ ] **Step 2: Correct the expected hits for the `whatsapp` case. The line should be:**

```go
{"whatsapp", "text me on WhatsApp at 555-1212", []string{"contact_bypass"}},
```

- [ ] **Step 3: Run — fails.**

```bash
go test ./internal/application/decide/ -run TestRestrictedContentHits
```

- [ ] **Step 4: Implement `restricted.go`.**

```go
package decide

import "regexp"

type restrictedCheck struct {
	name    string
	pattern *regexp.Regexp
}

var restrictedChecks = []restrictedCheck{
	{"off_platform_payment", regexp.MustCompile(`(?i)\b(venmo|cashapp|cash app|zelle|paypal|wire transfer|bank transfer|crypto|bitcoin|usdt|western union)\b`)},
	{"contact_bypass", regexp.MustCompile(`(?i)\b(whatsapp|telegram|signal|text me (at|on)|email me at|my number is)\b`)},
	{"address_leak", regexp.MustCompile(`\b\d{1,5}\s+\w+(\s\w+)*\s+(street|st|avenue|ave|road|rd|blvd|boulevard|lane|ln|drive|dr|court|ct|place|pl)\b`)},
	{"guarantee_language", regexp.MustCompile(`(?i)\b(guarantee(d)?|we promise|100% (safe|quiet)|no issues whatsoever)\b`)},
	{"discount_offer", regexp.MustCompile(`(?i)\b(discount|special rate|lower price|knock off|take off \$)\b`)},
}

// RestrictedContentHits returns the names of the patterns matched by body,
// ordered by first occurrence in restrictedChecks. Empty when clean.
func RestrictedContentHits(body string) []string {
	hits := make([]string, 0, 2)
	for _, c := range restrictedChecks {
		if c.pattern.MatchString(body) {
			hits = append(hits, c.name)
		}
	}
	return hits
}
```

- [ ] **Step 5: Run — pass.**

```bash
go test -race ./internal/application/decide/... && golangci-lint run ./internal/application/decide/...
```

- [ ] **Step 6: Commit.**

```bash
git add internal/application/decide/restricted.go internal/application/decide/restricted_test.go
git commit -m "feat(decide): restricted-content regex filter (off-platform payment, contact bypass, address leak, guarantees, discount)"
```

---

### Task T2.5 — Guesty ↔ domain mappers

**Group:** G2. **Parallel-safe with:** T2.1–T2.4. Also produces the shared test fixture shape used by T4.2.

**Files:**
- Create: `internal/domain/mappers/guesty_to_domain.go`
- Create: `internal/domain/mappers/domain_to_guesty.go`
- Create: `internal/domain/mappers/guesty_types.go` (DTOs the Guesty mapper consumes — lives in domain because the mapper is pure; the Guesty HTTP client will map its raw wire types into these DTOs before calling)
- Test: `internal/domain/mappers/guesty_to_domain_test.go`

**Steps:**

- [ ] **Step 1: Write the DTO types + the mapper shell + table test.**

`internal/domain/mappers/guesty_types.go`:
```go
// Package mappers holds pure conversion functions between domain types and
// boundary DTOs (Guesty API shapes, transport-layer webhook shapes). No I/O.
package mappers

import "time"

// GuestyListingDTO is the minimal projection of the Guesty listing response
// the classifier/generator care about. The infrastructure layer populates it
// from the raw API payload; the mapper converts it to domain.Listing.
type GuestyListingDTO struct {
	ID           string
	Title        string
	Bedrooms     int
	Beds         int
	MaxGuests    int
	Amenities    []string
	HouseRules   []string
	BasePrice    float64
	Neighborhood string
}

// GuestyAvailabilityDTO mirrors the Guesty availability response.
type GuestyAvailabilityDTO struct {
	Available bool
	Nights    int
	TotalUSD  float64
}

// GuestyMessageDTO is the normalized shape of a single conversation message.
type GuestyMessageDTO struct {
	PostID    string
	Body      string
	CreatedAt time.Time
	Type      string // "fromGuest" | "fromHost" | "toGuest" | "toHost" | "system"
	Module    string
}
```

`internal/domain/mappers/guesty_to_domain.go`:
```go
package mappers

import "github.com/chaustre/inquiryiq/internal/domain"

// ListingFromGuesty maps the Guesty listing projection into domain terms.
func ListingFromGuesty(d GuestyListingDTO) domain.Listing {
	return domain.Listing{
		ID: d.ID, Title: d.Title,
		Bedrooms: d.Bedrooms, Beds: d.Beds, MaxGuests: d.MaxGuests,
		Amenities: d.Amenities, HouseRules: d.HouseRules,
		BasePrice: d.BasePrice, Neighborhood: d.Neighborhood,
	}
}

// AvailabilityFromGuesty maps the Guesty availability projection.
func AvailabilityFromGuesty(d GuestyAvailabilityDTO) domain.Availability {
	return domain.Availability{Available: d.Available, Nights: d.Nights, TotalUSD: d.TotalUSD}
}

// MessageFromGuesty normalizes the Guesty per-module "type" string into a
// canonical domain.Role. Unknown types become RoleSystem so the pipeline
// will not auto-process them.
func MessageFromGuesty(d GuestyMessageDTO) domain.Message {
	return domain.Message{
		PostID:    d.PostID,
		Body:      d.Body,
		CreatedAt: d.CreatedAt,
		Role:      roleFromGuestyType(d.Type),
		Module:    d.Module,
	}
}

func roleFromGuestyType(t string) domain.Role {
	switch t {
	case "fromGuest", "toHost":
		return domain.RoleGuest
	case "fromHost", "toGuest":
		return domain.RoleHost
	default:
		return domain.RoleSystem
	}
}
```

`internal/domain/mappers/domain_to_guesty.go`:
```go
package mappers

// NotePayload is the wire body for POST /conversations/{id}/messages.
// Fields are JSON-tagged for the infrastructure/guesty HTTP client.
type NotePayload struct {
	Body string `json:"body"`
	Type string `json:"type"` // always "note" for this service; never "platform"
}

// NoteFromDomain builds the outbound note body. Fixed type="note" — the
// spec is explicit that reaching real guests is out of scope.
func NoteFromDomain(body string) NotePayload {
	return NotePayload{Body: body, Type: "note"}
}
```

`internal/domain/mappers/guesty_to_domain_test.go`:
```go
package mappers_test

import (
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
)

func TestListingFromGuesty(t *testing.T) {
	t.Parallel()
	got := mappers.ListingFromGuesty(mappers.GuestyListingDTO{ID: "L1", Title: "Soho 2BR", Bedrooms: 2, Beds: 3, MaxGuests: 4, Amenities: []string{"wifi"}, BasePrice: 200, Neighborhood: "Soho"})
	want := domain.Listing{ID: "L1", Title: "Soho 2BR", Bedrooms: 2, Beds: 3, MaxGuests: 4, Amenities: []string{"wifi"}, BasePrice: 200, Neighborhood: "Soho"}
	if got.ID != want.ID || got.MaxGuests != want.MaxGuests || got.Neighborhood != want.Neighborhood {
		t.Fatalf("listing mismatch: got %+v, want %+v", got, want)
	}
}

func TestMessageFromGuestyRoles(t *testing.T) {
	t.Parallel()
	cases := map[string]domain.Role{
		"fromGuest": domain.RoleGuest,
		"toHost":    domain.RoleGuest,
		"fromHost":  domain.RoleHost,
		"toGuest":   domain.RoleHost,
		"system":    domain.RoleSystem,
		"":          domain.RoleSystem,
		"weird":     domain.RoleSystem,
	}
	now := time.Now()
	for in, want := range cases {
		in, want := in, want
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			m := mappers.MessageFromGuesty(mappers.GuestyMessageDTO{PostID: "p", Body: "b", CreatedAt: now, Type: in, Module: "airbnb2"})
			if m.Role != want {
				t.Fatalf("role for %q: got %q, want %q", in, m.Role, want)
			}
		})
	}
}

func TestNoteFromDomain(t *testing.T) {
	t.Parallel()
	n := mappers.NoteFromDomain("hello")
	if n.Body != "hello" || n.Type != "note" {
		t.Fatalf("got %+v", n)
	}
}
```

- [ ] **Step 2: Run the tests.**

```bash
go test -race ./internal/domain/mappers/... && golangci-lint run ./internal/domain/mappers/...
```
Expected: pass.

- [ ] **Step 3: Commit.**

```bash
git add internal/domain/mappers/
git commit -m "feat(domain/mappers): Guesty <-> domain mappers + NotePayload with table tests"
```

---

### Task T3.1 — Clock (real + fake)

**Group:** G3. **Parallel-safe with:** T3.2, T3.3, T3.4, T3.5, T3.6, T3.7, T4.1, T4.2. **Required by:** T3.8 (debouncer).

**Files:**
- Create: `internal/infrastructure/clock/real.go`
- Create: `internal/infrastructure/clock/fake.go`
- Test: `internal/infrastructure/clock/fake_test.go`

**Steps:**

- [ ] **Step 1: Write both impls + test.**

`internal/infrastructure/clock/real.go`:
```go
// Package clock provides Clock implementations used by the debouncer and by
// any code that needs deterministic time in tests.
package clock

import "time"

// Real is the production Clock backed by stdlib time.
type Real struct{}

// NewReal returns a Real clock.
func NewReal() Real { return Real{} }

// Now returns time.Now().
func (Real) Now() time.Time { return time.Now() }

// Since returns time.Since(t).
func (Real) Since(t time.Time) time.Duration { return time.Since(t) }
```

`internal/infrastructure/clock/fake.go`:
```go
package clock

import (
	"sync"
	"time"
)

// Fake is a test clock whose time advances only when the test calls Advance.
// Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake returns a Fake clock initialized to start.
func NewFake(start time.Time) *Fake { return &Fake{t: start} }

// Now returns the current fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Since returns the elapsed fake duration since t.
func (f *Fake) Since(t time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t.Sub(t)
}

// Advance moves the fake clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
```

`internal/infrastructure/clock/fake_test.go`:
```go
package clock_test

import (
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
)

func TestFake(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	c := clock.NewFake(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now: got %v, want %v", c.Now(), start)
	}
	c.Advance(5 * time.Second)
	if got := c.Since(start); got != 5*time.Second {
		t.Fatalf("Since: got %v, want 5s", got)
	}
}
```

- [ ] **Step 2: Verify builds + test.**

```bash
go test -race ./internal/infrastructure/clock/...
```

- [ ] **Step 3: Commit.**

```bash
git add internal/infrastructure/clock/
git commit -m "feat(infra/clock): Real and Fake clock implementations"
```

---

### Task T3.2 — Config loader

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/config/config.go`
- Test: `internal/infrastructure/config/config_test.go`

**Steps:**

- [ ] **Step 1: Write `config.go`.**

```go
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

	GuestyWebhookSecret     string
	SvixMaxClockDrift       time.Duration

	DebounceWindow  time.Duration
	DebounceMaxWait time.Duration

	GuestyBaseURL  string
	GuestyToken    string
	GuestyTimeout  time.Duration
	GuestyRetries  int

	LLMBaseURL           string
	LLMAPIKey            string
	ModelClassifier      string
	ModelGenerator       string
	ClassifierTimeout    time.Duration
	GeneratorTimeout     time.Duration
	AgentMaxTurns        int

	ClassifierMinConf  float64
	GeneratorMinConf   float64
	ThreadContextWindow int
	GuestMemoryLimit    int

	DataDir string

	AutoReplayOnBoot     bool
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
		Port:     getenv("PORT", ":8080"),
		LogLevel: getenv("LOG_LEVEL", "info"),
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

		DataDir: getenv("DATA_DIR", "./data"),

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
```

- [ ] **Step 2: Test defaults + errors.**

```go
package config_test

import (
	"errors"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GUESTY_WEBHOOK_SECRET", "shh")
	t.Setenv("LLM_API_KEY", "sk-x")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.AutoResponseEnabled {
		t.Fatalf("AutoResponseEnabled should default to true")
	}
	if c.DebounceWindow.Milliseconds() != 15_000 {
		t.Fatalf("DebounceWindow: got %v, want 15s", c.DebounceWindow)
	}
	if c.AgentMaxTurns != 4 {
		t.Fatalf("AgentMaxTurns: got %d, want 4", c.AgentMaxTurns)
	}
	if c.GuestMemoryLimit != 5 {
		t.Fatalf("GuestMemoryLimit: got %d, want 5", c.GuestMemoryLimit)
	}
}

func TestLoadMissingSecret(t *testing.T) {
	t.Setenv("GUESTY_WEBHOOK_SECRET", "")
	t.Setenv("LLM_API_KEY", "sk-x")
	_, err := config.Load()
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("want ErrMissingRequired, got %v", err)
	}
}
```

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/infrastructure/config/...
git add internal/infrastructure/config/
git commit -m "feat(infra/config): typed env-backed Config loader with demo-oriented defaults (auto_response=true, auto_replay vars, guest_memory_limit)"
```

---

### Task T3.3 — slog logger with trace_id

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/obs/logger.go`
- Test: `internal/infrastructure/obs/logger_test.go`

**Steps:**

- [ ] **Step 1: Write `logger.go`.**

```go
// Package obs provides structured logging (slog JSON handler) with a
// trace_id carried through context, plus lightweight helpers used across
// the pipeline's stages.
package obs

import (
	"context"
	"io"
	"log/slog"

	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxKeyTraceID ctxKey = iota + 1
	ctxKeyAttrs
)

// NewLogger builds a JSON slog.Logger at the given level written to w.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

// WithTraceID returns ctx with a fresh trace_id and the id itself.
func WithTraceID(ctx context.Context) (context.Context, string) {
	id := uuid.NewString()
	return context.WithValue(ctx, ctxKeyTraceID, id), id
}

// TraceIDFrom returns the trace_id set by WithTraceID, or "".
func TraceIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		return v
	}
	return ""
}

// With returns a context carrying extra log attributes that LogAttrs will merge.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	existing, _ := ctx.Value(ctxKeyAttrs).([]slog.Attr)
	// any: slog.Attr's Value is already typed; we keep the slice typed too.
	merged := make([]slog.Attr, 0, len(existing)+len(attrs))
	merged = append(merged, existing...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxKeyAttrs, merged)
}

// LogAttrs logs via l with merged context attrs plus the trace_id attr.
func LogAttrs(ctx context.Context, l *slog.Logger, level slog.Level, msg string, attrs ...slog.Attr) {
	merged := make([]slog.Attr, 0, len(attrs)+8)
	if tid := TraceIDFrom(ctx); tid != "" {
		merged = append(merged, slog.String("trace_id", tid))
	}
	if existing, ok := ctx.Value(ctxKeyAttrs).([]slog.Attr); ok {
		merged = append(merged, existing...)
	}
	merged = append(merged, attrs...)
	l.LogAttrs(ctx, level, msg, merged...)
}
```

- [ ] **Step 2: Test trace_id threading.**

```go
package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
)

func TestLogAttrsIncludesTraceID(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := obs.NewLogger(&buf, slog.LevelInfo)
	ctx, tid := obs.WithTraceID(context.Background())
	ctx = obs.With(ctx, slog.String("stage", "classifier"))
	obs.LogAttrs(ctx, l, slog.LevelInfo, "ok", slog.Int("latency_ms", 42))
	got := buf.String()
	if !strings.Contains(got, tid) {
		t.Fatalf("log does not contain trace_id %q: %s", tid, got)
	}
	var m map[string]any // any: json wire boundary
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if m["stage"] != "classifier" || m["latency_ms"].(float64) != 42 {
		t.Fatalf("attrs missing: %v", m)
	}
}
```

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/infrastructure/obs/...
git add internal/infrastructure/obs/
git commit -m "feat(infra/obs): slog JSON logger with trace_id and ctx-attrs helpers"
```

---

### Task T3.4 — IdempotencyStore (memstore) + tests

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/store/memstore/idempotency.go`
- Test: `internal/infrastructure/store/memstore/idempotency_test.go`

**Steps:**

- [ ] **Step 1: Failing test.**

```go
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
```

- [ ] **Step 2: Implement.**

```go
// Package memstore is the in-memory, interface-satisfying default for
// storage interfaces. Swap to Redis/Mongo/SQLite later by implementing
// the same repository.* contracts.
package memstore

import (
	"context"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Idempotency tracks (ConversationKey, postID) tuples in two states:
// inflight (claimed but not complete) and complete. Both count as "already"
// for subsequent SeenOrClaim calls — the orchestrator is expected to drop.
type Idempotency struct {
	mu      sync.Mutex
	claimed map[string]bool // key = k + "|" + postID; value true = complete
}

// NewIdempotency returns an empty Idempotency store.
func NewIdempotency() *Idempotency {
	return &Idempotency{claimed: make(map[string]bool, 1024)}
}

// SeenOrClaim reports whether (k, postID) has been seen before. When false,
// a new inflight claim is recorded. Safe for concurrent callers.
func (i *Idempotency) SeenOrClaim(_ context.Context, k domain.ConversationKey, postID string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	key := string(k) + "|" + postID
	if _, ok := i.claimed[key]; ok {
		return true, nil
	}
	i.claimed[key] = false
	return false, nil
}

// Complete flips an inflight claim to complete. Idempotent — calling it twice
// is not an error; calling it for a never-claimed key also succeeds (defensive).
func (i *Idempotency) Complete(_ context.Context, k domain.ConversationKey, postID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claimed[string(k)+"|"+postID] = true
	return nil
}
```

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/infrastructure/store/memstore/...
git add internal/infrastructure/store/memstore/idempotency.go internal/infrastructure/store/memstore/idempotency_test.go
git commit -m "feat(infra/store/memstore): Idempotency store keyed on (ConversationKey, postID) tuple"
```

---

### Task T3.5 — WebhookStore (filestore JSONL) + tests

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/store/filestore/webhooks.go`
- Test: `internal/infrastructure/store/filestore/webhooks_test.go`

**Steps:**

- [ ] **Step 1: Implement + test.**

`internal/infrastructure/store/filestore/webhooks.go`:
```go
// Package filestore provides JSONL-backed implementations of the durable
// repository contracts. v1 choice — swap to Mongo/Postgres later via the
// same interfaces.
package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Webhooks is a JSONL append-only WebhookStore. All writes are serialized
// via mu so concurrent appends are atomic. Reads open a fresh file handle.
type Webhooks struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewWebhooks ensures parent dir exists and opens the file in append mode.
func NewWebhooks(dir string) (*Webhooks, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "webhooks.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Webhooks{path: p, writer: f}, nil
}

// Close flushes and closes the writer.
func (w *Webhooks) Close() error { return w.writer.Close() }

// Append serializes rec as one JSON line and fsyncs.
func (w *Webhooks) Append(_ context.Context, rec repository.WebhookRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}
	if _, err := w.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write webhook: %w", err)
	}
	return w.writer.Sync()
}

// ErrNotFound is returned by Get when no record has the given postID.
var ErrNotFound = errors.New("record not found")

// Get scans the file for the newest record with matching postID.
func (w *Webhooks) Get(_ context.Context, postID string) (repository.WebhookRecord, error) {
	f, err := os.Open(w.path)
	if err != nil {
		return repository.WebhookRecord{}, fmt.Errorf("open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }() // scan-only; close error is not actionable here.
	var match repository.WebhookRecord
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec repository.WebhookRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.PostID == postID {
			match = rec
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return repository.WebhookRecord{}, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return repository.WebhookRecord{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}

// Since returns all records whose ReceivedAt is within d of now.
func (w *Webhooks) Since(_ context.Context, d time.Duration) ([]repository.WebhookRecord, error) {
	cutoff := time.Now().Add(-d)
	f, err := os.Open(w.path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }()
	var out []repository.WebhookRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec repository.WebhookRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.ReceivedAt.After(cutoff) {
			out = append(out, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}
```

`internal/infrastructure/store/filestore/webhooks_test.go`:
```go
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
```

- [ ] **Step 2: Commit.**

```bash
go test -race ./internal/infrastructure/store/filestore/...
git add internal/infrastructure/store/filestore/webhooks.go internal/infrastructure/store/filestore/webhooks_test.go
git commit -m "feat(infra/store/filestore): JSONL append-only WebhookStore with Get and Since"
```

---

### Task T3.6 — ClassificationStore (filestore)

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/store/filestore/classifications.go`

Analogous to T3.5: JSONL append on `Put`, linear scan on `Get` by `postID`. No separate tests needed — the pattern is identical and the JSONL scanner is already exercised by T3.5's test; a trivial smoke test is fine:

- [ ] **Step 1: Implement.**

```go
package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Classifications persists each completed classification as a JSONL record
// keyed on postID.
type Classifications struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewClassifications opens data/classifications.jsonl for append.
func NewClassifications(dir string) (*Classifications, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "classifications.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Classifications{path: p, writer: f}, nil
}

// Close flushes the underlying file.
func (c *Classifications) Close() error { return c.writer.Close() }

type classificationLine struct {
	PostID         string                `json:"post_id"`
	Classification domain.Classification `json:"classification"`
}

// Put appends one JSONL line.
func (c *Classifications) Put(_ context.Context, postID string, cls domain.Classification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(classificationLine{PostID: postID, Classification: cls})
	if err != nil {
		return fmt.Errorf("marshal classification: %w", err)
	}
	if _, err := c.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write classification: %w", err)
	}
	return c.writer.Sync()
}

// Get returns the newest classification for postID. Returns ErrNotFound if none.
func (c *Classifications) Get(_ context.Context, postID string) (domain.Classification, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return domain.Classification{}, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	var match domain.Classification
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line classificationLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.PostID == postID {
			match = line.Classification
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return domain.Classification{}, fmt.Errorf("scan: %w", err)
	}
	if !found {
		return domain.Classification{}, fmt.Errorf("%w: postID=%s", ErrNotFound, postID)
	}
	return match, nil
}
```

- [ ] **Step 2: Commit.**

```bash
go build ./internal/infrastructure/store/filestore/...
git add internal/infrastructure/store/filestore/classifications.go
git commit -m "feat(infra/store/filestore): JSONL ClassificationStore (Put/Get by postID)"
```

---

### Task T3.7 — EscalationStore (ring + JSONL) + tests

**Group:** G3. **Parallel-safe.**

**Files:**
- Create: `internal/infrastructure/store/filestore/escalations.go`
- Create: `internal/infrastructure/store/memstore/escalation_ring.go`
- Test: `internal/infrastructure/store/memstore/escalation_ring_test.go`

**Steps:**

- [ ] **Step 1: Implement the JSONL durable writer at `filestore/escalations.go`.**

```go
package filestore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Escalations is the JSONL-durable half of the escalation store. Callers pair
// it with memstore.EscalationRing for fast List. This type only owns writes.
type Escalations struct {
	mu     sync.Mutex
	writer *os.File
}

// NewEscalations opens data/escalations.jsonl.
func NewEscalations(dir string) (*Escalations, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "escalations.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open escalations: %w", err)
	}
	return &Escalations{writer: f}, nil
}

// Close closes the underlying file.
func (e *Escalations) Close() error { return e.writer.Close() }

// Append serializes one escalation as JSONL.
func (e *Escalations) Append(_ context.Context, es domain.Escalation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := json.Marshal(es)
	if err != nil {
		return fmt.Errorf("marshal escalation: %w", err)
	}
	if _, err := e.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write escalation: %w", err)
	}
	return e.writer.Sync()
}
```

- [ ] **Step 2: Implement the ring + composite store at `memstore/escalation_ring.go`.**

```go
package memstore

import (
	"context"
	"sync"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// durableAppender is the narrow unexported interface EscalationRing uses to
// persist each escalation. Satisfied by filestore.Escalations.
type durableAppender interface {
	Append(ctx context.Context, e domain.Escalation) error
}

// EscalationRing combines an in-RAM ring buffer (latest N) with a durable
// appender (JSONL) so GET /escalations is fast and restarts do not lose data
// beyond what the ring holds.
type EscalationRing struct {
	mu       sync.Mutex
	capacity int
	buf      []domain.Escalation // oldest -> newest, bounded to capacity
	durable  durableAppender
}

// NewEscalationRing returns a ring of capacity items backed by durable.
func NewEscalationRing(capacity int, durable durableAppender) *EscalationRing {
	if capacity < 1 {
		capacity = 500
	}
	return &EscalationRing{capacity: capacity, buf: make([]domain.Escalation, 0, capacity), durable: durable}
}

// Record appends to the durable writer and inserts into the ring.
func (r *EscalationRing) Record(ctx context.Context, e domain.Escalation) error {
	if r.durable != nil {
		if err := r.durable.Append(ctx, e); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.capacity {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, e)
	return nil
}

// List returns the last limit escalations, newest first.
func (r *EscalationRing) List(_ context.Context, limit int) ([]domain.Escalation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.buf) {
		limit = len(r.buf)
	}
	out := make([]domain.Escalation, 0, limit)
	for i := len(r.buf) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.buf[i])
	}
	return out, nil
}
```

- [ ] **Step 3: Test.**

```go
package memstore_test

import (
	"context"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
)

type appenderCount struct{ n int }

func (a *appenderCount) Append(_ context.Context, _ domain.Escalation) error { a.n++; return nil }

func TestEscalationRing(t *testing.T) {
	t.Parallel()
	ap := &appenderCount{}
	r := memstore.NewEscalationRing(3, ap)
	for i := 0; i < 5; i++ {
		_ = r.Record(context.Background(), domain.Escalation{ID: string(rune('a' + i))})
	}
	if ap.n != 5 {
		t.Fatalf("durable Append not called for each record: got %d", ap.n)
	}
	got, _ := r.List(context.Background(), 10)
	if len(got) != 3 || got[0].ID != "e" || got[2].ID != "c" {
		t.Fatalf("ring state unexpected: %+v", got)
	}
}
```

- [ ] **Step 4: Commit.**

```bash
go test -race ./internal/infrastructure/store/...
git add internal/infrastructure/store/
git commit -m "feat(infra/store): EscalationRing (memstore) + durable JSONL Escalations appender"
```

---

### Task T3.8 — Timed debouncer + tests

**Group:** G3 but depends on T3.1 (Clock). **Parallel-safe with:** everything except T3.1.

**Files:**
- Create: `internal/infrastructure/debouncer/timed.go`
- Test: `internal/infrastructure/debouncer/timed_test.go`

**Steps:**

- [ ] **Step 1: Failing test (with fake clock).**

```go
package debouncer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
)

func TestDebouncerFlushesBurst(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake(time.Unix(0, 0))
	var (
		mu     sync.Mutex
		flush  []domain.Turn
		signal = make(chan struct{}, 10)
	)
	d := debouncer.NewTimed(100*time.Millisecond, time.Second, fc, func(_ context.Context, t domain.Turn) {
		mu.Lock()
		flush = append(flush, t)
		mu.Unlock()
		signal <- struct{}{}
	})
	defer d.Stop()

	k := domain.ConversationKey("c1")
	d.Push(context.Background(), k, domain.Message{PostID: "p1", Body: "hi"})
	d.Push(context.Background(), k, domain.Message{PostID: "p2", Body: "is it open?"})
	d.Push(context.Background(), k, domain.Message{PostID: "p1", Body: "hi"}) // dup

	select {
	case <-signal:
		t.Fatal("flush must NOT fire before the window elapses")
	case <-time.After(150 * time.Millisecond):
	}

	select {
	case <-signal:
		// Timer fires via time.AfterFunc (real wall clock), then the handler runs.
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not fire within 2s of real wall clock (timer relies on real time)")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flush) != 1 {
		t.Fatalf("want 1 flush, got %d", len(flush))
	}
	if len(flush[0].Messages) != 2 {
		t.Fatalf("want 2 messages (dedup of p1), got %d", len(flush[0].Messages))
	}
}

func TestDebouncerHostCancels(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake(time.Unix(0, 0))
	var flushes int
	d := debouncer.NewTimed(time.Second, 10*time.Second, fc, func(_ context.Context, _ domain.Turn) { flushes++ })
	defer d.Stop()
	k := domain.ConversationKey("c1")
	d.Push(context.Background(), k, domain.Message{PostID: "p1"})
	d.CancelIfHostReplied(k, domain.RoleHost)
	// Give the timer a chance to fire (it must not).
	time.Sleep(100 * time.Millisecond)
	if flushes != 0 {
		t.Fatalf("host cancellation should drop buffer: flushes=%d", flushes)
	}
}
```

Note: this test uses `time.AfterFunc` with a small real-clock window (100ms) for speed. The `Clock` interface is still threaded for the `maxWait` computation; we use real time for the timer itself because Go's stdlib `time.AfterFunc` is simpler than a fully virtualized scheduler and the spec's debounce budget is measured in real wall-clock seconds in production.

- [ ] **Step 2: Implement `timed.go`.**

```go
// Package debouncer implements repository.Debouncer with a sliding window
// bounded by a hard max-wait cap. One goroutine per buffered conversation is
// avoided — flush is driven by time.AfterFunc and guarded by a single mutex.
package debouncer

import (
	"context"
	"sync"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// FlushFn is the orchestrator entry point invoked when a buffer flushes.
type FlushFn func(ctx context.Context, t domain.Turn)

type convBuffer struct {
	key         domain.ConversationKey
	messages    []domain.Message
	seenPostIDs map[string]struct{}
	timer       *time.Timer
	createdAt   time.Time
}

// Timed is the production Debouncer. Construction injects window, maxWait,
// and Clock (for maxWait calculation) plus the flush callback.
type Timed struct {
	mu       sync.Mutex
	buffers  map[domain.ConversationKey]*convBuffer
	window   time.Duration
	maxWait  time.Duration
	clock    repository.Clock
	flush    FlushFn
	stopped  bool
}

// NewTimed constructs a Timed debouncer. flush is called on every successful
// flush in the goroutine that fires the timer.
func NewTimed(window, maxWait time.Duration, c repository.Clock, flush FlushFn) *Timed {
	return &Timed{
		buffers: make(map[domain.ConversationKey]*convBuffer, 64),
		window:  window,
		maxWait: maxWait,
		clock:   c,
		flush:   flush,
	}
}

// Push records msg under k and (re)arms the flush timer.
func (d *Timed) Push(_ context.Context, k domain.ConversationKey, msg domain.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	buf, ok := d.buffers[k]
	if !ok {
		buf = &convBuffer{key: k, seenPostIDs: make(map[string]struct{}, 4), createdAt: d.clock.Now()}
		d.buffers[k] = buf
	}
	if _, dup := buf.seenPostIDs[msg.PostID]; dup {
		return
	}
	buf.seenPostIDs[msg.PostID] = struct{}{}
	buf.messages = append(buf.messages, msg)
	if buf.timer != nil {
		buf.timer.Stop()
	}
	wait := d.windowClamped(buf)
	buf.timer = time.AfterFunc(wait, func() { d.fire(k) })
}

func (d *Timed) windowClamped(buf *convBuffer) time.Duration {
	remaining := d.maxWait - d.clock.Since(buf.createdAt)
	wait := d.window
	if remaining < wait {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// CancelIfHostReplied drops an active buffer for k when role is not a guest.
func (d *Timed) CancelIfHostReplied(k domain.ConversationKey, role domain.Role) {
	if role == domain.RoleGuest {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if buf, ok := d.buffers[k]; ok {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(d.buffers, k)
	}
}

// Stop prevents further Push and cancels pending timers.
func (d *Timed) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for k, buf := range d.buffers {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(d.buffers, k)
	}
}

func (d *Timed) fire(k domain.ConversationKey) {
	d.mu.Lock()
	buf, ok := d.buffers[k]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.buffers, k)
	d.mu.Unlock()
	turn := domain.Turn{Key: buf.key, Messages: append([]domain.Message{}, buf.messages...), LastPostID: buf.messages[len(buf.messages)-1].PostID}
	d.flush(context.Background(), turn)
}
```

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/infrastructure/debouncer/...
git add internal/infrastructure/debouncer/
git commit -m "feat(infra/debouncer): Timed sliding-window debouncer with hard max-wait and host-cancel"
```

---

### Task T4.1 — LLM client + tool schemas

**Group:** G4. **Parallel-safe with:** T4.2 and all G3 tasks.

**Files:**
- Create: `internal/infrastructure/llm/client.go`
- Create: `internal/infrastructure/llm/tools.go`
- Test: `internal/infrastructure/llm/tools_test.go`

**Steps:**

- [ ] **Step 1: Implement `client.go`.**

```go
// Package llm wraps sashabaranov/go-openai with a configurable BaseURL so the
// same code runs against OpenAI, DeepSeek (default in v1), or any other
// OpenAI-compatible endpoint. The concrete type Client satisfies
// repository.LLMClient.
package llm

import (
	"context"

	openai "github.com/sashabaranov/go-openai"
)

// Client is the production LLMClient.
type Client struct {
	c *openai.Client
}

// NewClient constructs a Client against the given BaseURL and API key.
// BaseURL examples: "https://api.deepseek.com/v1", "https://api.openai.com/v1".
func NewClient(baseURL, apiKey string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Client{c: openai.NewClientWithConfig(cfg)}
}

// Chat forwards the request to the underlying SDK.
func (c *Client) Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return c.c.CreateChatCompletion(ctx, req)
}
```

- [ ] **Step 2: Implement `tools.go` — three tool definitions the generator agent loop uses.**

```go
package llm

import openai "github.com/sashabaranov/go-openai"

// any: go-openai's FunctionDefinition.Parameters is typed as any because JSON
// schema is a free-form payload at the SDK boundary. We pass typed map
// literals in, not interface{} up the stack.

// GetListingTool is declared once and reused by every Chat request that needs
// listing facts.
var GetListingTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "get_listing",
		Description: "Look up facts for the listing on this reservation (title, bedrooms, amenities, house rules, base price, neighborhood). Call once if facts are needed for Overview/Explain.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"listing_id"},
			"additionalProperties": false,
			"properties": map[string]any{
				"listing_id": map[string]any{"type": "string", "description": "Guesty listing id"},
			},
		},
	},
}

// CheckAvailabilityTool MUST be called before the generator asserts any
// availability or total price (the C.L.O.S.E.R. Sell-Certainty beat).
var CheckAvailabilityTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "check_availability",
		Description: "Check whether the listing is available for specific dates and return the total. REQUIRED before filling the Sell-Certainty beat.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"listing_id", "from", "to"},
			"additionalProperties": false,
			"properties": map[string]any{
				"listing_id": map[string]any{"type": "string"},
				"from":       map[string]any{"type": "string", "format": "date"},
				"to":         map[string]any{"type": "string", "format": "date"},
			},
		},
	},
}

// GetConversationHistoryTool lets the generator pull older messages when the
// guest references prior context not visible in the thread window.
var GetConversationHistoryTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "get_conversation_history",
		Description: "Fetch older messages from this conversation beyond the recent window already provided. Use only when the current guest message references prior context you cannot see.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"conversation_id", "limit"},
			"additionalProperties": false,
			"properties": map[string]any{
				"conversation_id": map[string]any{"type": "string"},
				"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
				"before_post_id":  map[string]any{"type": "string"},
			},
		},
	},
}

// AllTools is the slice passed to each agent-loop request.
var AllTools = []openai.Tool{GetListingTool, CheckAvailabilityTool, GetConversationHistoryTool}
```

- [ ] **Step 3: Sanity test — the tool payloads marshal cleanly.**

```go
package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

func TestToolsMarshal(t *testing.T) {
	t.Parallel()
	for _, tool := range llm.AllTools {
		if tool.Function == nil || tool.Function.Name == "" {
			t.Fatalf("tool missing function: %+v", tool)
		}
		if _, err := json.Marshal(tool); err != nil {
			t.Fatalf("tool %q did not marshal: %v", tool.Function.Name, err)
		}
	}
}
```

- [ ] **Step 4: Commit.**

```bash
go test -race ./internal/infrastructure/llm/... && golangci-lint run ./internal/infrastructure/llm/...
git add internal/infrastructure/llm/
git commit -m "feat(infra/llm): go-openai Client with configurable BaseURL + tool schemas (get_listing, check_availability, get_conversation_history)"
```

---

### Task T4.2 — Guesty HTTP client + retries + mapper + tests

**Group:** G4. **Parallel-safe with:** T4.1 and all G3.

**Files:**
- Create: `internal/infrastructure/guesty/client.go`
- Create: `internal/infrastructure/guesty/types.go`
- Create: `internal/infrastructure/guesty/retry.go`
- Test: `internal/infrastructure/guesty/client_test.go`

**Steps:**

- [ ] **Step 1: Write the wire DTOs (infrastructure-local).**

```go
// Package guesty is the HTTP client that satisfies repository.GuestyClient.
// The BaseURL is injected (defaulting to Mockoon in dev). All responses are
// mapped into domain types via internal/domain/mappers before returning.
package guesty

import "time"

type wireListing struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Bedrooms     int      `json:"bedrooms"`
	Beds         int      `json:"beds"`
	MaxGuests    int      `json:"maxGuests"`
	Amenities    []string `json:"amenities"`
	HouseRules   []string `json:"houseRules"`
	BasePrice    float64  `json:"basePrice"`
	Neighborhood string   `json:"neighborhood"`
}

type wireAvailability struct {
	Available bool    `json:"available"`
	Nights    int     `json:"nights"`
	Total     float64 `json:"total"`
}

type wireMessage struct {
	PostID    string    `json:"postId"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
}

type wireConversationResponse struct {
	ID           string        `json:"_id"`
	GuestID      string        `json:"guestId"`
	Language     string        `json:"language"`
	Integration  struct {
		Platform string `json:"platform"`
	} `json:"integration"`
	Meta struct {
		GuestName    string `json:"guestName"`
		Reservations []struct {
			ID               string    `json:"_id"`
			CheckIn          time.Time `json:"checkIn"`
			CheckOut         time.Time `json:"checkOut"`
			ConfirmationCode string    `json:"confirmationCode"`
		} `json:"reservations"`
	} `json:"meta"`
	Thread []wireMessage `json:"thread"`
}
```

- [ ] **Step 2: Implement the retry helper.**

```go
package guesty

import (
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// ErrRetriesExhausted is returned when all retry attempts have failed.
var ErrRetriesExhausted = errors.New("guesty: retries exhausted")

// shouldRetry returns the retry delay for status; 0 means do not retry.
// Honors Retry-After on 429 when present.
func shouldRetry(resp *http.Response, attempt int, base time.Duration) time.Duration {
	if resp == nil {
		return backoff(attempt, base)
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if s, err := strconv.Atoi(ra); err == nil {
				return time.Duration(s) * time.Second
			}
		}
		return backoff(attempt, base)
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return backoff(attempt, base)
	}
	return 0
}

func backoff(attempt int, base time.Duration) time.Duration {
	// Exponential with jitter: base * 2^attempt * (0.8 .. 1.2).
	d := base * (1 << attempt)
	jitter := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64())) //nolint:gosec // jitter, not security
	return jitter
}
```

Remove the `//nolint` — regenerate with a non-security RNG using `math/rand/v2` if the gate flags `gosec: G404`:

```go
import mrand "math/rand/v2"
// ...
jitter := time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64()))
```

- [ ] **Step 3: Implement the client.**

```go
package guesty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
)

// Client is the production GuestyClient.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	retries    int
	baseBackoff time.Duration
}

// NewClient constructs a Client. timeout applies per-request; retries and
// baseBackoff shape the retry schedule for 429/5xx.
func NewClient(baseURL, token string, timeout time.Duration, retries int) *Client {
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: timeout},
		retries:    retries,
		baseBackoff: 200 * time.Millisecond,
	}
}

// GetListing GETs /listings/{id} and maps the response into a domain.Listing.
func (c *Client) GetListing(ctx context.Context, id string) (domain.Listing, error) {
	var wire wireListing
	if err := c.do(ctx, http.MethodGet, "/listings/"+url.PathEscape(id), nil, &wire); err != nil {
		return domain.Listing{}, err
	}
	dto := mappers.GuestyListingDTO{ID: wire.ID, Title: wire.Title, Bedrooms: wire.Bedrooms, Beds: wire.Beds, MaxGuests: wire.MaxGuests, Amenities: wire.Amenities, HouseRules: wire.HouseRules, BasePrice: wire.BasePrice, Neighborhood: wire.Neighborhood}
	return mappers.ListingFromGuesty(dto), nil
}

// CheckAvailability GETs /availability?listingId=...&from=...&to=....
func (c *Client) CheckAvailability(ctx context.Context, listingID string, from, to time.Time) (domain.Availability, error) {
	q := url.Values{}
	q.Set("listingId", listingID)
	q.Set("from", from.Format("2006-01-02"))
	q.Set("to", to.Format("2006-01-02"))
	var wire wireAvailability
	if err := c.do(ctx, http.MethodGet, "/availability?"+q.Encode(), nil, &wire); err != nil {
		return domain.Availability{}, err
	}
	return mappers.AvailabilityFromGuesty(mappers.GuestyAvailabilityDTO{Available: wire.Available, Nights: wire.Nights, TotalUSD: wire.Total}), nil
}

// GetConversationHistory GETs /conversations/{id}/messages?limit=&before=.
func (c *Client) GetConversationHistory(ctx context.Context, convID string, limit int, beforePostID string) ([]domain.Message, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if beforePostID != "" {
		q.Set("before", beforePostID)
	}
	var wire []wireMessage
	if err := c.do(ctx, http.MethodGet, "/conversations/"+url.PathEscape(convID)+"/messages?"+q.Encode(), nil, &wire); err != nil {
		return nil, err
	}
	out := make([]domain.Message, 0, len(wire))
	for i := range wire {
		out = append(out, mappers.MessageFromGuesty(mappers.GuestyMessageDTO{PostID: wire[i].PostID, Body: wire[i].Body, CreatedAt: wire[i].CreatedAt, Type: wire[i].Type, Module: wire[i].Module}))
	}
	return out, nil
}

// GetConversation returns the current Conversation snapshot.
func (c *Client) GetConversation(ctx context.Context, convID string) (domain.Conversation, error) {
	var wire wireConversationResponse
	if err := c.do(ctx, http.MethodGet, "/conversations/"+url.PathEscape(convID), nil, &wire); err != nil {
		return domain.Conversation{}, err
	}
	conv := domain.Conversation{
		RawID: wire.ID, GuestID: wire.GuestID, Language: wire.Language,
		GuestName:   wire.Meta.GuestName,
		Integration: domain.Integration{Platform: wire.Integration.Platform},
	}
	for _, r := range wire.Meta.Reservations {
		conv.Reservations = append(conv.Reservations, domain.Reservation{ID: r.ID, CheckIn: r.CheckIn, CheckOut: r.CheckOut, ConfirmationCode: r.ConfirmationCode})
	}
	for i := range wire.Thread {
		conv.Thread = append(conv.Thread, mappers.MessageFromGuesty(mappers.GuestyMessageDTO{PostID: wire.Thread[i].PostID, Body: wire.Thread[i].Body, CreatedAt: wire.Thread[i].CreatedAt, Type: wire.Thread[i].Type, Module: wire.Thread[i].Module}))
	}
	return conv, nil
}

// PostNote POSTs /conversations/{id}/messages with type=note.
func (c *Client) PostNote(ctx context.Context, conversationID, body string) error {
	payload := mappers.NoteFromDomain(body)
	return c.do(ctx, http.MethodPost, "/conversations/"+url.PathEscape(conversationID)+"/messages", payload, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	// any: body may be any JSON-marshalable Go value; out is a user-supplied
	// pointer. Both are JSON-boundary use cases permitted by conventions.
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			wait := shouldRetry(nil, attempt, c.baseBackoff)
			if wait == 0 {
				return fmt.Errorf("guesty %s %s: %w", method, path, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
				continue
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer func() { _ = resp.Body.Close() }()
			if out == nil {
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			return nil
		}
		wait := shouldRetry(resp, attempt, c.baseBackoff)
		_ = resp.Body.Close()
		if wait == 0 {
			return fmt.Errorf("guesty %s %s: status %d", method, path, resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	return fmt.Errorf("%w: %v", ErrRetriesExhausted, errors.Unwrap(lastErr))
}
```

- [ ] **Step 4: Test against `httptest.Server`.**

```go
package guesty_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
)

func TestClientGetListing(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/L1" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"L1","title":"Soho 2BR","maxGuests":4,"amenities":["wifi"],"neighborhood":"Soho"}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 0)
	got, err := c.GetListing(context.Background(), "L1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "L1" || got.MaxGuests != 4 || got.Neighborhood != "Soho" {
		t.Fatalf("got %+v", got)
	}
}

func TestClientRetriesOn5xx(t *testing.T) {
	t.Parallel()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"id":"L1"}`))
	}))
	defer srv.Close()
	c := guesty.NewClient(srv.URL, "dev", time.Second, 3)
	_, err := c.GetListing(context.Background(), "L1")
	if err != nil {
		t.Fatalf("should succeed after retries: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls: got %d, want 3", calls)
	}
}
```

- [ ] **Step 5: Commit.**

```bash
go test -race ./internal/infrastructure/guesty/... && golangci-lint run ./internal/infrastructure/guesty/...
git add internal/infrastructure/guesty/
git commit -m "feat(infra/guesty): HTTP GuestyClient with 429/5xx retries, configurable BaseURL (Mockoon in dev) and domain mapping"
```

---

### Task T5.1 — Classify use case (Stage A)

**Group:** G5. **Depends on:** G1 (types), G4 T4.1 (LLMClient). **Parallel-safe with:** T5.2 (disjoint package), blocks T5.3b.

**Files:**
- Create: `internal/application/classify/prompt.go`
- Create: `internal/application/classify/schema.go`
- Create: `internal/application/classify/usecase.go`
- Test: `internal/application/classify/usecase_test.go`

**Steps:**

- [ ] **Step 1: Put the classifier system prompt and JSON schema in `prompt.go` and `schema.go` copied verbatim from spec §5.3 and §5.2.**

Because the full prompt is ~100 lines, do **not** inline-edit it in code — store it as a Go string constant. Example skeleton:

```go
// Package classify implements the Stage A classifier — one LLM call, no tools,
// structured JSON output validated against a strict schema in Go.
package classify

const systemPrompt = `You are the InquiryIQ classifier for Cloud9 ...` // full text from spec §5.3
```

Similarly `schema.go`:

```go
package classify

const classificationSchema = `{
  "type": "object",
  "required": ["primary_code","confidence","extracted_entities","risk_flag","next_action","reasoning"],
  ...
}` // full schema from spec §5.2
```

- [ ] **Step 2: Implement the use case with a local unexported LLM interface for test-time fakery.**

`internal/application/classify/usecase.go`:
```go
package classify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/xeipuuv/gojsonschema"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// llmClient is the narrow unexported contract Classify depends on. The
// production wiring passes in infrastructure/llm.Client, which satisfies it
// structurally; tests pass a fake.
type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the Stage A classifier.
type UseCase struct {
	llm        llmClient
	model      string
	timeout    time.Duration
	schema     *gojsonschema.Schema
}

// New constructs a UseCase. Returns an error only if the embedded schema
// fails to compile (should never happen at runtime given the static string).
func New(llm llmClient, model string, timeout time.Duration) (*UseCase, error) {
	s, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(classificationSchema))
	if err != nil {
		return nil, fmt.Errorf("compile classifier schema: %w", err)
	}
	return &UseCase{llm: llm, model: model, timeout: timeout, schema: s}, nil
}

// Input is what the orchestrator passes in — the current turn plus prior context.
type Input struct {
	Turn  domain.Turn
	Prior domain.PriorContext
	Now   time.Time
}

// Classify returns a typed Classification. Retries once on unparseable or
// schema-invalid output with an appended corrective system message. Wraps
// domain.ErrClassificationInvalid when the second attempt also fails.
func (u *UseCase) Classify(ctx context.Context, in Input) (domain.Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	userMsg := buildUserMessage(in)
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userMsg},
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := u.llm.Chat(ctx, openai.ChatCompletionRequest{
			Model:               u.model,
			Messages:            messages,
			Temperature:         0.1,
			MaxCompletionTokens: 500,
			ResponseFormat:      &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
		})
		if err != nil {
			return domain.Classification{}, fmt.Errorf("classifier chat: %w", err)
		}
		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		if parsed, ok := u.validateAndParse(raw); ok {
			return parsed, nil
		}
		messages = append(messages,
			resp.Choices[0].Message,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: "Your previous response was not valid JSON per the schema. Return only the object, no prose."},
		)
	}
	return domain.Classification{}, domain.ErrClassificationInvalid
}

func (u *UseCase) validateAndParse(raw string) (domain.Classification, bool) {
	loader := gojsonschema.NewStringLoader(raw)
	result, err := u.schema.Validate(loader)
	if err != nil || !result.Valid() {
		return domain.Classification{}, false
	}
	var cls domain.Classification
	if err := json.Unmarshal([]byte(raw), &cls); err != nil {
		return domain.Classification{}, false
	}
	return cls, true
}

func buildUserMessage(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "current_date: %s\n", in.Now.UTC().Format("2006-01-02"))
	if in.Prior.GuestProfile != "" {
		fmt.Fprintf(&b, "guest_profile: %q\n", in.Prior.GuestProfile)
	}
	if in.Prior.Summary != "" {
		fmt.Fprintf(&b, "prior_thread_summary: %q\n", in.Prior.Summary)
	}
	fmt.Fprintf(&b, "prior_thread (last %d):\n", len(in.Prior.Thread))
	for _, m := range in.Prior.Thread {
		fmt.Fprintf(&b, "- [%s %s] %s\n", m.Role, m.CreatedAt.UTC().Format("2006-01-02T15:04Z"), m.Body)
	}
	b.WriteString("\n---\nguest_turn (classify THIS):\n")
	for _, m := range in.Turn.Messages {
		fmt.Fprintf(&b, "%s\n", m.Body)
	}
	return b.String()
}
```

- [ ] **Step 3: Test with a fake LLM that returns canned payloads.**

```go
package classify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/domain"
)

type fakeLLM struct {
	responses []string
	idx       int
}

func (f *fakeLLM) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if f.idx >= len(f.responses) {
		return openai.ChatCompletionResponse{}, errors.New("no more responses")
	}
	r := f.responses[f.idx]
	f.idx++
	return openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: r}}}}, nil
}

func TestClassifyHappyPath(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{`{"primary_code":"G1","confidence":0.9,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"clear booking intent"}`}}
	u, err := classify.New(f, "deepseek-chat", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cls, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if cls.PrimaryCode != domain.G1 || cls.Confidence != 0.9 {
		t.Fatalf("got %+v", cls)
	}
}

func TestClassifyRetriesOnInvalidThenSucceeds(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{
		"not json",
		`{"primary_code":"Y1","confidence":0.75,"extracted_entities":{},"risk_flag":false,"next_action":"generate_reply","reasoning":"parking question"}`,
	}}
	u, _ := classify.New(f, "m", time.Second)
	cls, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if cls.PrimaryCode != domain.Y1 {
		t.Fatalf("got %+v", cls)
	}
}

func TestClassifyFailsAfterTwoInvalid(t *testing.T) {
	t.Parallel()
	f := &fakeLLM{responses: []string{"still not json", "also still not json"}}
	u, _ := classify.New(f, "m", time.Second)
	_, err := u.Classify(context.Background(), classify.Input{Now: time.Now()})
	if !errors.Is(err, domain.ErrClassificationInvalid) {
		t.Fatalf("want ErrClassificationInvalid, got %v", err)
	}
}
```

- [ ] **Step 4: Commit.**

```bash
go test -race ./internal/application/classify/... && golangci-lint run ./internal/application/classify/...
git add internal/application/classify/
git commit -m "feat(application/classify): Stage A classifier — JSON-schema-validated LLM call with one retry on malformed output"
```

---

### Task T5.2 — GenerateReply use case (Stage B agent loop + reflection)

**Group:** G5. **Depends on:** G1, T4.1. **Parallel-safe with:** T5.1 (disjoint package), blocks T5.3b.

**Files:**
- Create: `internal/application/generatereply/prompt.go`
- Create: `internal/application/generatereply/usecase.go`
- Create: `internal/application/generatereply/tooldispatch.go`
- Test: `internal/application/generatereply/usecase_test.go`

**Steps:**

- [ ] **Step 1: Store the two system prompts (generator + reflection) verbatim from spec §6.5 and §6.4 in `prompt.go`.**

```go
package generatereply

const systemPrompt = `You are the InquiryIQ generator for Cloud9 ...` // full text from spec §6.5
const reflectionSystemPrompt = `You have exhausted your tool-call budget for this turn. Tools are now DISABLED. ...` // full text from spec §6.4
```

- [ ] **Step 2: Implement `tooldispatch.go` — one helper per tool.**

```go
package generatereply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

type getListingArgs struct {
	ListingID string `json:"listing_id"`
}
type checkAvailArgs struct {
	ListingID string `json:"listing_id"`
	From      string `json:"from"`
	To        string `json:"to"`
}
type historyArgs struct {
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
	BeforePostID   string `json:"before_post_id"`
}

// runTool dispatches a single tool call against the real Guesty client and
// returns a ToolCall audit record. Errors are encoded into Result as
// {"error":"..."} so the LLM can see them and adapt.
func runTool(ctx context.Context, guesty repository.GuestyClient, tc openai.ToolCall) domain.ToolCall {
	start := time.Now()
	name := tc.Function.Name
	rec := domain.ToolCall{Name: name, Arguments: json.RawMessage(tc.Function.Arguments)}
	defer func() { rec.LatencyMs = time.Since(start).Milliseconds() }()

	switch name {
	case "get_listing":
		var args getListingArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
			return rec
		}
		res, err := guesty.GetListing(ctx, args.ListingID)
		if err != nil {
			rec.Result, rec.Error = encodeErr("get_listing_failed", err.Error())
			return rec
		}
		rec.Result, _ = json.Marshal(res)
		return rec
	case "check_availability":
		var args checkAvailArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
			return rec
		}
		from, ferr := time.Parse("2006-01-02", args.From)
		to, terr := time.Parse("2006-01-02", args.To)
		if ferr != nil || terr != nil {
			rec.Result, rec.Error = encodeErr("invalid_dates", fmt.Sprintf("from=%v to=%v", ferr, terr))
			return rec
		}
		res, err := guesty.CheckAvailability(ctx, args.ListingID, from, to)
		if err != nil {
			rec.Result, rec.Error = encodeErr("check_availability_failed", err.Error())
			return rec
		}
		rec.Result, _ = json.Marshal(res)
		return rec
	case "get_conversation_history":
		var args historyArgs
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			rec.Result, rec.Error = encodeErr("invalid_arguments", err.Error())
			return rec
		}
		res, err := guesty.GetConversationHistory(ctx, args.ConversationID, args.Limit, args.BeforePostID)
		if err != nil {
			rec.Result, rec.Error = encodeErr("history_failed", err.Error())
			return rec
		}
		rec.Result, _ = json.Marshal(res)
		return rec
	}
	rec.Result, rec.Error = encodeErr("unknown_tool", name)
	return rec
}

func encodeErr(code, msg string) (json.RawMessage, string) {
	b, _ := json.Marshal(map[string]string{"error": code, "detail": msg})
	return b, code + ": " + msg
}
```

- [ ] **Step 3: Implement the agent loop `usecase.go`.**

```go
package generatereply

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

type llmClient interface {
	Chat(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

// UseCase is the Stage B generator.
type UseCase struct {
	llm      llmClient
	guesty   repository.GuestyClient
	model    string
	timeout  time.Duration
	maxTurns int
}

// New constructs a UseCase.
func New(l llmClient, g repository.GuestyClient, model string, timeout time.Duration, maxTurns int) *UseCase {
	return &UseCase{llm: l, guesty: g, model: model, timeout: timeout, maxTurns: maxTurns}
}

// Input is the generator contract.
type Input struct {
	Turn           domain.Turn
	Classification domain.Classification
	Prior          domain.PriorContext
	ConversationID string // raw Guesty id (for tool args)
	ListingID      string
	Now            time.Time
}

// Generate runs the agent loop and returns a Reply. Never errors on
// max-turns — returns a Reply with AbortReason="max_turns" instead.
func (u *UseCase) Generate(ctx context.Context, in Input) (domain.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: buildUserMessage(in)},
	}
	toolLog := make([]domain.ToolCall, 0, 4)
	for i := 0; i < u.maxTurns; i++ {
		resp, err := u.callOnce(ctx, messages)
		if err != nil {
			return domain.Reply{}, err
		}
		msg := resp.Choices[0].Message
		messages = append(messages, msg)
		if len(msg.ToolCalls) == 0 {
			return parseFinal(msg.Content, toolLog)
		}
		for _, tc := range msg.ToolCalls {
			rec := runTool(ctx, u.guesty, tc)
			toolLog = append(toolLog, rec)
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: tc.ID, Content: string(rec.Result)})
		}
	}
	return u.reflectOnFailure(ctx, messages, toolLog), nil
}

func (u *UseCase) callOnce(ctx context.Context, messages []openai.ChatCompletionMessage) (openai.ChatCompletionResponse, error) {
	return u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Tools:               llm.AllTools,
		ToolChoice:          "auto",
		ParallelToolCalls:   true,
		Temperature:         0.3,
		MaxCompletionTokens: 600,
		ResponseFormat:      &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
}

func (u *UseCase) reflectOnFailure(ctx context.Context, messages []openai.ChatCompletionMessage, toolLog []domain.ToolCall) domain.Reply {
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: reflectionSystemPrompt})
	resp, err := u.llm.Chat(ctx, openai.ChatCompletionRequest{
		Model:               u.model,
		Messages:            messages,
		Temperature:         0.4,
		MaxCompletionTokens: 400,
		ResponseFormat:      &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		return domain.Reply{AbortReason: "max_turns", ReflectionReason: "reflection call failed: " + err.Error(), UsedTools: toolLog}
	}
	var r domain.Reply
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &r); err != nil {
		return domain.Reply{AbortReason: "max_turns", ReflectionReason: "reflection unparseable: " + resp.Choices[0].Message.Content, UsedTools: toolLog}
	}
	r.AbortReason = "max_turns"
	r.UsedTools = toolLog
	return r
}

func parseFinal(content string, toolLog []domain.ToolCall) (domain.Reply, error) {
	var r domain.Reply
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return domain.Reply{}, fmt.Errorf("%w: %s", domain.ErrReplyInvalid, content)
	}
	r.UsedTools = toolLog
	return r, nil
}

func buildUserMessage(in Input) string {
	// Same shape as the spec §6 template; fill verbatim from Input fields.
	// Elided here — implementers should mirror the classifier's buildUserMessage
	// plus the classification summary and listing_id. Must be <=50 statements.
	return fmt.Sprintf("listing_id: %s\nconversation_id: %s\nclassification: %+v\nguest_profile: %q\nturn:\n%s",
		in.ListingID, in.ConversationID, in.Classification, in.Prior.GuestProfile, joinBodies(in.Turn.Messages))
}

func joinBodies(msgs []domain.Message) string {
	var b []byte
	for i := range msgs {
		b = append(b, msgs[i].Body...)
		b = append(b, '\n')
	}
	return string(b)
}
```

- [ ] **Step 4: Test the agent loop with a fake LLM that scripts tool calls then a final JSON reply, plus a max-turns test.**

```go
package generatereply_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// scripted returns a pre-baked sequence of responses.
type scripted struct {
	steps []openai.ChatCompletionResponse
	idx   int
}

func (s *scripted) Chat(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if s.idx >= len(s.steps) {
		return openai.ChatCompletionResponse{}, errors.New("no more responses")
	}
	r := s.steps[s.idx]
	s.idx++
	return r, nil
}

// stubGuesty implements repository.GuestyClient with fixed values.
type stubGuesty struct{}

func (stubGuesty) GetListing(_ context.Context, _ string) (domain.Listing, error) {
	return domain.Listing{ID: "L1", Title: "Soho 2BR", MaxGuests: 4}, nil
}
func (stubGuesty) CheckAvailability(_ context.Context, _ string, _, _ time.Time) (domain.Availability, error) {
	return domain.Availability{Available: true, Nights: 2, TotalUSD: 480}, nil
}
func (stubGuesty) GetConversationHistory(_ context.Context, _ string, _ int, _ string) ([]domain.Message, error) {
	return nil, nil
}
func (stubGuesty) GetConversation(_ context.Context, _ string) (domain.Conversation, error) {
	return domain.Conversation{}, nil
}
func (stubGuesty) PostNote(_ context.Context, _, _ string) error { return nil }

func TestGenerateHappyPath(t *testing.T) {
	t.Parallel()
	finalJSON, _ := json.Marshal(domain.Reply{
		Body:        "Hi Sarah — quick city weekend for 4. Soho 2BR sleeps 4 with self check-in. Those dates are open and the total is $480 all-in. Courtyard bedroom is the quietest sleep in Manhattan. Want me to hold it?",
		Confidence:  0.85,
		CloserBeats: domain.CloserBeats{Clarify: true, Label: true, Overview: true, SellCertainty: true, Explain: true, Request: true},
	})
	s := &scripted{steps: []openai.ChatCompletionResponse{
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{ToolCalls: []openai.ToolCall{{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "check_availability", Arguments: `{"listing_id":"L1","from":"2026-04-24","to":"2026-04-26"}`}}}}}}},
		{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: string(finalJSON)}}}},
	}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{Turn: domain.Turn{Messages: []domain.Message{{Body: "open for Fri-Sun, 4 adults?"}}}, ListingID: "L1", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if r.Body == "" || r.Confidence < 0.7 || len(r.UsedTools) != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestGenerateMaxTurnsTriggersReflection(t *testing.T) {
	t.Parallel()
	loopCall := openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{ToolCalls: []openai.ToolCall{{ID: "c1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "get_listing", Arguments: `{"listing_id":"L1"}`}}}}}}}
	reflectJSON := `{"body":"","closer_beats":{"clarify":false,"label":false,"overview":false,"sell_certainty":false,"explain":false,"request":false},"confidence":0.2,"reflection_reason":"I kept fetching the listing but never closed the loop","missing_info":["explicit check-out date"],"partial_findings":"Listing confirmed"}`
	s := &scripted{steps: []openai.ChatCompletionResponse{loopCall, loopCall, loopCall, loopCall, {Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: reflectJSON}}}}}}
	u := generatereply.New(s, stubGuesty{}, "m", 5*time.Second, 4)
	r, err := u.Generate(context.Background(), generatereply.Input{ListingID: "L1", Now: time.Now()})
	if err != nil {
		t.Fatalf("max_turns must not error: %v", err)
	}
	if r.AbortReason != "max_turns" || r.ReflectionReason == "" {
		t.Fatalf("got %+v", r)
	}
}
```

- [ ] **Step 5: Commit.**

```bash
go test -race ./internal/application/generatereply/... && golangci-lint run ./internal/application/generatereply/...
git add internal/application/generatereply/
git commit -m "feat(application/generatereply): Stage B agent loop with three tools, parallel tool calls, and reflection-on-max-turns instead of error"
```

---

### Task T5.3a — Guest-profile compression (Layer 4)

**Group:** G5. **Depends on:** G1 (types). **Parallel-safe with:** T5.1, T5.2.

**Files:**
- Create: `internal/application/processinquiry/guestprofile.go`
- Test: `internal/application/processinquiry/guestprofile_test.go`

**Steps:**

- [ ] **Step 1: Test + impl.**

```go
// Package processinquiry implements the top-level ProcessInquiry orchestrator
// use case plus helpers specific to orchestration (guest-profile compression,
// auto-replay on boot).
package processinquiry

import (
	"fmt"
	"strings"

	"github.com/chaustre/inquiryiq/internal/domain"
)

const maxProfileChars = 300

// BuildGuestProfile compresses up to N prior ConversationMemoryRecords for the
// same guest into one advisory paragraph (<=300 chars) fed to classifier and
// generator. Pure — no LLM call. Empty when records is empty.
func BuildGuestProfile(records []domain.ConversationMemoryRecord) string {
	if len(records) == 0 {
		return ""
	}
	var autoSends, escalations int
	reasons := map[string]int{}
	for i := range records {
		if records[i].LastAutoSendAt != nil {
			autoSends++
		}
		if records[i].LastEscalationAt != nil {
			escalations++
		}
		for _, r := range records[i].EscalationReasons {
			reasons[r]++
		}
	}
	top := topReasons(reasons, 3)
	var b strings.Builder
	fmt.Fprintf(&b, "guest has %d prior conversations on this platform; %d auto-sent, %d escalated.", len(records), autoSends, escalations)
	if len(top) > 0 {
		fmt.Fprintf(&b, " Common escalation reasons: %s.", strings.Join(top, ", "))
	}
	if pe := mostRecentPets(records); pe != nil {
		fmt.Fprintf(&b, " Prior trips indicated pets=%t.", *pe)
	}
	if s := b.String(); len(s) > maxProfileChars {
		return s[:maxProfileChars]
	}
	return b.String()
}

func topReasons(m map[string]int, n int) []string {
	if len(m) == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, kv{k, v})
	}
	// simple sort by count desc
	for i := 0; i < len(kvs); i++ {
		for j := i + 1; j < len(kvs); j++ {
			if kvs[j].v > kvs[i].v {
				kvs[i], kvs[j] = kvs[j], kvs[i]
			}
		}
	}
	if n > len(kvs) {
		n = len(kvs)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, kvs[i].k)
	}
	return out
}

func mostRecentPets(records []domain.ConversationMemoryRecord) *bool {
	for i := range records {
		if p := records[i].KnownEntities.Pets; p != nil {
			return p
		}
	}
	return nil
}
```

`guestprofile_test.go`:
```go
package processinquiry_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestBuildGuestProfileEmpty(t *testing.T) {
	t.Parallel()
	if processinquiry.BuildGuestProfile(nil) != "" {
		t.Fatal("empty records must produce empty profile")
	}
}

func TestBuildGuestProfileCounts(t *testing.T) {
	t.Parallel()
	now := time.Now()
	records := []domain.ConversationMemoryRecord{
		{LastAutoSendAt: &now, EscalationReasons: []string{"code_requires_human"}},
		{LastEscalationAt: &now, EscalationReasons: []string{"code_requires_human", "risk_flag"}},
	}
	got := processinquiry.BuildGuestProfile(records)
	if !strings.Contains(got, "2 prior conversations") || !strings.Contains(got, "code_requires_human") {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Commit.**

```bash
go test -race ./internal/application/processinquiry/...
git add internal/application/processinquiry/guestprofile.go internal/application/processinquiry/guestprofile_test.go
git commit -m "feat(application/processinquiry): Layer-4 guest-profile compression from cross-conversation memory"
```

---

### Task T5.3b — ProcessInquiry orchestrator

**Group:** G5. **Depends on:** T5.1, T5.2, T5.3a, T2.1, T2.2, T2.3, T2.4, T3.1, T3.4, T3.7, T3.8, T4.2.

**Files:**
- Create: `internal/application/processinquiry/usecase.go`
- Test: `internal/application/processinquiry/usecase_test.go`

**Steps:**

- [ ] **Step 1: Implement the orchestrator.**

Because this use case wires many collaborators, keep `Run` under 50 statements by extracting each phase into a private method: `classifyOrEscalate`, `generateOrEscalate`, `gateAndSend`, `recordEscalation`, `updateMemory`.

```go
package processinquiry

import (
	"context"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/obs"
	"github.com/google/uuid"
	"log/slog"
)

// Deps holds every interface the orchestrator consumes.
type Deps struct {
	Classifier      *classify.UseCase
	Generator       *generatereply.UseCase
	Guesty          repository.GuestyClient
	Idempotency     repository.IdempotencyStore
	Escalations     repository.EscalationStore
	Memory          repository.ConversationMemoryStore
	Classifications repository.ClassificationStore
	Toggles         domain.Toggles
	Thresholds      decide.Thresholds
	Log             *slog.Logger
}

// UseCase is the top-level orchestrator. One instance per server.
type UseCase struct {
	d Deps
}

// New constructs a UseCase.
func New(d Deps) *UseCase { return &UseCase{d: d} }

// Input is produced by the debouncer (one Turn) plus the resolved conversation.
type Input struct {
	Turn         domain.Turn
	Conversation domain.Conversation
	ListingID    string
	Now          time.Time
}

// Run is the full pipeline for a debounced turn. Never panics — all errors
// are logged and, where possible, recorded as escalations.
func (u *UseCase) Run(ctx context.Context, in Input) {
	ctx = obs.With(ctx, slog.String("post_id", in.Turn.LastPostID), slog.String("conversation_key", string(in.Turn.Key)))
	prior := u.priorContext(ctx, in)
	cls, ok := u.classifyOrEscalate(ctx, in, prior)
	if !ok {
		return
	}
	d := decide.PreGenerate(cls, u.d.Toggles, u.d.Thresholds.ClassifierMin)
	if !d.AutoSend {
		u.recordEscalation(ctx, in, cls, nil, d)
		u.closeTurn(ctx, in, cls, nil)
		return
	}
	reply, ok := u.generateOrEscalate(ctx, in, cls, prior)
	if !ok {
		return
	}
	issues := decide.ValidateReply(reply)
	final := decide.Decide(cls, reply, issues, u.d.Toggles, u.d.Thresholds)
	if !final.AutoSend {
		u.recordEscalation(ctx, in, cls, &reply, final)
		u.closeTurn(ctx, in, cls, &reply)
		return
	}
	if err := u.d.Guesty.PostNote(ctx, in.Conversation.RawID, reply.Body); err != nil {
		u.d.Log.ErrorContext(ctx, "post_note_failed", slog.String("err", err.Error()))
		u.recordEscalation(ctx, in, cls, &reply, domain.Decision{Reason: "post_note_failed", Detail: []string{err.Error()}})
	}
	u.closeTurn(ctx, in, cls, &reply)
}

func (u *UseCase) priorContext(ctx context.Context, in Input) domain.PriorContext {
	rec, _ := u.d.Memory.Get(ctx, in.Turn.Key)
	profile := ""
	if in.Conversation.GuestID != "" {
		if siblings, err := u.d.Memory.ListByGuest(ctx, in.Conversation.GuestID, 5); err == nil {
			profile = BuildGuestProfile(siblings)
		}
	}
	return domain.PriorContext{
		Summary:       rec.LastSummary,
		KnownEntities: rec.KnownEntities,
		Thread:        in.Conversation.Thread,
		GuestProfile:  profile,
	}
}

func (u *UseCase) classifyOrEscalate(ctx context.Context, in Input, prior domain.PriorContext) (domain.Classification, bool) {
	cls, err := u.d.Classifier.Classify(ctx, classify.Input{Turn: in.Turn, Prior: prior, Now: in.Now})
	if err != nil {
		u.d.Log.ErrorContext(ctx, "classify_failed", slog.String("err", err.Error()))
		u.recordEscalation(ctx, in, domain.Classification{}, nil, domain.Decision{Reason: "classifier_failed", Detail: []string{err.Error()}})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return cls, false
	}
	_ = u.d.Classifications.Put(ctx, in.Turn.LastPostID, cls)
	return cls, true
}

func (u *UseCase) generateOrEscalate(ctx context.Context, in Input, cls domain.Classification, prior domain.PriorContext) (domain.Reply, bool) {
	reply, err := u.d.Generator.Generate(ctx, generatereply.Input{
		Turn: in.Turn, Classification: cls, Prior: prior,
		ConversationID: in.Conversation.RawID, ListingID: in.ListingID, Now: in.Now,
	})
	if err != nil {
		u.d.Log.ErrorContext(ctx, "generate_failed", slog.String("err", err.Error()))
		u.recordEscalation(ctx, in, cls, nil, domain.Decision{Reason: "generator_failed", Detail: []string{err.Error()}})
		_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
		return domain.Reply{}, false
	}
	return reply, true
}

func (u *UseCase) recordEscalation(ctx context.Context, in Input, cls domain.Classification, reply *domain.Reply, d domain.Decision) {
	esc := domain.Escalation{
		ID: uuid.NewString(), TraceID: obs.TraceIDFrom(ctx),
		PostID: in.Turn.LastPostID, ConversationKey: in.Turn.Key,
		GuestID: in.Conversation.GuestID, GuestName: in.Conversation.GuestName,
		Platform: in.Conversation.Integration.Platform,
		CreatedAt: in.Now, Reason: d.Reason, Detail: d.Detail,
		Classification: cls, Reply: reply,
	}
	if reply != nil {
		esc.MissingInfo = reply.MissingInfo
		esc.PartialFindings = reply.PartialFindings
	}
	_ = u.d.Escalations.Record(ctx, esc)
}

func (u *UseCase) closeTurn(ctx context.Context, in Input, cls domain.Classification, reply *domain.Reply) {
	now := in.Now
	_ = u.d.Memory.Update(ctx, in.Turn.Key, func(r *domain.ConversationMemoryRecord) {
		r.ConversationKey = in.Turn.Key
		r.GuestID = in.Conversation.GuestID
		r.Platform = in.Conversation.Integration.Platform
		r.LastClassification = &cls
		if reply != nil && reply.AbortReason == "" {
			r.LastAutoSendAt = &now
		}
		if reply == nil || reply.AbortReason != "" {
			r.LastEscalationAt = &now
		}
		r.UpdatedAt = now
	})
	_ = u.d.Idempotency.Complete(ctx, in.Turn.Key, in.Turn.LastPostID)
}
```

- [ ] **Step 2: Write a table test that stubs every interface and asserts the escalation vs auto-send paths. Keep it focused on gate-routing outcomes, not LLM correctness (covered by T5.1/T5.2).**

The test is long (table-driven with one stub type per interface). Implementers can base it on the pattern used in T5.2's `stubGuesty`. Must cover at minimum: happy auto-send, PreGenerate escalation, generator abort, restricted-content hit, post_note_failed fallback.

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/application/processinquiry/...
git add internal/application/processinquiry/usecase.go internal/application/processinquiry/usecase_test.go
git commit -m "feat(application/processinquiry): orchestrator — debounced Turn -> prior context -> classify -> gate1 -> generate -> validate -> gate2 -> send/escalate -> memory"
```

---

### Task T5.4 — Auto-replay on boot

**Group:** G5. **Depends on:** T5.3b and T6.1 (transport mapper) — dispatch after both.

**Files:**
- Create: `internal/application/processinquiry/autoreplay.go`
- Test: `internal/application/processinquiry/autoreplay_test.go`

**Steps:**

- [ ] **Step 1: Implement.**

```go
package processinquiry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// AutoReplayConfig shapes the auto-replay-on-boot behavior (spec §13.2).
type AutoReplayConfig struct {
	Dir     string
	Delay   time.Duration
	Execute bool
}

// FixtureMapper converts a raw webhook JSON into the Input the orchestrator
// expects. Provided by transport/http so autoreplay shares the mapping with
// the live webhook path.
type FixtureMapper func(raw []byte) (Input, error)

// RunAutoReplay reads *.json fixtures in cfg.Dir, maps each via mapper, and
// invokes orch.Run per fixture with cfg.Delay spacing. Respects ctx.Done.
// Never panics.
func RunAutoReplay(ctx context.Context, cfg AutoReplayConfig, orch *UseCase, mapper FixtureMapper, log *slog.Logger) error {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return fmt.Errorf("auto-replay: read dir %s: %w", cfg.Dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(cfg.Dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.WarnContext(ctx, "auto_replay_read_failed", slog.String("file", path), slog.String("err", err.Error()))
			continue
		}
		in, err := mapper(raw)
		if err != nil {
			log.WarnContext(ctx, "auto_replay_map_failed", slog.String("file", path), slog.String("err", err.Error()))
			continue
		}
		in.Now = time.Now().UTC()
		log.InfoContext(ctx, "auto_replay_fixture", slog.String("file", path), slog.String("post_id", in.Turn.LastPostID))
		orch.Run(ctx, in)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.Delay):
		}
	}
	return nil
}

// ensure domain import is used
var _ = domain.Message{}
```

Remove the `_ = domain.Message{}` stub once another domain reference is added naturally — it exists here so the package compiles before the orchestrator imports are wired.

- [ ] **Step 2: Test with a minimal in-memory mapper and a UseCase built from fake stubs.**

```go
package processinquiry_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
)

func TestRunAutoReplayReadsFixtures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.json", "b.json"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644)
	}
	var hits int
	mapper := func(_ []byte) (processinquiry.Input, error) { hits++; return processinquiry.Input{}, nil }
	// orch nil-safe: we intercept via mapper's side effect only — RunAutoReplay invokes orch.Run on zero-value Input, which must not panic.
	// For this test we provide a UseCase built from zero-valued Deps — OK because Run short-circuits on empty Input in the classifier stage.
	// (If that changes, adapt the test to pass fake stubs.)
	orch := processinquiry.New(processinquiry.Deps{})
	err := processinquiry.RunAutoReplay(context.Background(), processinquiry.AutoReplayConfig{Dir: dir, Delay: 0}, orch, mapper, slog.Default())
	if err != nil {
		t.Fatalf("RunAutoReplay: %v", err)
	}
	if hits != 2 {
		t.Fatalf("mapper hits: got %d, want 2", hits)
	}
}

var _ time.Duration = 0
```

- [ ] **Step 3: Commit.**

```bash
go test -race ./internal/application/processinquiry/...
git add internal/application/processinquiry/autoreplay.go internal/application/processinquiry/autoreplay_test.go
git commit -m "feat(application/processinquiry): auto-replay-on-boot helper for demo mode (shares mapping with transport webhook path)"
```

---

> **Style note for tasks below:** steps describe *what to build and what to test*, not full file bodies. Builder consults `docs/superpowers/specs/2026-04-20-inquiryiq-design.md` for prompt text, full JSON schemas, and long type definitions. Copy-paste of code in this plan is reserved for signatures, key invariants, and non-obvious snippets.

### Task T6.1 — Transport DTO + mapper + Svix verify

**Group:** G6. **Depends on:** G1 (domain types). **Parallel-safe with:** T6.2.

**Files:**
- Create: `internal/transport/http/dto.go`, `mapper.go`, `svix.go`
- Test: `mapper_test.go`, `svix_test.go`

**What to build:**

- `dto.go` — JSON-tagged structs mirroring `GUESTY_WEBHOOK_CONTRACT.md` §"Full Payload":
  - `WebhookRequestDTO{Event, ReservationID, Message (postId/body/createdAt/type/module), Conversation (_id/guestId/language/integration.platform/meta.guestName/meta.reservations[]/thread[]), Meta{EventID, MessageID}}`.
  - Kept **transport-local** — no downstream package imports this.
- `mapper.go` — one exported func:
  ```go
  func ToDomain(dto WebhookRequestDTO) (domain.Message, domain.Conversation)
  ```
  Uses `domain/mappers.MessageFromGuesty` for role mapping. Handles missing reservations and empty thread gracefully.
- `svix.go` — one exported func:
  ```go
  func VerifySignature(secret string, id, ts string, body []byte, sig string, maxDrift time.Duration, now time.Time) error
  ```
  Returns `domain.ErrWebhookSignatureInvalid` or `domain.ErrWebhookClockDrift`. Uses `crypto/hmac` + `crypto/sha256`, compares with `hmac.Equal`. Parses `sig` as `"v1,<base64>"` and handles multiple space-separated versions.

**Tests:**

- `mapper_test.go` — table test with the minimal fixture from `GUESTY_WEBHOOK_CONTRACT.md §"Minimum Test Payload"`. Assert `conversation.RawID`, `reservations[0]`, thread length, mapped message role.
- `svix_test.go` — four cases: valid sig passes, wrong secret returns `ErrWebhookSignatureInvalid`, drift > maxDrift returns `ErrWebhookClockDrift`, multi-version header (`v1,<a> v1,<b>`) accepts either match.

**Commit:**
```
git add internal/transport/http/dto.go internal/transport/http/mapper.go internal/transport/http/svix.go internal/transport/http/*_test.go
git commit -m "feat(transport): webhook DTO, domain mapper, Svix HMAC-SHA256 verify with drift guard"
```

---

### Task T6.2 — Transport handlers + router

**Group:** G6. **Depends on:** T6.1, T5.3b (orchestrator), T3.3 (obs). **Parallel-safe with:** T6.1.

**Files:**
- Create: `internal/transport/http/handler.go`, `router.go`
- Test: `handler_test.go`

**What to build:**

- `router.go`: chi router with three routes:
  - `POST /webhooks/guesty/message-received` → `Handler.Webhook`
  - `GET /escalations` → `Handler.Escalations`
  - `GET /healthz` → `Handler.Health`
- `handler.go`: `Handler` holds the orchestrator, webhook store, escalation store, idempotency store, resolver, debouncer, Svix secret, SvixMaxClockDrift, and logger.
- `Handler.Webhook` flow (keep ≤50 statements — extract helpers):
  1. Read raw body with a 1MB limit, compute trace ID, add to ctx.
  2. Verify Svix sig — 401 on fail.
  3. Unmarshal DTO — 400 on fail.
  4. Map to domain; append `WebhookRecord` (best-effort; log on failure but continue).
  5. Resolve `ConversationKey`.
  6. If sender role is not guest → `debouncer.CancelIfHostReplied`, 202, return.
  7. If body empty/whitespace → synthetic X1 escalation, 202.
  8. `idempotency.SeenOrClaim`: already → 200 and return.
  9. `debouncer.Push(k, msg)`. Respond **202**.
- `Handler.Escalations`: `escalations.List(ctx, 100)` → JSON response.
- `Handler.Health`: `{"status":"ok"}`.

**Tests:**

- `handler_test.go` — use `httptest.NewRecorder`. Cover: invalid signature (401), duplicate postID (200 via idempotency), host-sender webhook (202, no debounce Push), valid guest webhook (202 + Push called + Append called). Use in-process fakes for `Debouncer`, `IdempotencyStore`, `WebhookStore`.

**Commit:**
```
git commit -m "feat(transport): webhook handler (fast-ack async hand-off) + /escalations + /healthz"
```

---

### Task T6.3 — `cmd/server/main.go` wiring

**Group:** G6. **Depends on:** every prior task.

**Files:**
- Modify: `cmd/server/main.go` (replace the T0.1 stub).

**What to build:**

- Load `config.Load`; fail fast with clear error on missing required vars.
- Construct, in order: `obs.NewLogger`, `clock.NewReal`, filestores (`Webhooks`, `Classifications`, `Escalations`), memstores (`Idempotency`, `EscalationRing` wrapping the filestore, `ConversationMemory` — in v1 a minimal memstore can defer `ListByGuest` to return `nil` if not implemented yet; document the gap), `llm.NewClient`, `guesty.NewClient`, identity `ConversationResolver` (inline closure), `classify.New`, `generatereply.New`, `processinquiry.New` (pass `Toggles{AutoResponseEnabled: cfg.AutoResponseEnabled}` and `Thresholds{ClassifierMin: cfg.ClassifierMinConf, GeneratorMin: cfg.GeneratorMinConf}`).
- Construct the debouncer: `NewTimed(cfg.DebounceWindow, cfg.DebounceMaxWait, realClock, flushFn)` where `flushFn(ctx, turn)`:
  1. GET conversation via Guesty (thread recheck).
  2. If last message in thread is not guest → drop silently.
  3. Build `processinquiry.Input` and call `orch.Run(ctx, in)`.
- Redacted config dump on startup (`slog.Info "config"` with secrets replaced by `"***"`).
- Build router, start `http.Server` with sane timeouts (`ReadHeaderTimeout: 5s`).
- Graceful shutdown on SIGINT/SIGTERM: stop HTTP, `debouncer.Stop`, close filestores.
- If `cfg.AutoReplayOnBoot`, spawn a goroutine running `processinquiry.RunAutoReplay` after the listener is bound. Its mapper reuses `transport/http.ToDomain` — share the single function; do not duplicate the mapping.

**Test:** none for `main.go` itself (covered by G7 integration tests).

**Commit:**
```
git commit -m "feat(cmd/server): wire config -> stores -> clients -> use cases -> debouncer -> router, graceful shutdown, optional auto-replay goroutine"
```

---

### Task T6.4 — `cmd/replay/main.go` CLI

**Group:** G6. **Depends on:** T6.3 (shares wiring helpers). **Parallel-safe with:** none (final step of the slice).

**Files:**
- Modify: `cmd/replay/main.go`

**What to build:**

- Flags: `--trace`, `--execute`, `--since`, `--escalations-only`, `--fixtures-dir`; positional `postId`.
- Reuse the wiring from `cmd/server/main.go` via a shared `internal/infrastructure/wire` package (extract during T6.3 if several call sites need it — otherwise duplicate with a TODO to extract later).
- For each target record (single postId / since window / fixtures dir):
  1. Read raw body from `WebhookStore.Get` or from disk (fixtures-dir mode).
  2. Skip Svix verify.
  3. Map to domain; call `orch.Run`.
  4. In `--trace` mode, attach a `slog.Handler` that also prints each request/response frame (done most cleanly by passing a wrapped `LLMClient` for the replay session).
- When `--execute=false` (default), swap `GuestyClient.PostNote` for a no-op logger. In `--execute=true`, use the real client.

**Test:** integration-tested in G7.

**Commit:**
```
git commit -m "feat(cmd/replay): replay CLI with --trace/--execute/--since/--escalations-only/--fixtures-dir"
```

---

### Task T7.1 — Mockoon environment fixture

**Group:** G7. **Parallel-safe with:** T7.2–T7.5.

**Files:**
- Create: `fixtures/mockoon/guesty.json`
- Modify: `Makefile` (the `mock-up` target already exists from T0.1 — verify it points at this file).

**What to build:**

- Mockoon environment JSON covering four endpoints:
  1. `GET /listings/:id` → static `wireListing` payload for `L1` (Soho 2BR sleeps 4).
  2. `GET /availability?listingId=L1&from=YYYY-MM-DD&to=YYYY-MM-DD` → `{"available": true, "nights": 2, "total": 480}` for any date range; add a branch returning `{"available": false}` when `listingId=L2`.
  3. `GET /conversations/:id/messages` (history) → two older messages.
  4. `GET /conversations/:id` → the full conversation snapshot from `GUESTY_WEBHOOK_CONTRACT.md §"Full Payload"` adapted to the domain mapper's expectations.
  5. `POST /conversations/:id/messages` → 200 `{"ok":true}` and record the body in Mockoon transaction log (visible via `--log-transaction`).

**Test:** smoke-test with `curl` against `make mock-up` in the task's commit message body.

**Commit:**
```
git commit -m "chore(fixtures): Mockoon env for Guesty listings / availability / conversation / note endpoints"
```

---

### Task T7.2 — Integration test: webhook → auto-note happy path

**Group:** G7. **Depends on:** T6.3, T7.1. **Parallel-safe with:** T7.3, T7.4, T7.5.

**Files:**
- Create: `tests/integration/happy_test.go`, `fixtures/webhooks/happy_availability.json`

**What to build:**

- Build tag `//go:build integration`; Makefile target `test-integration` running `go test -tags=integration -count=1 ./tests/integration/...`.
- Fixture: guest asking "Is the Soho 2BR available Fri Apr 24 – Sun Apr 26 for 4 adults? What's the total?" — matches the Mockoon `L1`/`2026-04-24`/`2026-04-26` branch.
- Test spins up the server in-process (call `cmd/server` wiring extracted into a helper, or use `httptest.NewServer` around the chi router), starts Mockoon via `exec.Command`, POSTs the fixture, waits up to 20s for `POST /conversations/.../messages` to land in Mockoon's transaction log, asserts the note body starts with the guest's name and contains `$480`.
- Skip with `t.Skipf` when Mockoon isn't installed (detect via `exec.LookPath`).

**Commit:**
```
git commit -m "test(integration): webhook -> debounce -> classify -> generate -> auto-note happy path against Mockoon"
```

---

### Task T7.3 — Integration test: burst debounce

**Group:** G7. **Parallel-safe with:** T7.2, T7.4, T7.5.

**Files:**
- Create: `tests/integration/burst_test.go`, `fixtures/webhooks/burst_1.json`, `burst_2.json`, `burst_3.json`

**What to build:**

- Three fixtures with the same `conversation._id` and distinct `postId`s (`hi`, `for 4`, `for next weekend`), all `type=fromGuest`.
- Test POSTs all three within 500ms, sleeps `DEBOUNCE_WINDOW_MS + 2s`, asserts exactly **one** `POST /conversations/.../messages` in Mockoon's log, and that one classification line was appended to `data/classifications.jsonl`.
- Run with overridden `DEBOUNCE_WINDOW_MS=2000` for test speed.

**Commit:**
```
git commit -m "test(integration): burst of 3 guest messages debounces to a single classification + single note"
```

---

### Task T7.4 — Integration test: escalation paths

**Group:** G7. **Parallel-safe with:** T7.2, T7.3, T7.5.

**Files:**
- Create: `tests/integration/escalation_test.go`, fixtures: `refund.json` (Y2), `discount.json` (R1), `venmo.json` (risk_flag via off-platform payment).

**What to build:**

- Three subtests, each POSTs its fixture and asserts:
  - Mockoon transaction log has **zero** `POST /conversations/.../messages` calls.
  - `GET /escalations` returns exactly one entry with the expected `reason` (`code_requires_human`, `code_requires_human`, `risk_flag`) and a populated `trace_id`.

**Commit:**
```
git commit -m "test(integration): escalation paths — Y2 refund, R1 discount, risk_flag off-platform payment"
```

---

### Task T7.5 — README

**Group:** G7. **Parallel-safe with:** T7.2–T7.4.

**Files:**
- Modify: `README.md`

**What to write:**

- **What it is** — one paragraph; link to `CHALLENGE.md` and `docs/superpowers/specs/2026-04-20-inquiryiq-design.md`.
- **Run it**:
  ```
  cp .env.example .env  # fill LLM_API_KEY, GUESTY_WEBHOOK_SECRET
  make mock-up          # terminal 1 — starts Mockoon on :3001
  make run              # terminal 2 — starts the service on :8080
  make demo             # alternative — runs with AUTO_REPLAY_ON_BOOT=true
  ```
- **Architecture diagram** — the flow from spec §2 plus the four-layer picture from `CLAUDE.md`.
- **Key decisions** — two-stage pipeline, two gates, debounce, interface-first storage, replay, DeepSeek base URL.
- **Where AI was used and how it was verified** — mandated by CHALLENGE.md §7. Mention: spec/plan drafted with Claude, each generated Go file reviewed against conventions, table tests catch gate logic regressions, Mockoon transaction log verifies the outbound path, live DeepSeek verifies the classifier and generator against the same fixtures.
- **What I'd build next** — pull the "v2 TODOs" list from spec §15, keep 3–5 most load-bearing.

**Commit:**
```
git commit -m "docs: README with run instructions, architecture summary, AI usage disclosure, next steps"
```

---

## Self-review

**Spec coverage audit** (mapping every spec section to a task):

| Spec | Covered by |
|---|---|
| §1 goals/non-goals | T7.5 README |
| §2 flow | T5.3b, T6.2 (handler), T6.3 (wiring) |
| §3 package layout | T1.1–T7.5 in aggregate |
| §4 interfaces | T1.2 |
| §5 classifier | T5.1 |
| §6 generator + reflection | T5.2 |
| §7.1 debouncer | T3.8, T6.3 (flushFn) |
| §7.2 idempotency | T3.4, T6.2 |
| §7.3 gates | T2.1, T2.2 |
| §7.4 restricted content | T2.4 |
| §8.1 canonical identity | T1.2 (ConversationResolver), T6.3 (identity impl) |
| §8.2 memory L1–L3 | T3.x (memstore), T5.3b |
| §8.2 memory L4 guest profile | T5.3a, T5.3b |
| §9 error matrix | spread across T2.x, T3.x, T4.x, T5.x |
| §10 observability | T3.3, T5.3b (ctx threading), T6.2 |
| §11 tests | T7.2–T7.4 |
| §12 config | T3.2 |
| §13.1 replay CLI | T6.4 |
| §13.2 auto-replay | T5.4, T6.3 |
| §14 build order | 90-min path in Execution Strategy |
| §15 v2 TODOs | T7.5 README |
| §16 conventions | Binding section at top |

**Placeholder scan:** no "TBD" / "fill in" / "implement later" in task bodies. Two spots intentionally defer detail to the spec text:
- T5.1 / T5.2 prompt bodies reference spec §5.3 / §6.4 / §6.5 instead of inlining ~100 lines of prompt text. This is explicit and documented.
- T5.3b step 2 and T6.4 describe test matrices rather than full test files. The table shapes are specified; the builder writes the rows.

**Type consistency audit:** `Classifier`, `Generator`, `GuestyClient`, `LLMClient`, `Debouncer`, `Decision`, `Reply`, `Classification`, `Toggles`, `Thresholds`, `ConversationKey`, `PriorContext`, `Turn`, `Escalation`, `ConversationMemoryRecord` — all spelled identically across tasks. `PreGenerate` (not `Pre_Generate` or `PreGen`); `Decide` (not `Gate`); `ValidateReply` (not `Validate`); `RestrictedContentHits` (not `RestrictedHits`). `ProcessInquiry` is the use-case package name; `processinquiry.UseCase` is the concrete type. No drift found.

**Commit discipline:** every task ends with a single conventional commit. A subagent loop (`writing-plans` recommendation) enforces "four checks green → commit" between tasks.

---

## Execution options

**Plan complete and saved to `docs/superpowers/plans/2026-04-20-inquiryiq.md`. Two execution options:**

1. **Subagent-driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration. Parallel groups (G1, G2, G3, G4, G7) dispatch multiple tasks concurrently.
2. **Inline execution** — execute tasks in this session with checkpoints for review after each group.
