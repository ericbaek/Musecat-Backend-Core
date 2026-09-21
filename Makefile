.DEFAULT_GOAL := help

APP_NAME := musecat-core
BIN_DIR  := bin
BINARY   := $(BIN_DIR)/server
PORT     := 8090

.PHONY: help
help: ## Show this help message
	@echo "Musecat Backend Core - Developer Commands"
	@echo "=========================================="
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the backend server locally (default port 8090)
	go run . serve --http=0.0.0.0:$(PORT)

.PHONY: serve
serve: run ## Alias for 'run'

.PHONY: dev
dev: ## Run server with hot reload via Air
	@command -v air >/dev/null 2>&1 || (echo "Air is not installed. Install with: go install github.com/air-verse/air@latest" && exit 1)
	air

.PHONY: build
build: ## Compile application binary to bin/server
	@mkdir -p $(BIN_DIR)
	go build -v -o $(BINARY) .

.PHONY: test
test: ## Run all tests with verbose output
	go test -v ./...

.PHONY: test-quick
test-quick: ## Run all tests with caching enabled
	go test ./...

.PHONY: fmt
fmt: ## Format Go source code with gofmt
	gofmt -w -s .

.PHONY: vet
vet: ## Run static analysis with go vet
	go vet ./...

.PHONY: lint
lint: ## Run linter (golangci-lint if installed, fallback to go vet and gofmt)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found; running go vet and gofmt check..."; \
		set -e; \
		go vet ./...; \
		diff=$$(gofmt -l .); \
		if [ -n "$$diff" ]; then \
			echo "Unformatted files found:"; \
			echo "$$diff"; \
			exit 1; \
		fi; \
		echo "All checks passed!"; \
	fi

.PHONY: clean
clean: ## Remove build artifacts and temporary files
	rm -rf $(BIN_DIR) tmp
