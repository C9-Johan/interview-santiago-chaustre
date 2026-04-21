# InquiryIQ — Cloud9 Interview Vertical Slice

Go service that receives a Guesty `reservation.messageReceived` webhook,
classifies the guest message with an LLM, generates a C.L.O.S.E.R. reply via an
agent loop that calls Guesty tools for real listing/availability facts, and
either auto-sends the reply as an internal Guesty note or escalates the turn
to a human — based on a deterministic rules gate, not the LLM's opinion.

See [`CHALLENGE.md`](./CHALLENGE.md) for the brief and
[`GUESTY_WEBHOOK_CONTRACT.md`](./GUESTY_WEBHOOK_CONTRACT.md) for the real
webhook shape.

---

## Run it

### Prerequisites

- Go 1.26+
- [Mockoon CLI](https://mockoon.com/cli/) for a local Guesty stand-in:
  `npm i -g @mockoon/cli`
- A DeepSeek or OpenAI API key (optional — only needed to hit a real LLM).

### Demo (no cloud dependencies)

```sh
# Terminal 1 — fake Guesty
make mock-up

# Terminal 2 — service with auto-replay that posts the fixtures to itself
export GUESTY_WEBHOOK_SECRET=whsec_demo
export LLM_API_KEY=sk-xxx           # DeepSeek works via LLM_BASE_URL default
make demo
```

`make demo` boots the server with `AUTO_REPLAY_ON_BOOT=true`, which waits a
short delay and then POSTs every fixture in `fixtures/webhooks/` to the
service's own webhook endpoint with valid Svix signatures. You'll see
classifications, auto-sends, and escalations flow through the logs.

### Hit the webhook manually

```sh
export GUESTY_WEBHOOK_SECRET=whsec_demo
export LLM_API_KEY=sk-xxx
make build && ./tmp/server
```

Then from a second shell sign and POST a fixture (the `cmd/replay` CLI does
this for you — see `cmd/replay/main.go`).

### Tests

```sh
make test                # unit tests + race detector
make test-integration    # end-to-end: boots the service against Mockoon + fake LLM
make lint                # golangci-lint, Sonar-style gate
```

The integration tests auto-skip with a helpful message when `mockoon-cli` is
not on `PATH` — they're gated, never hard dependencies.

---

## Architecture

Four layers, one-way dependency (outer → inner). Domain knows nothing about
transport or infra; infra imports domain but never the reverse.

```
cmd/
  server/                    main.go — wires dependencies, serves HTTP
  replay/                    CLI to re-feed persisted webhook bodies through the pipeline
internal/
  transport/http/            handlers, chi router, Svix signature verification, DTOs
  application/
    processinquiry/          orchestrator: prior-context → classify → GATE 1 → generate → GATE 2 → send/escalate
    classify/                Stage A: one LLM call, strict JSON schema, retry-once-on-invalid
    generatereply/           Stage B: agent loop calling get_listing/check_availability/get_conversation_history
    decide/                  pure GATE 1 + GATE 2 rules, reply validator, restricted-content filter
  domain/                    entities, value objects, taxonomy codes, sentinel errors
    repository/              exported interfaces (GuestyClient, LLMClient, stores, resolver, debouncer, clock)
    mappers/                 pure funcs at layer boundaries (webhook → domain)
  infrastructure/
    guesty/                  HTTP client against the real Guesty Open API (listings, calendar, conversations, send-message)
    llm/                     OpenAI-compatible SDK with configurable BaseURL (DeepSeek drops in without code changes)
    debouncer/               per-conversation timed debouncer with max-wait clamp
    store/memstore/          in-memory idempotency, escalation ring, conversation memory
    store/filestore/         JSONL-backed webhook log, classification log, escalation log (durable replay source)
    config/                  env-var loader with typed defaults
    clock/                   real + fake wall clocks behind an interface
    obs/                     trace-id plumbing, structured slog helpers
fixtures/
  mockoon/guesty.json        Mockoon environment modelling the real Guesty API shapes
  webhooks/*.json            Signed-webhook fixtures: happy, burst, escalation paths
tests/
  integration/               end-to-end tests with a real Mockoon subprocess + scripted fake LLM
```

### Request flow

1. **HTTP handler** reads the raw body once (needed for signature verification),
   verifies the Svix `v1,<base64-hmac-sha256>` signature against the
   `svix-id`/`svix-timestamp`/`raw body` tuple with a 5-minute drift check,
   dedupes on `(conversation key, postId)`, records the raw webhook for
   replay, and hands the message to the debouncer. Returns `202` in
   milliseconds.

2. **Debouncer** merges back-to-back messages on the same conversation into
   one `domain.Turn` with a 15s window (configurable) clamped by a 60s
   max-wait, then fires the flush callback. Host replies cancel pending
   flushes.

3. **Orchestrator** (`processinquiry`):
   - Loads prior conversation memory + thread context.
   - Calls **Stage A classifier** — one LLM call, schema-validated JSON
     output, retry-once-on-invalid.
   - Runs **GATE 1** (`decide.PreGenerate`): drops turns whose code is not in
     the low-risk set, sets `risk_flag`, falls below the classifier
     confidence threshold, or hits the always-escalate codes (Y2, Y5, R1, R2).
   - Calls **Stage B generator** — agent loop against the real Guesty client
     via OpenAI tool calls (`get_listing`, `check_availability`,
     `get_conversation_history`). Max-turns reflection prompt guarantees a
     `domain.Reply` even when the budget runs out.
   - Runs **GATE 2** (`decide.Decide`): re-checks the classifier verdict,
     confidence on the generator side, C.L.O.S.E.R. beat coverage, hedging
     language, and the restricted-content regex (off-platform payments,
     address leakage, guarantee language, discount offers).
   - Calls Guesty `POST /communication/conversations/:id/send-message` with
     `type: "note"` on auto-send; otherwise records a `domain.Escalation`.
   - Writes classification + escalation records to the durable JSONL stores
     and updates the conversation memory.

### Key design decisions

- **LLM-agnostic client.** `infrastructure/llm` wraps `sashabaranov/go-openai`
  with a configurable `BaseURL`, so DeepSeek is the default but OpenAI drops
  in via one env var. No hard-coded provider URLs.
- **Real Guesty Open API shapes, not the simplified brief.** The infra client
  uses `_id`, `accommodates`, newline-separated `houseRules`,
  `prices.basePrice`, `address.neighborhood`, the `/availability-pricing/api/calendar/...`
  endpoint, and the `/communication/conversations/:id/send-message` route.
  The `endDate` query param is inclusive, so the client translates `to` →
  `to.Add(-24h)` and folds per-day availability/pricing into a single
  `domain.Availability` value.
- **Consumer-side interfaces.** Every external dependency (HTTP clients,
  stores, clock, debouncer) is declared as a small unexported interface in
  the consumer package — accept interfaces, return structs. Redis or
  Postgres stores drop in later without touching the application layer.
- **Mockoon over in-process fakes.** Guesty is mocked with a real HTTP
  server (Mockoon) so the infrastructure client exercises its full
  serialize/HTTP/deserialize path, including status-code handling, auth
  headers, and timeouts. The LLM side uses an `httptest.Server` with
  scripted `onChat` callbacks to keep the assertions deterministic.
- **Mappers at every layer boundary.** Webhook DTOs map to domain in
  `transport/http/mapper.go`; domain maps to Guesty wire types inside
  `infrastructure/guesty/types.go`. No struct is shared across layers.
- **Sonar-style lint gate.** `.golangci.yml` enforces `funlen 100/50`,
  `cyclop 30`, `gocognit 20`, `nestif 5`, `dupl 150`, and forbids
  `//nolint` / `#nosec` in non-generated code. Test files are exempt from
  size/complexity/duplication checks but still lint for correctness.

---

## What I'd build next

- **Persistent idempotency + classification/escalation stores.** Swap
  `memstore` + JSONL for Postgres (idempotency, escalations) and Redis
  (debouncer buffer) behind the existing interfaces. No application-layer
  change required.
- **Operator UI / Slack notifier.** A tiny web panel or a Slack webhook on
  every `domain.Escalation` so a human can pick up the turn inside 5
  minutes. The escalation store already carries everything a reviewer needs.
- **Observability hook-up.** `obs` threads trace IDs through context;
  plumbing them into OpenTelemetry + structured log shipping is a one-day
  addition.
- **Confidence calibration telemetry.** Log the classifier's
  self-reported confidence alongside the observed auto-reply acceptance /
  rejection signal so the 0.65 threshold becomes data-driven, not a guess.
- **Multi-tenant.** Wire `AccountID` through the domain + stores so one
  instance can handle multiple Cloud9 accounts. Today it's implicit-single.
- **Replay → re-classify.** `cmd/replay` already walks the JSONL log; add a
  `--reclassify` mode that re-runs only Stage A with a new prompt so we can
  diff classifications against a baseline before shipping prompt changes.

---

## AI usage & verification

This slice was built with Claude Code (Anthropic's CLI for Claude) as the
primary AI collaborator, working in per-task git worktrees via `worktrunk`
and committing small, atomic changes.

### Where AI helped

- **Scaffolding the four-layer structure.** I described the boundaries
  (transport / application / domain / infrastructure) and the non-negotiable
  constraints (Svix signing, consumer-side interfaces, configurable LLM
  BaseURL) in `CLAUDE.md`; Claude Code generated the initial package
  layout and constructor wiring.
- **Prompt + schema authoring for Stage A classifier.** I iterated on the
  system prompt and JSON schema interactively, then had Claude produce the
  Go-side schema validator using `gojsonschema`. I reviewed both and
  rejected a first attempt that accepted free-form `additional` entities
  with no key pattern — current schema enforces `^[a-z][a-z0-9_]{1,39}$`.
- **Agent loop for Stage B generator.** The tool dispatcher pattern
  (`runTool` → typed argument struct → Guesty client call → JSON result
  fed back as `role: tool`) is a direct adaptation of OpenAI's function
  calling docs; Claude wrote the dispatcher, I rewrote the reflection-prompt
  fallback so max-turns never crashes the pipeline.
- **Mockoon environment JSON + integration test harness.** Deterministic
  data for listings / availability / conversations / posts / send-message.
  The integration tests (`tests/integration/*_test.go`) were paired:
  Claude generated the first draft, I pruned `//nolint` suppressions
  (forbidden by the convention), fixed the fake LLM type signatures, and
  added path-based `gosec` exclusions where the subprocess / logfile
  access is genuinely required.

### How I verified it

- **Compile + vet + lint gate on every worktree** — `go build ./...`,
  `go vet ./...`, `golangci-lint run` must all exit clean before a commit.
- **Race-detector unit tests** — `go test -race -count=1 ./...` on every
  merge; the debouncer and in-memory stores are concurrency-sensitive and
  the race detector catches accidental shared state.
- **End-to-end integration tests** — Mockoon + scripted fake LLM cover the
  happy path auto-note, burst-debounce collapse, and three escalation
  reasons. When `mockoon-cli` is absent the tests skip with a clear hint.
- **Reviewed every AI-written line.** Where I disagreed with the design
  (`//nolint` sprinkles, accidentally dropping the C.L.O.S.E.R. validator
  on aborted replies, the first pass at Guesty's calendar endDate semantics)
  I rewrote the code and left the convention-matching version in.

### Where AI got it wrong

- First-pass Guesty client used the *simplified* API paths from
  `CHALLENGE.md §5.2` (`/listings/{id}`, `/availability`) — I caught it
  before the integration test layer and refactored against the real
  Guesty Open API endpoints.
- First-pass integration helper leaned on `//nolint:gosec` and
  `//nolint:revive` to silence linter warnings the project forbids; I
  replaced the suppressions with a path-scoped exclusion in
  `.golangci.yml` documented with a comment.
- Initial reply validator accepted replies whose `CloserBeats.SellCertainty`
  was `true` but had never called `check_availability` — a subtle
  hallucination path. Added an explicit `sell_certainty_without_availability`
  rule in `decide.ValidateReply`.
