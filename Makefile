BINARY  := infra-probe
PKG     := github.com/netseven/infra-probe
CMD     := ./cmd/infra-probe
BIN_DIR := bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary for the current platform
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

.PHONY: build-linux
build-linux: ## Cross-compile a static Linux amd64 binary (the deployment target)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD)

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run unit tests and write coverage.out
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

.PHONY: integration
integration: ## Run integration tests (needs a Docker daemon)
	INFRA_PROBE_REQUIRE_DOCKER=1 go test -tags=integration -count=1 ./test/...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -s -w .

.PHONY: vuln
vuln: ## Scan dependencies for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: tidy
tidy: ## Sync go.mod / go.sum
	go mod tidy

.PHONY: check
check: vet lint test-race ## Everything to run before a commit

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out
