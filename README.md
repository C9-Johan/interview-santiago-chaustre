# InquiryIQ — Guesty inquiry → AI reply loop

A service that ingests a Guesty `reservation.messageReceived` webhook, classifies the guest message
with an LLM ("traffic-light" taxonomy), generates a **C.L.O.S.E.R.** reply grounded in real listing
facts via tool-calling, applies a **hard-rules** auto-send gate, and posts the result back to Guesty
as an **internal note** (so nothing reaches real guests). See `CHALLENGE.md` / `GUESTY_WEBHOOK_CONTRACT.md`.

## Run it

```bash
npm install
npm test              # 43 offline tests + 5 LLM evals (skipped unless OPENAI_API_KEY is set)
npm run dev           # starts on http://localhost:3000 (mock LLM + mock Guesty, signature skipped)
```

Then exercise the loop (no credentials needed — runs fully offline on mock adapters):

```bash
# 1. Send the sample webhook → fast 200 ack, processed async
curl -s -X POST localhost:3000/webhooks/guesty/message-received \
  -H 'Content-Type: application/json' -H 'svix-id: evt_1' \
  --data @src/fixtures/sample-webhook.json

# 2. See what the bot "sent" to Guesty (the mock note sink)
curl -s localhost:3000/admin/notes

# 3. Operator kill switch — turn auto-replies off; next message escalates instead
curl -s -X POST localhost:3000/admin/auto-response -H 'Content-Type: application/json' -d '{"enabled":false}'

# 4. Same message twice (same svix-id/postId) → second returns {"status":"duplicate"}

# 5. Every accepted ack returns a requestId; inspect that request's full trace (parse → classify →
#    decide → send) — one JSON file per request, newest last
ls -t traces/ | head -1 && cat "traces/$(ls -t traces/ | head -1)"
```

### Config (all optional — defaults run offline)

| Env | Default | Purpose |
|---|---|---|
| `PORT` | `3000` | HTTP port |
| `OPENAI_API_KEY` | — | When set, uses the **real** OpenAI adapter (Vercel AI SDK). Unset → deterministic mock. |
| `OPENAI_MODEL` | `gpt-4o-mini` | Model for classify + generate |
| `SKIP_SIGNATURE` | `true` | Bypass Svix verification (so unsigned fixtures work). Set `false` + `GUESTY_WEBHOOK_SECRET` in prod. |
| `GUESTY_WEBHOOK_SECRET` | — | Svix signing secret for HMAC verification |
| `AUTO_RESPONSE_ENABLED` | `true` | Initial state of the auto-send kill switch |
| `DEBOUNCE_MS` | `0` | Per-conversation burst debounce window (0 = off) |
| `TRACE_DIR` | `traces` | Folder for per-request JSON trace files (the no-DB audit log) |

See `.env.example`.

## Architecture

**Lightweight ports & adapters** — the spirit of hexagonal without the ceremony, because this is one
endpoint. Pure domain logic has no I/O; the two external systems (LLM, Guesty) sit behind interfaces
so they're swappable and mockable; the HTTP layer is thin. `src/app.ts` is the only file that knows
about concrete implementations.

```
transport/   thin HTTP: signature verify → dedup → fast 200 → async  (webhookRouter, adminRouter, rawBody, verifySignature)
pipeline/    orchestration: parse → guards → classify → decide → send  (processMessage, debounce)
domain/      pure, unit-tested: types, taxonomy, decide (the gate), parseWebhook, playbook, prompts
telemetry/   request-scoped tracing: Tracer + TraceSink port (requestId threaded through every step)
ports/       interfaces: LlmPort, GuestyPort, Store
adapters/    implementations: llm/{mock,openai}, guesty/mockGuesty, store/memoryStore, trace/fileTraceSink, log/logger
```

### Key decisions

- **The auto-send decision is pure, hard-coded rules — never the LLM** (`domain/decide.ts`). Auto-send
  only if: toggle on **and** `!risk_flag` **and** `confidence ≥ 0.65` **and** the primary code is in the
  low-risk set `{G1,G2,Y1,Y3,Y4,Y6,Y7}`. Anything else (incl. `Y2,Y5,R1,R2`) escalates, with a reason
  naming the failing condition. This is the most safety-critical code, so it's the most tested.
