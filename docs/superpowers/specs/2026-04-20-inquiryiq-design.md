# InquiryIQ — Design Spec

**Date:** 2026-04-20
**Author:** Santiago Chaustre
**Status:** Draft (pre-implementation)
**Target:** 90-minute vertical slice of the guest-inquiry auto-reply loop against the Guesty + OpenAI-compatible contracts in `CHALLENGE.md` and `GUESTY_WEBHOOK_CONTRACT.md`.

---

## 1. Goal & Non-Goals

### Goal
Ship a Go service that:

1. Receives Guesty message-received webhooks (Svix-signed).
2. Acknowledges within <200ms, then processes async.
3. Classifies the guest turn using the Traffic Light taxonomy (§6 of the brief).
4. Generates a C.L.O.S.E.R.-structured reply via an agentic LLM loop with tool-calling for Guesty lookups.
5. Applies a deterministic, rules-based gate to decide auto-send vs. escalate.
6. Posts auto-replies back to Guesty as internal notes (`type: "note"`), or records an escalation for a human operator.

### Non-Goals (v1)
- UI / admin console.
- Production deployment.
- Multi-pod scale-out (single-process is fine for the slice; the interfaces permit it).
- Cross-conversation or cross-guest memory.
- Real JSON-schema-strict tool calls (DeepSeek doesn't honor full JSON-schema mode; we validate in Go).

### Evaluation criteria it targets (from CHALLENGE.md §3)
- **Project thinking:** explicit gates, interface-first extension points, replay for reproducibility.
- **Architecture:** layered (transport / domain / pipeline / integrations / storage), interfaces on every external boundary.
- **Software quality:** typed outputs, deterministic gate as a pure function, idempotency, structured logs with trace IDs.
- **Agency:** addresses under-specified concerns the brief flags — debounce, idempotency, restricted-content filter, replayability.
- **AI fluency:** two-stage pipeline (classifier → gated → generator agent loop with tools → reflection on failure).
- **Pragmatism:** JSONL + in-memory v1 impls behind the interfaces that Redis/MongoDB/SQLite will implement later.

---

## 2. High-level flow

```
webhook in
  -> transport: svix verify, 202 ack, persist raw (always)
  -> resolver: raw conversation._id -> canonical ConversationKey
  -> idempotency: SeenOrClaim(k, postId) — drop if already complete
  -> sender-role check: if not guest, cancel debounce for k and stop
  -> debouncer.Push(k, msg)
       ... sliding 15s window (hard cap 60s) ...
  -> flush(k) -> Turn{k, messages, lastPostId}
  -> memory.Get(k) — prior summary + known entities
  -> classifier.Classify(turn, priorContext) -> Classification
  -> GATE 1 (pre-generation): escalate if code in {Y2, Y5, R1, R2},
     confidence < 0.65, or risk_flag — stop with escalation record
  -> thread recheck via guesty.GetConversation(k)
     if host already replied since flush -> stop silently
  -> generator.Generate(turn, classification, priorContext) -> Reply
     agent loop, up to 4 turns, tools:
       get_listing
       check_availability
       get_conversation_history
     on max turns: reflection call (tools disabled), returns abort_reason="max_turns"
  -> GATE 2 (post-generation): validate reply + restricted-content regex
  -> Decide(classification, reply, validationIssues, toggles)
       AutoSend=true -> guesty.PostNote(k, body)
       AutoSend=false -> escalationStore.Record(...)
  -> memory.Update(k, merge entities + outcome)
  -> idempotency.Complete(k, postId)
```

Two gates are deliberate: the first one kills bad cases cheaply before any generator tokens are spent; the second protects against LLM drift even when the classifier was clean.

---

## 3. Package layout

```
cmd/
  server/main.go            # wire everything, start http.Server
  replay/main.go            # ./replay <postId> — re-run a stored webhook through the pipeline
internal/
  config/                   # env loader
  transport/
    webhook.go              # POST /webhooks/guesty/message-received
    svix.go                 # HMAC-SHA256 verify on raw body
    health.go               # GET /healthz, GET /escalations
  domain/
    types.go                # Message, Turn, Classification, Reply, Decision, Observation
    conversation.go         # ConversationKey, ConversationResolver
    classifier.go           # Classifier interface
    generator.go            # Generator interface, agent loop
    gate.go                 # Decide(...) pure function
    closer.go               # post-generation validation (beats, hedging, length)
    restricted.go           # regex-based restricted-content hits
    memory.go               # ConversationMemory interface (summarizer)
    ports.go                # cross-cutting interfaces (GuestyClient, LLMClient)
  pipeline/
    debounce.go             # Debouncer interface + timedDebouncer
    orchestrator.go         # the flow above
    idempotency.go          # uses IdempotencyStore interface
  integrations/
    guesty/                 # http impl of GuestyClient against a base URL (Mockoon in dev)
      client.go
      types.go
    llm/
      client.go             # wraps sashabaranov/go-openai, BaseURL set for DeepSeek
      tools.go              # openai.Tool definitions
  storage/
    ports.go                # all store interfaces live here
    filestore/              # JSONL concrete impls
    memstore/               # in-memory concrete impls
  obs/
    logger.go               # slog JSON handler, trace_id threading
fixtures/
  webhooks/*.json
  mockoon/guesty.json       # Mockoon environment export
data/                       # created at runtime, JSONL + snapshots
docs/
  superpowers/specs/2026-04-20-inquiryiq-design.md  # this file
```

---

## 4. Interface contracts (single source of truth)

Every external dependency is an interface in the consumer package. Concrete impls live in their own subpackage and are wired at `cmd/server/main.go`. Tests use fakes implementing the same interface. v2 swaps (Redis, MongoDB, SQLite) are drop-in — no domain code changes.

### 4.1 Domain-owned external ports
```go
// internal/domain/ports.go

type GuestyClient interface {
    GetListing(ctx context.Context, id string) (Listing, error)
    CheckAvailability(ctx context.Context, listingID string, from, to time.Time) (Availability, error)
    GetConversationHistory(ctx context.Context, convID string, limit int, beforePostID string) ([]Message, error)
    GetConversation(ctx context.Context, convID string) (Conversation, error) // for thread recheck
    PostNote(ctx context.Context, conversationID, body string) error
}

type LLMClient interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type Classifier interface {
    Classify(ctx context.Context, turn Turn, prior PriorContext) (Classification, error)
}

type Generator interface {
    Generate(ctx context.Context, turn Turn, cls Classification, prior PriorContext) (Reply, error)
}

type ConversationResolver interface {
    Resolve(ctx context.Context, w WebhookPayload) (ConversationKey, error)
}

type ConversationMemory interface {
    Summary(ctx context.Context, k ConversationKey, thread []Message, window int) (string, error)
}

type Clock interface {
    Now() time.Time
    Since(t time.Time) time.Duration
}
```

### 4.2 Storage ports (all behind interfaces)
```go
// internal/storage/ports.go

type WebhookStore interface {
    Append(ctx context.Context, rec WebhookRecord) error
    Get(ctx context.Context, postID string) (WebhookRecord, error)
    Since(ctx context.Context, d time.Duration) ([]WebhookRecord, error) // replay --since
}

type IdempotencyStore interface {
    SeenOrClaim(ctx context.Context, k ConversationKey, postID string) (already bool, err error)
    Complete(ctx context.Context, k ConversationKey, postID string) error
}

type ClassificationStore interface {
    Put(ctx context.Context, postID string, c Classification) error
    Get(ctx context.Context, postID string) (Classification, error)
}

type EscalationStore interface {
    Record(ctx context.Context, e Escalation) error
    List(ctx context.Context, limit int) ([]Escalation, error)
}

type ConversationMemoryStore interface {
    Get(ctx context.Context, k ConversationKey) (ConversationMemoryRecord, error)
    Update(ctx context.Context, k ConversationKey, mut func(*ConversationMemoryRecord)) error
}

type ConversationAliasStore interface {
    Lookup(ctx context.Context, rawID string) (ConversationKey, bool, error)
    Link(ctx context.Context, rawIDs []string, canonical ConversationKey) error
}
```

### 4.3 v1 concrete impls → v2 swap paths

| Interface | v1 impl | v2 swap (drop-in) |
|---|---|---|
| `WebhookStore` | `filestore.JSONLWebhooks` (append-only `data/webhooks.jsonl`) | `mongo.Webhooks` (unique idx on `postId`) |
| `IdempotencyStore` | `memstore.Idempotency` + warm-start from `webhooks.jsonl` | `redis.Idempotency` (`SETNX` w/ TTL) or `sqlite.Idempotency` |
| `ClassificationStore` | `filestore.JSONLClassifications` | `mongo.Classifications` (query by `primary_code`, `created_at`) |
| `EscalationStore` | `memstore.EscalationRing` (last 500) + JSONL append | `sqlite.Escalations` (range + full-text) |
| `ConversationMemoryStore` | `memstore.Memory` + 30s snapshot to `data/memory.json` | `redis.Memory` (JSON w/ per-key TTL) |
| `ConversationAliasStore` | nil (identity resolver passes through) | `sqlite.Aliases` (many-to-one mapping) |
| `GuestyClient` | `guestyhttp.Client` (HTTP, `BaseURL` env-configured — Mockoon in dev) | same impl, real Guesty base URL |
| `LLMClient` | `deepseek.Client` (wraps `sashabaranov/go-openai` w/ `cfg.BaseURL`) | OpenAI, Anthropic, etc. — same interface |
| `Debouncer` | `timedDebouncer` (map + mutex, sliding window) | `redis.SlidingWindow` (Lua) |
| `Clock` | `realClock{}` | `fakeClock` in tests |

---

## 5. Stage A — Classifier

### 5.1 Typed output
```go
type PrimaryCode string
const (
    G1, G2 PrimaryCode = "G1", "G2"
    Y1, Y2, Y3, Y4, Y5, Y6, Y7 PrimaryCode = "Y1","Y2","Y3","Y4","Y5","Y6","Y7"
    R1, R2 PrimaryCode = "R1", "R2"
    X1     PrimaryCode = "X1"
)

type NextAction string
const (
    ActionGenerate  NextAction = "generate_reply"
    ActionEscalate  NextAction = "escalate_human"
    ActionQualify   NextAction = "qualify_question"
)

type ExtractedEntities struct {
    CheckIn     *time.Time     `json:"check_in,omitempty"`
    CheckOut    *time.Time     `json:"check_out,omitempty"`
    GuestCount  *int           `json:"guest_count,omitempty"`
    Pets        *bool          `json:"pets,omitempty"`
    Vehicles    *int           `json:"vehicles,omitempty"`
    ListingHint *string        `json:"listing_hint,omitempty"`
    Additional  []Observation  `json:"additional,omitempty"` // open bag, max 8
}

type Observation struct {
    Key        string  `json:"key"`          // snake_case, 1-40 chars
    Value      string  `json:"value"`        // <=200 chars
    ValueType  string  `json:"value_type"`   // "string"|"number"|"bool"|"list"
    Confidence float64 `json:"confidence"`   // 0..1
    Source     string  `json:"source"`       // quoted guest text, <=120 chars
}

type Classification struct {
    PrimaryCode       PrimaryCode       `json:"primary_code"`
    SecondaryCode     *PrimaryCode      `json:"secondary_code,omitempty"`
    Confidence        float64           `json:"confidence"`
    ExtractedEntities ExtractedEntities `json:"extracted_entities"`
    RiskFlag          bool              `json:"risk_flag"`
    RiskReason        string            `json:"risk_reason,omitempty"`
    NextAction        NextAction        `json:"next_action"` // advisory — Go gate is authoritative
    Reasoning         string            `json:"reasoning"`   // <=240 chars, not used downstream
}
```

### 5.2 JSON Schema (validated in Go — DeepSeek gives `json_object`, not full `json_schema`)
```json
{
  "type": "object",
  "required": ["primary_code","confidence","extracted_entities","risk_flag","next_action","reasoning"],
  "additionalProperties": false,
  "properties": {
    "primary_code":   {"enum": ["G1","G2","Y1","Y2","Y3","Y4","Y5","Y6","Y7","R1","R2","X1"]},
    "secondary_code": {"type": ["string","null"]},
    "confidence":     {"type": "number", "minimum": 0, "maximum": 1},
    "risk_flag":      {"type": "boolean"},
    "risk_reason":    {"type": "string", "maxLength": 200},
    "next_action":    {"enum": ["generate_reply","escalate_human","qualify_question"]},
    "reasoning":      {"type": "string", "maxLength": 240},
    "extracted_entities": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "check_in":    {"type": ["string","null"], "format": "date"},
        "check_out":   {"type": ["string","null"], "format": "date"},
        "guest_count": {"type": ["integer","null"], "minimum": 1, "maximum": 20},
        "pets":        {"type": ["boolean","null"]},
        "vehicles":    {"type": ["integer","null"], "minimum": 0, "maximum": 10},
        "listing_hint":{"type": ["string","null"], "maxLength": 120},
        "additional": {
          "type": "array", "maxItems": 8,
          "items": {
            "type": "object",
            "required": ["key","value","value_type","confidence","source"],
            "additionalProperties": false,
            "properties": {
              "key":        {"type": "string", "pattern": "^[a-z][a-z0-9_]{1,39}$"},
              "value":      {"type": "string", "maxLength": 200},
              "value_type": {"enum": ["string","number","bool","list"]},
              "confidence": {"type": "number", "minimum": 0, "maximum": 1},
              "source":     {"type": "string", "maxLength": 120}
            }
          }
        }
      }
    }
  }
}
```

### 5.3 System prompt
```
You are the InquiryIQ classifier for Cloud9, a short-term rental operator. You read a single guest turn (one or more messages) from a reservation conversation and emit STRICT JSON.

# Your one job
Identify THE ONE THING BLOCKING THE BOOKING and return the Traffic Light code, confidence, extracted entities, risk flag, and next_action. You do not write replies. You do not call tools.

# Taxonomy (primary_code — pick exactly one)
GREEN — high intent, ready to book
  G1 intent: "book","reserve","confirm","pay"
  G2 context: "wedding","family trip","work stay"
YELLOW — one concern blocking
  Y1 logistics: parking, directions, access
  Y2 trust/admin: deposit, refund, cancel, ID verification
  Y3 product fit: beds, layout, stairs, size
  Y4 timing: check-in/out, early, late, luggage
  Y5 permissions: pets, party, visitors, rules
  Y6 availability: dates, calendar, vacancy
  Y7 price clarity: total, fees, cleaning, taxes
RED — price resistance
  R1 haggle: discount, deal, best price
  R2 budget: "too expensive","can't afford","cheaper"
GRAY — not enough signal
  X1 vague: "hi", emoji only, "interested"

# Priority when multiple signals
RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY
If a RED signal is present, primary is RED even if other intents exist.
Example: "any discount? also is parking included?" -> primary R1, secondary Y1.

# Default bias
If ambiguous, pick GRAY (X1) or the weaker YELLOW. Never promote to GREEN without explicit high-intent language.

# Confidence calibration
0.90-1.00 unmistakable single-signal ("book it for the 24th")
0.70-0.89 clear primary, minor noise
0.50-0.69 plausible but ambiguous — borderline
<0.50    guessing — use X1 unless risk_flag=true

# risk_flag = true if ANY of these appear (also set risk_reason):
- off-platform payment ("venmo","cashapp","wire","paypal me","zelle","crypto","bank transfer")
- request for the exact street address before booking
- guarantee language ("guarantee no noise","promise no issues","100% safe")
- contact info exchange bypass ("whatsapp","text me at","email me at")
- minors traveling unaccompanied
- any illegal activity reference

# next_action rules (deterministic, no judgement)
- risk_flag=true -> "escalate_human"
- primary in {Y2,Y5,R1,R2} -> "escalate_human"
- primary == X1 -> "qualify_question"
- confidence < 0.65 -> "escalate_human"
- otherwise -> "generate_reply"

# Entity extraction — typed fields
Extract ONLY from explicit text. Do not infer. Dates must be ISO (YYYY-MM-DD) and resolved to absolute if the guest gave a relative date (use the provided current_date). Leave fields null when not stated.

# If the current guest turn is silent on an entity present in known_from_prior_turns, you MAY
# carry it forward UNCHANGED into extracted_entities, but mark it with source="prior_turn"
# in the `additional` array rather than the typed field. Do NOT assume it's still valid.

# Additional entity extraction (the `additional` array)
Beyond the typed fields, you may surface up to 8 OTHER signals that could matter for conversion, personalization, or future product work. These are NOT scored or used for routing — they are for learning and future iteration.

Only surface a signal if it's explicit enough to quote. For each, include a short `source` quote (verbatim, <=120 chars) so we can audit later.

Good examples (non-exhaustive — use judgement):
- trip_occasion (wedding, honeymoon, bachelor, birthday, work, funeral, relocation)
- trip_duration_intent ("long weekend", "month stay")
- group_type (family with kids, friends, coworkers, couple, solo)
- accessibility_need (wheelchair, no stairs, hearing)
- noise_sensitivity / sleep_priority
- work_requirements (wifi speed, monitor, desk, calls)
- flight_timing (red-eye arrival, late check-in)
- deal_breakers mentioned ("need parking", "must have AC")
- competitor_comparison ("cheaper than the one on Bleecker")
- prior_stay_signal ("stayed before", "regular customer")
- neighborhood_preference ("near Central Park")
- kitchen_use_intent ("cooking Thanksgiving dinner")

Keys must be snake_case, <=40 chars. Do not invent values. Do not duplicate the typed fields. If nothing qualifies, return an empty array.

Value encoding:
- `value_type="list"` -> `value` is JSON-ish: `["wifi","desk"]`
- `value_type="bool"` -> `value` is `"true"` or `"false"`
- `value_type="number"` -> `value` is the number as a string, e.g. `"7"`
- `value_type="string"` -> free text, <=200 chars

`confidence` here is how sure you are about THIS observation, not about the primary_code. Stay conservative.

# Output
Return ONLY a JSON object matching the schema. No prose, no code fences, no trailing commentary.
```

### 5.4 User-message template
```
current_date: {{YYYY-MM-DD}}
conversation_language: {{conversation.language}}
listing (if known): {{listing.title_or_blank}}
reservation: {check_in, check_out, confirmation_code}

known_from_prior_turns (advisory, may be stale):
{{known_entities_summary_or_blank}}

prior_thread_summary (older messages, if any):
"{{layer-1 summary or blank}}"

prior_thread (last {{THREAD_CONTEXT_WINDOW=10}} non-system, oldest -> newest):
{{#each thread}}- [{{role}} {{createdAt}}] {{body}}\n{{/each}}

---
guest_turn (classify THIS):
{{#each turn.messages}}{{body}}\n{{/each}}
```

### 5.5 Source-of-truth split
- LLM emits `next_action`, but `domain/gate.go` re-derives the routing decision from the other fields. LLM is advisory; Go is authoritative. Disagreement is logged.
- Thresholds (`CONFIDENCE_CLASSIFIER_MIN=0.65`) live in `Config`, are applied in Go, and are quoted in the prompt only so the LLM behaves — Go is the single source of truth.

### 5.6 Reliability
- DeepSeek does not honor full `json_schema`. We use `response_format: json_object` and validate in Go.
- On parse or schema failure: one retry with an appended system message ("your previous response was invalid JSON; return only the object"). Second failure → escalate with `reason="llm_malformed"`.

---

## 6. Stage B — Generator (agent loop)

### 6.1 Typed output
```go
type Reply struct {
    Body             string      `json:"body"`
    UsedTools        []ToolCall  `json:"used_tools"`
    CloserBeats      CloserBeats `json:"closer_beats"`
    Confidence       float64     `json:"confidence"`
    AbortReason      string      `json:"abort_reason,omitempty"`
    ReflectionReason string      `json:"reflection_reason,omitempty"`
    MissingInfo      []string    `json:"missing_info,omitempty"`
    PartialFindings  string      `json:"partial_findings,omitempty"`
}

type CloserBeats struct {
    Clarify, Label, Overview, SellCertainty, Explain, Request bool
}

type ToolCall struct {
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
    Result    json.RawMessage `json:"result"`
    LatencyMs int64           `json:"latency_ms"`
    Error     string          `json:"error,omitempty"`
}
```

### 6.2 Tool schemas
Three tools, declared with `FunctionDefinition.Strict: true`:

- `get_listing(listing_id)` — listing facts. Call once when facts are needed for Overview/Explain.
- `check_availability(listing_id, from, to)` — availability + total price. REQUIRED before filling the Sell-Certainty beat.
- `get_conversation_history(conversation_id, limit, before_post_id?)` — fetch older messages beyond the provided window. Only when the guest explicitly references prior context not visible.

Full `FunctionDefinition.Parameters` are defined in `internal/integrations/llm/tools.go`.

### 6.3 Agent loop
```go
for i := 0; i < maxTurns; i++ {
    resp, err := llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
        Model:               cfg.ModelGenerator,
        Messages:            messages,
        Tools:               tools,
        ToolChoice:          "auto",
        ParallelToolCalls:   true,
        Temperature:         0.3,
        MaxCompletionTokens: 600,
        ResponseFormat:      &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
    })
    if err != nil { return Reply{}, err }
    msg := resp.Choices[0].Message
    messages = append(messages, msg)
    if len(msg.ToolCalls) == 0 {
        return parseFinalReply(msg.Content, toolLog)
    }
    for _, tc := range msg.ToolCalls {
        result := runTool(ctx, tc)          // includes latency + error
        toolLog = append(toolLog, result)
        messages = append(messages, openai.ChatCompletionMessage{
            Role: openai.ChatMessageRoleTool, ToolCallID: tc.ID, Content: string(result.Result),
        })
    }
}
return reflectOnFailure(ctx, messages, toolLog) // ALWAYS returns a Reply, never errors
```

### 6.4 Reflection on max_turns
At `maxTurns` exhaustion, one final LLM call is made with **tools disabled** and the reflection system prompt appended. The response populates `Reply.AbortReason="max_turns"`, `ReflectionReason`, `MissingInfo`, `PartialFindings`. The orchestrator records this as an escalation with the reflection payload so the human operator sees *why* the LLM gave up and what was partially learned.

Reflection prompt is in `internal/domain/generator.go` as a const, reproduced verbatim in the code.

### 6.5 Generator system prompt
Full text is in `internal/domain/generator.go` (see design notes from §3 of the brainstorming session). Key invariants:
- One short paragraph, 3–5 sentences, no hedging, no emoji (unless guest used one), no generic intros.
- `check_availability` MUST be called before asserting `sell_certainty=true`.
- Never invent prices, dates, amenities, policies.
- Restricted content self-check (off-platform payment, addresses, guarantees, discounts) — always escalate via `abort_reason="policy_decline"`.
- Output is STRICT JSON matching the Reply shape.

### 6.6 Post-generation validation (pure)
`domain/closer.ValidateReply(reply, classification) []string` checks:
- `abort_reason` set → propagate.
- body length within [60, 900] runes.
- hedging regex miss.
- `Clarify` and `Request` beats both true.
- `SellCertainty=true` but `check_availability` not in `UsedTools` → **fabrication**, flag.

Any issue → second gate escalates.

---

## 7. Pipeline

### 7.1 Debouncer
Central timer-driven buffer (single manager, not goroutine-per-conversation) keyed on `ConversationKey`:
- Sliding window: each `Push` resets a 15s timer.
- Hard cap: `DEBOUNCE_MAX_WAIT_MS=60000` — total buffer lifetime, clamped.
- Dedup on `postID` inside the buffer to survive Svix mid-window retries.
- `CancelIfHostReplied(k, role)` — if sender is not guest, drop the buffer without flushing.
- `Clock` is injected (`realClock` / `fakeClock`) so debounce logic is tested without `time.Sleep`.

Post-flush the orchestrator runs in the same goroutine that fired the timer. Panics are recovered, logged with the full trace_id, and recorded as escalations (`reason="panic"`) so the idempotency entry gets marked complete.

### 7.2 Idempotency
Key is the tuple `(ConversationKey, postID)`, not just `postID`. This handles the rare case of a conversation merge across two `_id`s where raw postIds could collide after merging.

States: `inflight` (claimed but not complete), `complete` (processed, drop on re-arrival). On server restart, the `memstore.Idempotency` impl rebuilds the seen-set from `data/webhooks.jsonl` by pairing webhook records with classification records; anything in the webhook log but not in the classification log is treated as `never-processed` and eligible on next receipt.

### 7.3 Gate — `domain/gate.Decide` (pure)
Short-circuit order:
1. `!AutoResponseEnabled` → deny.
2. `reply.AbortReason != ""` → deny with detail.
3. `cls.PrimaryCode ∈ {Y2, Y5, R1, R2}` → deny (code requires human).
4. `cls.PrimaryCode ∉ {G1, G2, Y1, Y3, Y4, Y6, Y7}` → deny (not low-risk).
5. `cls.Confidence < 0.65` → deny.
6. `reply.Confidence < 0.70` → deny.
7. `cls.RiskFlag` → deny.
8. `len(validationIssues) > 0` → deny with issues.
9. `restrictedContentHits(reply.Body)` non-empty → deny with hits.
10. Otherwise → `AutoSend: true, Reason: "ok"`.

Every denial carries `Reason` (machine-readable) + `Detail` ([]string, specifics). Both are written into the `Escalation` record.

### 7.4 Restricted-content filter
Regex-only checks in `domain/restricted.go`:
- `off_platform_payment` (venmo / cashapp / zelle / paypal / wire / crypto / western union)
- `contact_bypass` (whatsapp / telegram / signal / "text me at" / "email me at" / "my number is")
- `address_leak` (crude street-address pattern)
- `guarantee_language` (guarantee / promise / 100% / "no issues")
- `discount_offer` (discount / deal / special rate / lower price)

Applied to reply body in Gate step 9. False positives are acceptable — they escalate, never block silently.

---

## 8. Conversation identity & memory

### 8.1 Canonicalization (v1: identity; v2-ready)
Transport layer never leaks raw `conversation._id`. Every downstream component receives a `ConversationKey` resolved once by `ConversationResolver`. v1 impl is identity (`raw -> ConversationKey(raw)`) with a nil alias store. v2 plugs in `sqlite.Aliases` without touching call sites.

### 8.2 Memory (three layers)
- **Layer 1 (context window):** Classifier/generator user messages include the last `THREAD_CONTEXT_WINDOW=10` non-system messages. Older messages are collapsed into a one-paragraph summary by `ConversationMemory.Summary`, cached per `(k, lastSummarizedPostID)`.
- **Layer 2 (tool):** `get_conversation_history` lets the generator pull older messages on demand when the current turn references prior context not in the window. Counts toward `maxTurns` budget.
- **Layer 3 (memory store):** `ConversationMemoryStore` holds `ConversationMemoryRecord` per canonical key — last summary, accumulated `ExtractedEntities`, `AdditionalSignals`, last classification, last auto-send/escalation timestamps, escalation reason tail. Populated on every completed turn; fed into the classifier as `known_from_prior_turns` advisory context. Classifier prompt is explicit that it's prior-turn state, not the current turn, and must not be treated as still valid without re-confirmation.

---

## 9. Error matrix

| Failure | Handler | Behavior |
|---|---|---|
| Svix signature invalid or timestamp drift > 5m | `transport/svix.go` | 401, log, do not persist |
| Unparseable JSON body | `transport/webhook.go` | 400, persist raw for audit |
| Duplicate `(convKey, postID)` | `pipeline/idempotency.go` | 200 immediate; still append to webhook log |
| Sender not guest | `transport/webhook.go` | 202, `debouncer.CancelIfHostReplied`, stop |
| Empty / whitespace body | `pipeline/orchestrator.go` | Synthetic X1 classification, escalate without LLM |
| Missing reservation (pre-booking) | classifier | `listing_id=nil` passed; generator uses `abort_reason="insufficient_facts"` if unresolvable |
| Guesty 429 | `guestyhttp.Client` | Exponential backoff + jitter, respect `Retry-After`, 3 retries |
| Guesty 5xx | same | 3 retries w/ jitter |
| Guesty 4xx non-429 | same | No retry; tool caller receives `{"error": "..."}` |
| LLM network timeout | `deepseek.Client` | 30s classifier, 45s generator; one retry on network/5xx |
| LLM unparseable JSON | classifier / generator | One reprompt attempt with validation error; then escalate `reason="llm_malformed"` |
| Tool args fail schema | agent loop | Tool returns `{"error":"invalid_arguments"}`; LLM adapts or aborts |
| Agent loop exceeds `maxTurns` | generator | Reflection call, escalate with rich reason — **never errors** |
| Orchestrator panic | `pipeline/orchestrator.go` | `defer recover()` → escalation `reason=panic`, stack in logs, idempotency marked complete |

All retry budgets are env-configurable.

---

## 10. Observability

- Structured logs via `slog.JSONHandler` to stdout.
- Mandatory attributes on every log: `trace_id`, `conversation_key`, `post_id`, `stage`, `latency_ms`.
- `trace_id` generated in `transport/webhook.go`, threaded via `context.Context`.
- Every `Escalation` record carries its `trace_id` so operators can correlate from `/escalations` to logs.
- v2 TODO: OTLP traces, Prometheus metrics, log shipping. `slog.Handler` is swappable.

---

## 11. Testing strategy

**Unit (no network, no LLM, no mocks beyond fakes implementing our interfaces):**
- `domain/gate.Decide` — exhaustive table test, one case per `Reason` branch + low-risk happy path.
- `domain/restricted.restrictedContentHits` — pos/neg per pattern.
- `domain/closer.ValidateReply` — pos/neg per issue.
- `pipeline/idempotency` — dup/inflight/complete transitions.
- `pipeline/debounce` — with `fakeClock`: burst flushes together, maxWait enforced, host-reply cancels.

**Integration (Mockoon Guesty):**
- `make mock-up` starts Mockoon with `fixtures/mockoon/guesty.json`.
- `TestWebhookToNote_HappyPath` — fixture in → auto-note observed in Mockoon log.
- `TestWebhookToEscalation_Y2` — deposit/refund inquiry → escalation recorded, no outbound.
- `TestBurst_Debounce` — three POSTs within 10s → one classifier call, one note.

**LLM (opt-in, behind `//go:build live_llm`):**
- `TestLive_Classifier_SampleFixtures` — runs against real DeepSeek when `LLM_API_KEY` is set.

**Explicitly not covered:** round-trip Svix through a real hosted webhook source; real Guesty.

---

## 12. Config (env vars)

```
PORT                            :8080
LOG_LEVEL                       info
AUTO_RESPONSE_ENABLED           true

GUESTY_WEBHOOK_SECRET           <required>
SVIX_MAX_CLOCK_DRIFT_SECONDS    300

DEBOUNCE_WINDOW_MS              15000
DEBOUNCE_MAX_WAIT_MS            60000

GUESTY_BASE_URL                 http://localhost:3001
GUESTY_TOKEN                    dev
GUESTY_TIMEOUT_MS               3000
GUESTY_RETRIES                  3

LLM_BASE_URL                    https://api.deepseek.com/v1
LLM_API_KEY                     <required>
LLM_MODEL_CLASSIFIER            deepseek-chat
LLM_MODEL_GENERATOR             deepseek-chat
LLM_CLASSIFIER_TIMEOUT_MS       30000
LLM_GENERATOR_TIMEOUT_MS        45000
LLM_AGENT_MAX_TURNS             4

CONFIDENCE_CLASSIFIER_MIN       0.65
CONFIDENCE_GENERATOR_MIN        0.70
THREAD_CONTEXT_WINDOW           10

DATA_DIR                        ./data
```

All values loaded into one `Config` struct in `cmd/server/main.go`, printed redacted at startup.

---

## 13. Replay CLI

`cmd/replay/main.go`:

```
./replay <postId>            # re-process a single stored webhook (dry-run by default)
./replay <postId> --trace    # dump full LLM message log + toolLog
./replay <postId> --execute  # actually POST the note to Mockoon
./replay --since 1h          # re-process everything in the last hour
./replay --escalations-only  # only records that previously escalated
```

Replay:
- skips Svix signature verify (raw body came from our own file, trusted),
- bypasses the debouncer (deterministic single-turn replay; batches same-convo messages within 15s of the primary record),
- runs the full classifier → generator → gate pipeline,
- defaults `GuestyClient.PostNote` to a no-op logger (override with `--execute`).

---

## 14. Build order (90-minute plan)

1. **0–15:** Skeleton, config, interfaces, package layout. `main.go` wires no-op impls. `GET /healthz` returns 200.
2. **15–30:** Webhook handler, Svix verify, JSONL webhook store, idempotency, sender-role filter. Fixtures round-trip cleanly.
3. **30–50:** Debouncer + orchestrator skeleton + classifier (real DeepSeek) + first gate. Fixture → escalation recorded, no reply yet.
4. **50–75:** Mockoon Guesty client + generator agent loop + second gate + outbound `PostNote`. Fixture → auto-note observed in Mockoon log.
5. **75–85:** Replay CLI, `GET /escalations`, restricted-content filter tuning.
6. **85–90:** README + commit.

`ConversationMemoryStore` + `get_conversation_history` tool land in step 4 if time allows; otherwise deferred to v2.

---

## 15. Explicit v2 / TODOs

- `redis.IdempotencyStore`, `redis.ConversationMemoryStore` for multi-pod scale-out.
- `mongo.WebhookStore` + `mongo.ClassificationStore` for durable + queryable history.
- `sqlite.EscalationStore` with range/full-text, backing an operator UI.
- `sqlite.ConversationAliasStore` + admin merge endpoint.
- `Notifier` interface (Slack / PagerDuty) for escalations.
- Per-listing auto-response toggle overrides.
- Cross-conversation (per-`guestId`) memory.
- Outcome feedback loop: later turn invalidating prior extraction → memory record marked "contradicted" for classifier audit.
- OTLP traces, Prometheus metrics.
- Prompt / tool versioning + shadow-mode rollout.
- Move tool-arg validation to real JSON-schema `Strict: true` when the provider supports it.
- Offline eval harness: labeled historical inquiries → classifier/gate regression scores.

---

## 16. Open questions (to confirm with interviewer)

- Is DeepSeek an acceptable stand-in for OpenAI via base-URL override, or should we assume OpenAI will be used?
- Should `AUTO_RESPONSE_ENABLED` default to on or off for the demo?
- Are there labeled historical inquiries we can use to calibrate the classifier thresholds, or are the 0.65/0.70 defaults fine for the slice?
- Should replay `--execute` be gated behind an additional env var (defense in depth against accidental prod sends)?
