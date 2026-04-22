.PHONY: fmt lint vet test test-integration build run run-prod run-bin mock-up demo check \
	stack-up stack-down stack-logs stack-status eval eval-multi \
	dev-up dev-down dev-logs dev-status \
	e2e e2e-wait e2e-smoke e2e-full tilt-up tilt-down \
	mise-verify mise-tools mise-env \
	env-check up up-prod down down-prod logs \
	up-headless up-prod-headless

# COMPOSE defaults to `podman compose` (rootless-friendly). Override to your
# preferred binary: make stack-up COMPOSE="docker compose"
COMPOSE ?= podman compose
STACK_FILE    := compose/stack.yml
DEV_FILE      := compose/dev.yml
PROD_OVERRIDE := compose/prod.override.yml
# Layered compose for prod-like mode — stack (real backends + observability)
# merged with dev (service + mocks + tester UI), overridden to flip the
# service's stores/OTLP at the real ones.
PROD_FILES := -f $(STACK_FILE) -f $(DEV_FILE) -f $(PROD_OVERRIDE)

fmt:
	gofumpt -l -w .
	goimports -local github.com/chaustre/inquiryiq -l -w .
lint:
	golangci-lint run
vet:
	go vet ./...
test:
	go test -race -count=1 ./...
test-integration:
	go test -tags=integration -count=1 ./tests/integration/...
build:
	go build -o ./tmp/server ./cmd/server
	go build -o ./tmp/replay ./cmd/replay
	go build -o ./tmp/eval   ./cmd/eval
# Stage-A classifier regression run. Requires LLM_API_KEY; override -set to
# point at a different labeled file. Exits non-zero on any failing case.
eval: build
	./tmp/eval -set eval/golden_set.json
# Multi-language regression: loads every JSON under eval/sets/ and prints a
# per-locale report. Use for release gates where one locale must not regress.
eval-multi: build
	./tmp/eval -dir eval/sets
# `make run` is the interactive dev loop: Tilt dashboard at :10350 with
# env pre-flight, live logs, and one-click buttons for smoke/unit/lint/eval.
# MODE=prod swaps dev-only mocks for the full Mongo + Valkey + observability
# stack (same as `make up-prod` but inside the dashboard).
run:
	tilt up
run-prod:
	MODE=prod tilt up
# Legacy no-compose runner — build + exec the bare binary. Useful if you
# want to attach a debugger; otherwise prefer `make run`.
run-bin: build
	./tmp/server
mock-up:
	mockoon-cli start -d fixtures/mockoon/guesty.json --port 3001 --log-transaction
demo: build
	AUTO_REPLAY_ON_BOOT=true ./tmp/server
check: fmt vet lint test

# --- Production-grade local stack -----------------------------------------
# Mongo + Valkey + mongo-express + RedisInsight + Alloy + Tempo + Prometheus
# + Grafana. Point the service at it:
#   STORE_BACKEND=mongo MONGO_URI=mongodb://localhost:27017
#   IDEMPOTENCY_BACKEND=redis REDIS_ADDR=localhost:6379
#   OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318
stack-up:
	$(COMPOSE) -f $(STACK_FILE) up -d
	@echo "Grafana:        http://localhost:3000  (anonymous Admin)"
	@echo "Alloy UI:       http://localhost:12345"
	@echo "Tempo API:      http://localhost:3200"
	@echo "Prometheus:     http://localhost:9090"
	@echo "Mongo Express:  http://localhost:8081"
	@echo "RedisInsight:   http://localhost:5540"
	@echo "Set OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318 on the service."
stack-down:
	$(COMPOSE) -f $(STACK_FILE) down
stack-logs:
	$(COMPOSE) -f $(STACK_FILE) logs -f
stack-status:
	$(COMPOSE) -f $(STACK_FILE) ps

# --- Developer stack (mocks + service + tester UI) -----------------------
# Everything you need to drive the service from a browser. Requires
# LLM_API_KEY in the shell; every other env has a dev default.
dev-up:
	$(COMPOSE) -f $(DEV_FILE) up -d --build
	@echo "Tester UI:       http://localhost:4000"
	@echo "Service webhook: http://localhost:8080/webhooks/guesty/message-received"
	@echo "Guesty mock:     http://localhost:3001"
dev-down:
	$(COMPOSE) -f $(DEV_FILE) down
dev-logs:
	$(COMPOSE) -f $(DEV_FILE) logs -f
dev-status:
	$(COMPOSE) -f $(DEV_FILE) ps

