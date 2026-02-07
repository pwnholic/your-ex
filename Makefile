.PHONY: help build test clean docker-build docker-run install fmt vet lint

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the sniper binary
	@echo "Building sniper..."
	@go build -o sniper ./cmd/sniper
	@echo "Build complete: ./sniper"

test: ## Run all tests
	@echo "Running tests..."
	@go test -v ./...

test-short: ## Run short tests only
	@echo "Running short tests..."
	@go test -short -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -f sniper
	@rm -f coverage.out coverage.html
	@echo "Clean complete"

fmt: ## Format code
	@echo "Formatting code..."
	@go fmt ./...
	@echo "Format complete"

vet: ## Run go vet
	@echo "Running go vet..."
	@go vet ./...
	@echo "Vet complete"

lint: ## Run golangci-lint
	@echo "Running golangci-lint..."
	@golangci-lint run ./...
	@echo "Lint complete"

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	@docker build -t sniper-bot:latest .
	@echo "Docker build complete"

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	@docker run -d --name sniper-bot \
		-v $(PWD)/config.example.yaml:/root/.sniper/config.yaml \
		-p 9090:9090 \
		sniper-bot:latest

docker-stop: ## Stop Docker container
	@echo "Stopping Docker container..."
	@docker stop sniper-bot || true
	@docker rm sniper-bot || true

install: ## Install binary to /usr/local/bin
	@echo "Installing sniper..."
	@go install ./cmd/sniper
	@echo "Install complete"

run: ## Run the bot locally
	@echo "Running sniper..."
	@go run ./cmd/sniper start --config config.example.yaml --mode monitor

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@go mod download
	@echo "Dependencies downloaded"

tidy: ## Tidy go modules
	@echo "Tidying modules..."
	@go mod tidy
	@echo "Tidy complete"

version: ## Show version info
	@echo "Sniper Bot v1.0.0"
	@echo "Go version: $$(go version)"

all: fmt vet build ## Run fmt, vet, and build
