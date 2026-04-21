.PHONY: fmt lint vet test test-integration build run mock-up demo check obs-up obs-down obs-logs obs-status

# COMPOSE defaults to `podman compose` (rootless-friendly). Override to your
# preferred binary: make obs-up COMPOSE="docker compose"
COMPOSE ?= podman compose
OBS_FILE := compose/observability.yml

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
run: build
	./tmp/server
mock-up:
	mockoon-cli start -d fixtures/mockoon/guesty.json --port 3001 --log-transaction
demo: build
	AUTO_REPLAY_ON_BOOT=true ./tmp/server
check: fmt vet lint test

# --- Observability stack --------------------------------------------------
# Alloy (OTLP receiver) + Tempo (traces) + Prometheus + Grafana at :3000.
# Point the service at it: OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318
obs-up:
	$(COMPOSE) -f $(OBS_FILE) up -d
	@echo "Grafana:    http://localhost:3000  (anonymous Admin)"
	@echo "Alloy UI:   http://localhost:12345"
	@echo "Tempo API:  http://localhost:3200"
	@echo "Prometheus: http://localhost:9090"
	@echo "Set OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318 on the service."
obs-down:
	$(COMPOSE) -f $(OBS_FILE) down
obs-logs:
	$(COMPOSE) -f $(OBS_FILE) logs -f
obs-status:
	$(COMPOSE) -f $(OBS_FILE) ps
