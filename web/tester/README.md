# InquiryIQ tester

Tiny browser UI + signing proxy for driving a running InquiryIQ service.
Lets you play guest-on-the-left, watch escalations-on-the-right, all in a
single page. It's a dev tool; do not run it in production.

## What it does

Two panes side by side:

- **Guest chat (left)** — type a message, hit send. The proxy wraps the
  text in a Guesty `reservation.messageReceived` envelope, signs it with the
  configured `GUESTY_WEBHOOK_SECRET`, and POSTs it to the real service. Once
  the service generates an auto-reply, the bot's C.L.O.S.E.R. response
  appears as a bubble on the same thread. When the service escalates
  instead, a dashed "Escalated" bubble shows the reason inline.
- **Escalation inbox (right)** — polls `/escalations` on the service and
  renders every record as a card: reason, primary code, detail, guest
  conversation key, timestamp. This is roughly what a human reviewer would
  see in a production Slack/web queue.

Preset buttons at the bottom of the chat cover the common Traffic Light
codes (availability, check-in timing, parking, discount, pets, low-signal)
so you can reproduce each classification path without remembering the
trigger phrases.

## Why a proxy instead of a pure browser UI

Two reasons:

1. **Signing.** The webhook needs a Svix HMAC-SHA256 over the raw body
   with the production webhook secret. Keeping the secret in the browser
   would be fine in a demo but would bake a bad habit; the proxy holds the
   secret and signs on the server side.
2. **Intercepting the bot reply.** The service calls
   `POST /communication/conversations/:id/send-message` on Guesty when it
   auto-sends. Normally that would go to Mockoon and the reply would
   vanish into a log. The tester sits on the service's `GUESTY_BASE_URL`,
   captures the send-message body so the chat can render it, and forwards
   every other Guesty path (`/listings/...`, `/availability-pricing/...`,
   `/communication/conversations/...`) to the underlying Mockoon.

The proxy is stateless across restarts — the per-conversation log is
in-memory. That's fine: it's a play tool, not a record.

## Stack

- Static HTML + CSS + a single JS module (arrow-js via ESM CDN, ~3KB).
  Arrow-js is a tiny reactive library; `reactive(state)` wraps an object,
  ``html`...` `` templates track reads and patch the DOM. No bundler, no
  framework.
- Go HTTP server for the signing proxy + static file server + Mockoon
  reverse proxy. Single binary, single `main.go`.

## Run it

Easiest: use the dev compose from the repo root:

```sh
export LLM_API_KEY=sk-xxx
make dev-up
# open http://localhost:4000
```

Standalone (service + Mockoon already running separately):

```sh
cd web/tester
GUESTY_WEBHOOK_SECRET=whsec_demo \
INQUIRYIQ_URL=http://localhost:8080 \
MOCKOON_URL=http://localhost:3001 \
go run .
```

## Env

| Variable | Default | Meaning |
|---|---|---|
| `TESTER_LISTEN` | `:4000` | HTTP bind address |
| `INQUIRYIQ_URL` | `http://localhost:8080` | Service base URL |
| `MOCKOON_URL` | `http://localhost:3001` | Fallback Guesty mock |
| `GUESTY_WEBHOOK_SECRET` | `whsec_demo` | HMAC secret — must match the service's |
| `TESTER_STATIC` | `./static` | Path to the UI bundle |

## Endpoints

UI-facing:

- `GET  /` — chat UI
- `POST /api/send` — accept a guest message, sign, forward to the service
- `GET  /api/escalations` — proxy to service `/escalations`
- `GET  /api/conversations/{id}` — per-conversation log (guest + bot entries)

Service-facing (shaped to look like Guesty):

- `POST /communication/conversations/{id}/send-message` — **intercepted**;
  records the bot reply so the UI can show it
- everything else with a Guesty-ish prefix → transparent proxy to Mockoon