- **Two LLM steps, two techniques:** classification uses **structured output** (Zod-validated, not an
  agent loop); reply generation uses **tool-calling** (`get_listing`, `check_availability`) so the reply
  is grounded in real facts. If a required fact is missing the generator throws → the pipeline escalates
  rather than fabricating (per the brief's "escalate instead of faking it").
- **Fast-ack + async:** the webhook verifies, dedupes, and returns `200` in milliseconds, then processes
  via `setImmediate` so Guesty/Svix retries are safe.
- **Idempotency** on both `message.postId` (message-level) and `svix-id` (delivery-level).
- **Mock-first:** the whole slice runs offline and deterministically; swapping in real OpenAI is just a
  key. Same for Guesty (the `httpGuesty` real client is a documented TODO).

### Extras built (beyond the core loop)

- **Idempotency / dedup** — retries don't double-reply.
- **Operator kill switch** — `GET/POST /admin/auto-response`, gates the auto-send rule.
- **Observability / request tracing** — a `requestId` (UUID) is minted at the webhook entry and
  threaded through dedup → debounce → the whole pipeline, so every log line shares it (returned in the
  `200` body too). Since there's no DB, each request's full lifecycle is also persisted as a JSON file
  in `traces/` (`TRACE_DIR`): the raw inbound payload (what the agent received) plus every step's
  output — `webhook_received → parsed → classified → decided → reply_generated → auto_reply_posted` (or
  the escalate/ignored variants) — timestamps, and the final outcome. Inspect a run with
  `cat traces/*.json`. The `TraceSink` port means swapping files for a traces table / OTel later
  touches nothing upstream. Plus `GET /admin/notes` to see everything the bot posted.
- **Burst debounce** (`DEBOUNCE_MS`) — coalesces a guest's rapid-fire messages into one classified turn.
- **LLM evals** (`test/evals/`) — a small labeled dataset scored against the §6 ground truth:
  classification precision/recall and C.L.O.S.E.R. reply quality. Key-gated, so the offline suite stays
  deterministic; run them to catch prompt/model regressions.

## What I'd build next

- **Real Guesty + OpenAI HTTP clients** — `adapters/guesty/httpGuesty.ts` (OAuth, `~2 req/s` rate-limit
  with retry + jitter on 429/5xx). The port already exists; only the adapter body is stubbed.
- **Durable state** — `memoryStore` is process-local; production needs Redis (seen-set with TTL) + a
  flag service for the toggle so dedup/toggle survive restarts and work across replicas. Debounce
  likewise needs a shared delayed queue keyed by conversation.
- **Real signature E2E** — currently `SKIP_SIGNATURE=true` for the fixtures; verify against live Svix.
- **Listing id resolution** — the webhook contract often omits it; the mock returns a fixed listing.
  Resolve via the reservation/conversation in the real Guesty client.
- **Team notifications** on escalation (Slack), and a **replay** endpoint for stored raw payloads.
- **Timezone bucketing** (`America/Bogota`) for analytics dashboards.

## Where AI was used, and how I verified it

This was built with Claude Code. After locking the type/port contracts by hand (the seams everything
depends on), I fanned the three independent buckets — pure domain, adapters, transport — out to
parallel sub-agents, each pinned to exact function signatures, then wrote the integration layer
(`processMessage`, `app`, `index`) myself.

I verified the AI output rather than trusting it:
- **43 offline tests**, including a full truth table for the decision gate, webhook-parsing edge cases
  (missing reservation, empty body, host-already-replied, sender mapping), the webhook transport
  (401/400/duplicate/accepted + dedup-key recording) and real Svix signature verify/tamper checks.
- **5 LLM evals** (key-gated) that score the real classifier and C.L.O.S.E.R. generator against the
  §6 ground truth — so prompt/model changes are measured, not eyeballed.
- **End-to-end runs** against a live local server: confirmed the fast-200 ack, the C.L.O.S.E.R. note,
  duplicate suppression, toggle-off escalation, and that an `R1` haggle message correctly beats a
  co-occurring `Y1` parking signal via the priority rule (`RED > … > GREEN > GRAY`).
- **`tsc --noEmit`** clean under `strict` + `noUncheckedIndexedAccess`.

One thing I caught reviewing the mock classifier: "book … this weekend" matches both `G1` (book) and
`Y6` (weekend); raw priority ranks Y6 above GREEN, so the mock needed an explicit rule to treat a
booking verb as the dominant signal. That's a mock-classifier heuristic only — the real LLM classifier
follows the documented priority chain directly.
