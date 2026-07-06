# Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
# Use of this source code is governed by the MIT License.

.PHONY: all build test clean install run lint fmt vet docker

# Variables
BINARY_NAME=nexusguard
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)"

# Default target
all: build

# Build the binary
build:
	@echo "🔨 Building NexusGuard AI..."
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/nexusguard
	@echo "✅ Build complete: ./$(BINARY_NAME)"

# Run tests
test:
	@echo "🧪 Running tests..."
	go test -v -race -coverprofile=coverage.out ./...
	@echo "📊 Coverage report:"
	go tool cover -func=coverage.out

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -f $(BINARY_NAME)
	rm -f coverage.out
	rm -rf dist/

# Install to GOPATH/bin
install:
	@echo "📦 Installing NexusGuard AI..."
	go install $(LDFLAGS) ./cmd/nexusguard
	@echo "✅ Installed to $$(go env GOPATH)/bin/$(BINARY_NAME)"

# Run the application
run: build
	./$(BINARY_NAME)

# Run in development mode
dev:
	go run ./cmd/nexusguard --port 8080

# Run linting
lint:
	@echo "🔍 Running linter..."
	golangci-lint run ./...

# Format code
fmt:
	@echo "🎨 Formatting code..."
	go fmt ./...

# Vet code
vet:
	@echo "🔎 Vetting code..."
	go vet ./...

# Download dependencies
deps:
	@echo "📥 Downloading dependencies..."
	go mod tidy
	go mod download

# Build for multiple platforms
build-all:
	@echo "🔨 Building for all platforms..."
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/nexusguard
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64 ./cmd/nexusguard
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/nexusguard
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/nexusguard
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/nexusguard
	@echo "✅ All builds complete in dist/"

# Docker build
docker:
	@echo "🐳 Building Docker image..."
	docker build -t nexusguard-ai:$(VERSION) .

# Release with goreleaser
release:
	@echo "🚀 Creating release..."
	goreleaser release --clean

# Quick release (snapshot)
snapshot:
	@echo "📸 Creating snapshot..."
	goreleaser release --snapshot --clean

# Generate coverage report in HTML
coverage-html: test
	go tool cover -html=coverage.out -o coverage.html
	@echo "📊 Coverage report generated: coverage.html"

# Print version info
version:
	@echo "Version:    $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"

# Help
help:
	@echo "NexusGuard AI - Makefile Commands"
	@echo ""
	@echo "  make build        - Build the binary"
	@echo "  make test         - Run tests with coverage"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make install      - Install to GOPATH/bin"
	@echo "  make run          - Build and run"
	@echo "  make dev          - Run in development mode"
	@echo "  make lint         - Run linter"
	@echo "  make fmt          - Format code"
	@echo "  make build-all    - Build for all platforms"
	@echo "  make docker       - Build Docker image"
	@echo "  make release      - Create release with goreleaser"
	@echo "  make version      - Show version info"
	@echo "  make help         - Show this help"
