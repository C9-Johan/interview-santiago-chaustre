# CLAUDE.md

Guidance for Claude Code when working in this repo.

## Security rules (must follow)

- **NEVER read, open, print, or otherwise access `.env`** (or any `.env.*` file other than
  `.env.example`). It contains real secrets (e.g. `OPENAI_API_KEY`, `GUESTY_WEBHOOK_SECRET`). Do not
  `cat`, `Read`, `grep`, or echo it, and never include its contents in output, logs, or commits.
  If you need to know which variables exist, read `.env.example` instead.
- Never commit secrets. `.env` is gitignored — keep it that way.

## Project

InquiryIQ — a Guesty webhook → AI reply service. Ingests `reservation.messageReceived`, classifies
the guest message (traffic-light taxonomy), generates a C.L.O.S.E.R. reply via tool-calling, applies a
hard-rules auto-send gate, and posts the result back to Guesty as an internal note. See `README.md`
for full architecture and `CHALLENGE.md` / `GUESTY_WEBHOOK_CONTRACT.md` for the spec.

## Architecture

Lightweight ports & adapters: `domain/` (pure, tested) · `ports/` (interfaces) · `adapters/`
(mock + real impls) · `transport/` (thin HTTP) · `pipeline/` (orchestration). `src/app.ts` is the only
composition root that knows about concrete implementations.

The auto-send decision (`src/domain/decide.ts`) is **pure hard-coded rules, never the LLM** — treat it
as safety-critical and keep it fully unit-tested.

## Commands

- `npm test` — run the vitest suite (keep it green).
- `npm run dev` — start the dev server (loads `.env`; mock adapters when no `OPENAI_API_KEY`).
- `npx tsc --noEmit` — typecheck (`strict` + `noUncheckedIndexedAccess`); must stay clean.

## Conventions

- ESM + TypeScript; relative imports use the `.js` extension.
- Comments explain **why**, not what.
- Adapters implement ports — don't let domain or transport import concrete adapters directly.
