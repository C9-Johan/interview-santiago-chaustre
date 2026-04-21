.PHONY: fmt lint vet test test-integration build run mock-up demo check \
	stack-up stack-down stack-logs stack-status eval eval-multi \
	dev-up dev-down dev-logs dev-status

# COMPOSE defaults to `podman compose` (rootless-friendly). Override to your
# preferred binary: make stack-up COMPOSE="docker compose"
COMPOSE ?= podman compose
STACK_FILE := compose/stack.yml
DEV_FILE   := compose/dev.yml

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
run: build
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
