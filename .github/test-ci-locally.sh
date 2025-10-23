#!/usr/bin/env bash
# Script to test CI locally using the EXACT same environment as GitHub Actions
# This matches ubuntu-latest + actions/setup-go

set -euo pipefail

# Source versions from single source of truth
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=.github/tool-versions
source "${SCRIPT_DIR}/tool-versions"

# Use exact patch version for local testing
readonly GO_VERSION="${GO_PATCH_VERSION}"
readonly GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION}"
readonly UBUNTU_VERSION="${UBUNTU_VERSION}"

# Detect container runtime
if command -v podman &> /dev/null; then
    CONTAINER_RUNTIME="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_RUNTIME="docker"
else
    echo "ERROR: Neither podman nor docker found. Please install one of them." >&2
    exit 1
fi

echo "Testing CI in EXACT GitHub Actions environment (ubuntu-latest + setup-go)..."
echo "Using container runtime: ${CONTAINER_RUNTIME}"
echo

# Run in a container that exactly matches the CI environment
"${CONTAINER_RUNTIME}" run --rm \
  -v "$(pwd):/workspace:z" \
  -w /workspace \
  -e "GO_VERSION=${GO_VERSION}" \
  -e "GOLANGCI_LINT_VERSION=${GOLANGCI_LINT_VERSION}" \
  "ubuntu:${UBUNTU_VERSION}" \
  bash -c '
    set -euo pipefail

    echo "=== Installing prerequisites ==="
    apt-get update -qq 2>&1 | grep -v "^Get:" || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl git ca-certificates build-essential 2>&1 | grep -v "^Selecting\|^Preparing\|^Unpacking\|^Setting up" || true
    echo

    echo "=== Installing Go ${GO_VERSION} (same method as actions/setup-go) ==="
    # Download Go binary (same as setup-go does)
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o go.tar.gz
    tar -C /usr/local -xzf go.tar.gz
    rm go.tar.gz
    export PATH="/usr/local/go/bin:${PATH}"
    export GOPATH="/root/go"
    export PATH="${GOPATH}/bin:${PATH}"
    go version
    echo

    echo "=== Environment Info ==="
    uname -a
    echo

    echo "=== Configuring git for tests ==="
    git config --global user.name "CI Test"
    git config --global user.email "ci@example.com"
    echo

    echo "=== Installing golangci-lint ${GOLANGCI_LINT_VERSION} ==="
    # Download and verify golangci-lint installer
    curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh -o install-golangci-lint.sh
    sh install-golangci-lint.sh -b "${GOPATH}/bin" "${GOLANGCI_LINT_VERSION}"
    rm install-golangci-lint.sh
    echo

    echo "=== Running go mod download ==="
    go mod download
    echo

    echo "=== Checking formatting (gofmt) ==="
    UNFORMATTED=$(gofmt -l .)
    if [ -n "${UNFORMATTED}" ]; then
      echo "ERROR: The following files are not formatted:" >&2
      echo "${UNFORMATTED}" >&2
      exit 1
    fi
    echo "✓ All files properly formatted"
    echo

    echo "=== Running go vet ==="
    go vet ./...
    echo "✓ go vet passed"
    echo

    echo "=== Running golangci-lint ==="
    golangci-lint run ./...
    echo "✓ golangci-lint passed"
    echo

    echo "=== Checking go mod tidy ==="
    cp go.mod go.mod.backup
    cp go.sum go.sum.backup
    go mod tidy
    if ! diff -q go.mod go.mod.backup > /dev/null 2>&1 || ! diff -q go.sum go.sum.backup > /dev/null 2>&1; then
      echo "ERROR: go mod tidy produced changes" >&2
      diff -u go.mod.backup go.mod || true
      diff -u go.sum.backup go.sum || true
      rm -f go.mod.backup go.sum.backup
      exit 1
    fi
    rm -f go.mod.backup go.sum.backup
    echo "✓ go.mod and go.sum are tidy"
    echo

    echo "=== Running tests with race detector ==="
    go test -v -race ./...
    echo "✓ Tests passed"
    echo

    echo "=== Building ==="
    go build -o /dev/null .
    echo "✓ Build successful"
    echo

    echo "========================================="
    echo "All CI checks passed! ✓"
    echo "========================================="
  '
