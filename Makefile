# Makefile for jira-ai-issue-solver container operations

# Configuration - these can be overridden by the caller
PROJECT_ID ?= $(error PROJECT_ID is required. Usage: make deploy PROJECT_ID=your-project-id REGION=your-region SERVICE_NAME=your-service-name)
REGION ?= $(error REGION is required. Usage: make deploy PROJECT_ID=your-project-id REGION=your-region SERVICE_NAME=your-service-name)
SERVICE_NAME ?= $(error SERVICE_NAME is required. Usage: make deploy PROJECT_ID=your-project-id REGION=your-region SERVICE_NAME=your-service-name)



.PHONY: build push clean test run stop logs help debug debug-tests deploy

# Default target
help:
	@echo "Available targets:"
	@echo "  build       - Build the container image"
	@echo "  push        - Build and push the container to Google Container Registry"
	@echo "  deploy      - Deploy the container to Cloud Run (includes push)"
	@echo "  test        - Test the container"
	@echo "  run         - Run the container"
	@echo "  stop        - Stop the container"
	@echo "  logs        - Show container logs"
	@echo "  clean       - Clean up containers and images"
	@echo "  debug       - Debug the application with Delve"
	@echo "  debug-tests - Debug tests"
	@echo "  help        - Show this help message"
	@echo ""
	@echo "Deployment usage:"
	@echo "  make deploy PROJECT_ID=your-project-id REGION=your-region SERVICE_NAME=your-service-name"
	@echo "  make deploy PROJECT_ID=... REGION=... SERVICE_NAME=... ARGS=\"--skip-secrets\""

# Build the container
build:
	@echo "Building jira-ai-issue-solver container..."
	./build.sh

# Push the container to Google Container Registry
push:
	@echo "Building and pushing jira-ai-issue-solver container to GCR..."
	@echo "Project ID: $(PROJECT_ID)"
	@echo "Region: $(REGION)"
	@echo "Image: $(REGION)-docker.pkg.dev/$(PROJECT_ID)/jira-ai-issue-solver/jira-ai-issue-solver:v1"
	# Authenticate with Google Container Registry for Podman
	gcloud auth print-access-token | podman login -u oauth2accesstoken --password-stdin $(REGION)-docker.pkg.dev
	# Build the image
	podman build --platform linux/amd64 --tag jira-ai-issue-solver:latest --file Dockerfile .
	# Tag for GCR
	podman tag jira-ai-issue-solver:latest $(REGION)-docker.pkg.dev/$(PROJECT_ID)/jira-ai-issue-solver/jira-ai-issue-solver:v1
	# Push to GCR
	podman push $(REGION)-docker.pkg.dev/$(PROJECT_ID)/jira-ai-issue-solver/jira-ai-issue-solver:v1

# Deploy the container to Cloud Run
deploy: push
	@echo "Deploying jira-ai-issue-solver container to Cloud Run..."
	@echo "Project ID: $(PROJECT_ID)"
	@echo "Region: $(REGION)"
	@echo "Service Name: $(SERVICE_NAME)"
	PROJECT_ID="$(PROJECT_ID)" REGION="$(REGION)" SERVICE_NAME="$(SERVICE_NAME)" ./deploy.sh $(ARGS)

# Test the container
test:
	@echo "Testing jira-ai-issue-solver container..."
	./test-container.sh

# Run the container
run:
	@echo "Starting jira-ai-issue-solver container..."
	podman run -d \
		--name jira-ai-solver \
		-p 8080:8080 \
		-v ./config.yaml:/app/config.yaml:ro \
		jira-ai-issue-solver:latest

# Stop the container
stop:
	@echo "Stopping jira-ai-issue-solver container..."
	podman stop jira-ai-solver || true
	podman rm jira-ai-solver || true

# Show container logs
logs:
	@echo "Container logs:"
	podman logs jira-ai-solver

# Clean up containers and images
clean:
	@echo "Cleaning up containers and images..."
	podman rm -f jira-ai-solver jira-ai-test 2>/dev/null || true
	podman rmi jira-ai-issue-solver:latest 2>/dev/null || true
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