# --- Top-level entry points ---------------------------------------------
# Two modes, same UX:
#   make up        = mock mode    — Mockoon + service (in-memory/file stores) + tester UI
#   make up-prod   = prod-like    — adds Mongo + Valkey + Alloy/Tempo/Prom/Grafana,
#                                   flips service backends to the real ones
# Both bring the stack up, wait for /healthz, and leave it running so you
# can click around. Shut down with `make down` / `make down-prod`.
# LLM_API_KEY must be in the shell env (dev.yml fails loudly if missing).

# Fast pre-flight. Fails with a useful message if LLM_API_KEY is unset.
# Re-runs `mise run env:check` for visibility into defaults + secrets.
env-check:
	@test -n "$$LLM_API_KEY" || { \
		echo "✗ LLM_API_KEY unset."; \
		echo "  cp .env.local.example .env.local, fill in the key, then either:"; \
		echo "    - activate the mise shell hook (mise.jdx.dev/getting-started)"; \
		echo "    - or run this target via:  mise exec -- make $@"; \
		exit 1; \
	}
	@mise run env:check 2>/dev/null || true
	@echo "✓ env ok — LLM_API_KEY set"

# Mock mode: launches Tilt — dashboard at :10350 with env pre-flight, live
# logs, clickable service URLs, and one-click smoke/unit/lint/eval buttons.
# Ctrl-C exits Tilt (containers keep running; `make down` stops them).
up:
	tilt up

down:
	$(COMPOSE) -f $(DEV_FILE) down

# Prod-like mode: same Tilt dashboard, but merges stack + dev + prod.override
# so the service talks to real Mongo + Valkey and exports traces/metrics to
# Alloy → Tempo/Prom → Grafana. Tilt resource links point at each observability
# UI (Grafana, Mongo Express, RedisInsight, …). Guesty stays mocked.
up-prod:
	MODE=prod tilt up

down-prod:
	$(COMPOSE) $(PROD_FILES) down

# Headless variants — scripted compose up without Tilt, for CI or when you
# just want the stack running in the background.
up-headless: env-check
	$(COMPOSE) -f $(DEV_FILE) up -d --build
	./scripts/wait-for-health.sh
up-prod-headless: env-check
	$(COMPOSE) $(PROD_FILES) up -d --build
	./scripts/wait-for-health.sh

logs:
	$(COMPOSE) -f $(DEV_FILE) logs -f

# --- End-to-end (legacy aliases) ----------------------------------------
# `make e2e` keeps the old "bring up dev + smoke" behavior for muscle memory.
# New code should prefer `make up` / `make e2e-smoke`.
e2e: up e2e-smoke

e2e-wait:
	./scripts/wait-for-health.sh

e2e-smoke:
	./scripts/e2e-smoke.sh

# Run the full flow and clean up regardless of pass/fail — suitable for
# CI or a one-shot "did I break it" check.
e2e-full:
	$(MAKE) dev-up
	$(MAKE) e2e-wait
	-$(MAKE) e2e-smoke; rc=$$?; $(MAKE) dev-down; exit $$rc

# --- mise verification ---------------------------------------------------
# Proves the pinned toolchain installs, resolves, and runs end-to-end. Good
# smoke test after editing `.mise.toml` or onboarding a new machine.
#   make mise-verify    # install + tool versions + env check
#   make mise-tools     # just print resolved versions
#   make mise-env       # just print masked env (flags unset secrets)
mise-verify: mise-tools mise-env
	@echo ""
	@echo "✓ mise toolchain + env OK. Next:"
	@echo "    make check   # fmt + vet + lint + race-tests"
	@echo "    make e2e     # compose up + smoke"

mise-tools:
	@command -v mise >/dev/null || { echo "mise not on PATH — see https://mise.jdx.dev"; exit 1; }
	mise install
	@echo ""
	@echo "--- resolved tool versions ---"
	@mise exec -- go version
	@mise exec -- golangci-lint --version | head -1
	@mise exec -- gofumpt -version
	@mise exec -- tilt version
	@mise exec -- jq --version

mise-env:
	@echo ""
	@echo "--- resolved env (secrets masked) ---"
	mise run env:check

# --- Tilt ----------------------------------------------------------------
# Dashboard at http://localhost:10350 with per-service logs, port-forwards,
# and one-click buttons for unit/integration/e2e/lint/eval. Requires tilt
# in PATH (mise installs it).
tilt-up:
	tilt up
tilt-down:
	tilt down
