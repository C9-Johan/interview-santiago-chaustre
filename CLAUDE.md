# InquiryIQ — Project Instructions

90-minute vertical slice for the Cloud9 interview. Go service that receives a Guesty
webhook, classifies the guest message with an LLM, generates a C.L.O.S.E.R. reply, and
either auto-sends it as a Guesty internal note or escalates to a human.

Brief: @CHALLENGE.md · Webhook contract: @GUESTY_WEBHOOK_CONTRACT.md

## The rule

**Follow @coding-conventions.md exactly.** Guard clauses over `else`, consumer-declared
unexported interfaces, generics over `any`/`reflect`, mappers at layer boundaries, GoDoc
on exported identifiers, Sonar-style gate (funlen 100/50, cyclop 30, gocognit 20,
nestif 5, dupl 150). No `//nolint`, no suppressions.

## Layered architecture

Four layers, one-way dependency (outer → inner). Domain knows nothing about transport or
infra; infra imports domain but never the reverse.

```
cmd/                        # main.go — wire dependencies, start HTTP server
internal/
  transport/http/           # handlers, router, Svix signature verification, DTOs
  application/              # use cases: ProcessInquiry, Classify, GenerateReply, Decide
  domain/                   # entities, value objects, taxonomy codes, sentinel errors
    repository/             # EXPORTED interfaces (Guesty client, LLM client, store)
    mappers/                # pure funcs at layer boundaries (webhook→domain, domain→Guesty)
  infrastructure/
    guesty/                 # HTTP client implementing domain/repository.GuestyClient
    llm/                    # OpenAI-compatible client (DeepSeek via configurable base_url)
    store/                  # idempotency + message log (memory first, interface-backed)
```

Handlers acknowledge the webhook fast (202) and hand off to an async worker. The
auto-send gate is a **rules check in the application layer**, never an LLM decision
(see §6 of CHALLENGE.md).

## Non-negotiables from memory

- **LLM client**: OpenAI-compatible SDK with configurable `base_url` so DeepSeek drops in
  without code changes. Do not hard-code `api.openai.com`.
- **Third-party mocks**: stand up Mockoon (or WireMock) on a configurable base URL for
  Guesty + LLM during local dev and tests. No in-process fakes for HTTP behavior.
- **Interfaces everywhere external**: storage, cache, HTTP clients declared as small
  consumer-side interfaces with constructor injection. Redis/Postgres/alt LLM drop in
  later.

## Quick checks before claiming done

- `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run` all green
- Webhook endpoint returns 202 and dedupes on the contract's idempotency key
- Auto-send gate matches the exact rules in CHALLENGE.md §6 (code set, conf ≥ 0.65, toggle)
