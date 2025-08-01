# OpenStack Reporter Makefile

.PHONY: build run test clean install-deps dev docker-build docker-run help

# Variables
BINARY_NAME=openstack-reporter
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")
GIT_COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS=-ldflags "-X openstack-reporter/internal/version.Version=$(VERSION) -X openstack-reporter/internal/version.GitCommit=$(GIT_COMMIT) -X openstack-reporter/internal/version.BuildTime=$(BUILD_TIME)"

# Default target
help: ## Show this help message
	@echo "OpenStack Reporter - Build automation"
	@echo ""
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install-deps: ## Install Go dependencies
	@echo "Installing dependencies..."
	go mod download
	go mod verify
	go mod tidy

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_NAME) main.go

build-linux: ## Build for Linux (multiple architectures)
	@echo "Building $(BINARY_NAME) for Linux amd64..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 main.go
	@echo "Building $(BINARY_NAME) for Linux arm64..."
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-arm64 main.go

build-macos: ## Build for macOS (multiple architectures)
	@echo "Building $(BINARY_NAME) for macOS amd64..."
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 main.go
	@echo "Building $(BINARY_NAME) for macOS arm64..."
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 main.go

build-all: build build-linux build-macos ## Build for all platforms

run: ## Run the application
	@echo "Running $(BINARY_NAME)..."
	@OS=$$(uname); ARCH=$$(uname -m); \
	if [ "$$OS" = "Darwin" ]; then \
		if [ "$$ARCH" = "arm64" ]; then \
			./bin/$(BINARY_NAME)-darwin-arm64; \
		elif [ "$$ARCH" = "x86_64" ]; then \
			./bin/$(BINARY_NAME)-darwin-amd64; \
		else \
			echo "Unsupported architecture: $$ARCH on Darwin"; \
			exit 1; \
		fi \
	elif [ "$$OS" = "Linux" ]; then \
		if [ "$$ARCH" = "aarch64" ] || [ "$$ARCH" = "arm64" ]; then \
			./bin/$(BINARY_NAME)-linux-arm64; \
		elif [ "$$ARCH" = "x86_64" ]; then \
			./bin/$(BINARY_NAME)-linux-amd64; \
		else \
			echo "Unsupported architecture: $$ARCH on Linux"; \
			exit 1; \
		fi \
	else \
		echo "Unsupported OS: $$OS"; \
		exit 1; \
	fi

dev: ## Run in development mode with auto-reload
	@echo "Starting development server..."
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air not found. Install it with: go install github.com/cosmtrek/air@latest"; \
		go run main.go; \
	fi

test: ## Run tests
	@echo "Running tests..."
	go test -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

lint: ## Run linter
	@echo "Running linter..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install it from https://golangci-lint.run/"; \
	fi

format: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	@if command -v goimports > /dev/null; then \
		goimports -w .; \
	fi

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf data/
	rm -f coverage.out coverage.html

setup-env: ## Setup environment file
	@if [ ! -f .env ]; then \
		echo "Creating .env file from example..."; \
		cp .env.example .env; \
		echo "Please edit .env file with your OpenStack credentials"; \
	else \
		echo ".env file already exists"; \
	fi

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	@export VERSION=$(VERSION) && \
	export GIT_COMMIT=$(GIT_COMMIT) && \
	export BUILD_TIME=$(BUILD_TIME) && \
	docker builder prune -f
	docker build --no-cache --build-arg VERSION=$$VERSION --build-arg GIT_COMMIT=$$GIT_COMMIT --build-arg BUILD_TIME=$$BUILD_TIME -t $(BINARY_NAME):$(VERSION) .
	docker tag $(BINARY_NAME):$(VERSION) $(BINARY_NAME):latest

# Build docker-compose image with version info

docker-compose-build: ## Build Docker image via docker-compose with version info
	@echo "Building Docker image via docker-compose..."
	@export VERSION=$(VERSION) && \
	export GIT_COMMIT=$(GIT_COMMIT) && \
	export BUILD_TIME=$(BUILD_TIME) && \
	docker-compose build --no-cache

docker-run: ## Run Docker container
	@echo "Running Docker container..."
	docker run -p 8080:8080 --env-file .env $(BINARY_NAME):latest

docker-compose-up: ## Start with docker-compose
	@echo "Starting with docker-compose..."
	docker-compose up -d

docker-compose-down: ## Stop docker-compose
	@echo "Stopping docker-compose..."
	docker-compose down

install: build ## Install binary to system
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp bin/$(BINARY_NAME) /usr/local/bin/

uninstall: ## Uninstall binary from system
	@echo "Uninstalling $(BINARY_NAME)..."
	sudo rm -f /usr/local/bin/$(BINARY_NAME)

release: clean build-all ## Prepare release builds
	@echo "Creating release package..."
	mkdir -p release
	cp bin/$(BINARY_NAME) release/
	cp bin/$(BINARY_NAME)-linux release/
	cp bin/$(BINARY_NAME)-macos release/
	cp .env.example release/
	cp README.md release/
	tar -czf release/$(BINARY_NAME)-$(VERSION).tar.gz -C release .
	@echo "Release package created: release/$(BINARY_NAME)-$(VERSION).tar.gz"

# Development helpers
deps-update: ## Update dependencies
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

security-check: ## Run security check
	@echo "Running security check..."
	@if command -v gosec > /dev/null; then \
		gosec ./...; \
	else \
		echo "gosec not found. Install it with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
	fi
