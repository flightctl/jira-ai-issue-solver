# Makefile for jira-ai-issue-solver container operations

# Detect container runtime (podman preferred, fallback to docker)
CONTAINER_RUNTIME := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: build clean run stop logs help debug debug-tests fmt lint tidy unit-test

# Default target
help:
	@echo "Available targets:"
	@echo "  build       - Build the container image"
	@echo "  unit-test   - Run unit tests with race detector"
	@echo "  fmt         - Auto-format code (gofmt, gci)"
	@echo "  lint        - Run golangci-lint"
	@echo "  tidy        - Run go mod tidy"
	@echo "  run         - Run the container"
	@echo "  stop        - Stop the container"
	@echo "  logs        - Show container logs"
	@echo "  clean       - Clean up containers and images"
	@echo "  debug       - Debug the application with Delve"
	@echo "  debug-tests - Debug tests"
	@echo "  help        - Show this help message"

# Build the container
build:
	@echo "Building jira-ai-issue-solver container..."
	./build.sh

# Run the container
run:
	@echo "Starting jira-ai-issue-solver container..."
	$(CONTAINER_RUNTIME) run -d \
		--name jira-ai-solver \
		-p 8080:8080 \
		-v ./config.yaml:/app/config.yaml:ro \
		jira-ai-issue-solver:latest

# Stop the container
stop:
	@echo "Stopping jira-ai-issue-solver container..."
	$(CONTAINER_RUNTIME) stop jira-ai-solver || true
	$(CONTAINER_RUNTIME) rm jira-ai-solver || true

# Show container logs
logs:
	@echo "Container logs:"
	$(CONTAINER_RUNTIME) logs jira-ai-solver

# Clean up containers and images
clean:
	@echo "Cleaning up containers and images..."
	$(CONTAINER_RUNTIME) rm -f jira-ai-solver jira-ai-test 2>/dev/null || true
	$(CONTAINER_RUNTIME) rmi jira-ai-issue-solver:latest 2>/dev/null || true
	rm -f test-config.yaml

# Run with compose
compose-up:
	@echo "Starting with Podman Compose..."
	podman-compose -f podman-compose.yml up -d

# Stop compose
compose-down:
	@echo "Stopping Podman Compose..."
	podman-compose -f podman-compose.yml down

# Show compose logs
compose-logs:
	@echo "Podman Compose logs:"
	podman-compose -f podman-compose.yml logs

# Debug the application with Delve
debug:
	@echo "Starting debug session with Delve..."
	$(HOME)/go/bin/dlv debug main.go -- -config config.yaml



# Debug tests
debug-tests:
	@echo "Starting debug session for tests..."
	$(HOME)/go/bin/dlv test ./... -- -v

# Run unit tests with race detector
unit-test:
	@echo "Running unit tests with race detector..."
	go test -v -race ./...

# Auto-format code (import ordering and gofmt)
fmt:
	@echo "Formatting code..."
	gofmt -w .
	gci write --section standard --section default --section "prefix(jira-ai-issue-solver)" .

# Run golangci-lint
lint:
	@echo "Running golangci-lint..."
	golangci-lint run ./...

# Run go mod tidy
tidy:
	@echo "Running go mod tidy..."
	go mod tidy
