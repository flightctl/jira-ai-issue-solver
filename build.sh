#!/bin/bash

# Build script for jira-ai-issue-solver using Podman
set -e

# Configuration
IMAGE_NAME="jira-ai-issue-solver"
TAG="${1:-latest}"
FULL_IMAGE_NAME="${IMAGE_NAME}:${TAG}"

echo "Building ${FULL_IMAGE_NAME} with Podman..."

# Build the image
podman build \
    --platform linux/amd64 \
    --tag "${FULL_IMAGE_NAME}" \
    --file Dockerfile \
    .

echo "Build completed successfully!"
echo "Image: ${FULL_IMAGE_NAME}"
echo ""
echo "To run the container:"
echo "podman run -d --name jira-ai-solver -p 8080:8080 -v ./config.yaml:/app/config.yaml:ro ${FULL_IMAGE_NAME}"
echo ""
echo "To run with config file and private key mounted:"
echo "podman run -d --name jira-ai-solver -p 8080:8080 \\"
echo "  -v ./config.yaml:/app/config.yaml:ro \\"
echo "  -v ./keys/github-app.private-key.pem:/etc/bot/github-app.private-key.pem:ro \\"
echo "  -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \\"
echo "  ${FULL_IMAGE_NAME}"
echo ""
echo "See docs/testing-setup.md for detailed deployment instructions."
