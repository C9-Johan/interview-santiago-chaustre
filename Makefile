.PHONY: fmt lint vet test test-integration build run mock-up demo check
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
