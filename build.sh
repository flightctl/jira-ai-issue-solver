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
echo "To run with environment variables:"
echo "podman run -d --name jira-ai-solver -p 8080:8080 \\"
echo "  -e JIRA_AI_JIRA_BASE_URL=https://your-domain.atlassian.net \\"
echo "  -e JIRA_AI_JIRA_USERNAME=your-username \\"
echo "  -e JIRA_AI_JIRA_API_TOKEN=your-jira-api-token \\"
echo "  -e JIRA_AI_GITHUB_PERSONAL_ACCESS_TOKEN=your-github-token \\"
echo "  -e JIRA_AI_GITHUB_BOT_USERNAME=your-bot-username \\"
echo "  -e JIRA_AI_GITHUB_BOT_EMAIL=your-bot-email \\"
echo "  -e JIRA_AI_AI_PROVIDER=claude \\"
echo "  ${FULL_IMAGE_NAME}" 