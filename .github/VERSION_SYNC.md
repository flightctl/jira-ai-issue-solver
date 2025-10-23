# Tool Version Synchronization

## Overview

This project maintains tool version consistency across local testing, CI workflows, and containerized builds through a single source of truth: `.github/tool-versions`.

## Single Source of Truth

**File**: `.github/tool-versions`

This file uses a simple `KEY=VALUE` format that is compatible with both:
- Bash scripts (via `source`)
- Makefiles (via `include`)

The file has no extension to keep it tool-agnostic (not tied to shell or Make specifically).

## How It Works

### 1. Local Testing (`.github/test-ci-locally.sh`)
The local CI test script sources `tool-versions` to get the exact tool versions:
```bash
source "${SCRIPT_DIR}/tool-versions"
readonly GO_VERSION="${GO_PATCH_VERSION}"
```

### 2. Makefile (`Makefile`)
The Makefile includes `tool-versions` and passes versions as build args:
```makefile
include .github/tool-versions

lint-image:
    $(CONTAINER_RUNTIME) build \
        --build-arg GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
        -t $(LINT_IMAGE) .
```

### 3. Container Builds (`Containerfile.lint`)
Accepts version as a build arg from the Makefile:
```dockerfile
ARG GOLANGCI_LINT_VERSION=v1.62.2
FROM docker.io/golangci/golangci-lint:${GOLANGCI_LINT_VERSION}
```

### 4. GitHub Actions (`.github/workflows/*.yml`)
Workflows validate that their hardcoded versions match `tool-versions`:
```yaml
- name: Verify Go version matches tool-versions
  run: |
    source .github/tool-versions
    WORKFLOW_GO_VERSION="1.23"
    if [ "$GO_VERSION" != "$WORKFLOW_GO_VERSION" ]; then
      echo "ERROR: Go version mismatch!"
      exit 1
    fi
```

## Updating Tool Versions

### Process
1. Edit `.github/tool-versions` and update the desired version(s)
2. If changing Go version, update the corresponding workflow files:
   - `.github/workflows/lint.yml` (line ~40 and ~47)
   - `.github/workflows/test.yml` (line ~40 and ~47)
3. Run tests locally: `.github/test-ci-locally.sh`
4. Commit all changes together

### Example: Updating golangci-lint

```bash
# 1. Edit .github/tool-versions
# Change: GOLANGCI_LINT_VERSION=v1.62.2
# To:     GOLANGCI_LINT_VERSION=v1.63.0

# 2. Test locally
.github/test-ci-locally.sh

# 3. Commit
git add .github/tool-versions
git commit -m "Update golangci-lint to v1.63.0"
```

### Example: Updating Go version

```bash
# 1. Edit .github/tool-versions
# Change: GO_VERSION=1.23
#         GO_PATCH_VERSION=1.23.12
# To:     GO_VERSION=1.24
#         GO_PATCH_VERSION=1.24.0

# 2. Update workflow files
# Edit .github/workflows/lint.yml:
#   - Line ~40: go-version: '1.24'
#   - Line ~47: WORKFLOW_GO_VERSION="1.24"
# Edit .github/workflows/test.yml:
#   - Line ~40: go-version: '1.24'
#   - Line ~47: WORKFLOW_GO_VERSION="1.24"

# 3. Test locally
.github/test-ci-locally.sh

# 4. Commit
git add .github/tool-versions .github/workflows/*.yml
git commit -m "Update Go to 1.24"
```

## Verification

### Verify Makefile Integration
```bash
make -n lint-image
# Should show the correct GOLANGCI_LINT_VERSION in build args
```

### Verify Bash Script Integration
```bash
source .github/tool-versions
echo "GO_VERSION=${GO_VERSION}"
```

### Verify GitHub Actions
The CI workflows will automatically fail if the versions in the workflow files don't match `tool-versions`.

## Rationale

This approach provides:
1. **Single source of truth** - All versions defined in one place
2. **Automatic propagation** - Local tools and containers use the same versions
3. **Validation** - GitHub Actions verify they match the source of truth
4. **Simplicity** - Uses standard tools (source, include) without custom scripts
5. **Type safety** - GitHub Actions will fail if versions drift

## Files Involved

- `.github/tool-versions` - Single source of truth (edit this)
- `.github/test-ci-locally.sh` - Local testing script (sources tool-versions)
- `Makefile` - Build automation (includes tool-versions)
- `Containerfile.lint` - Lint container (receives version via build arg)
- `.github/workflows/lint.yml` - Lint workflow (validates against tool-versions)
- `.github/workflows/test.yml` - Test workflow (validates against tool-versions)
