# AGENTS.md

This file provides guidance to AIs when working with code in this repository.

## Project Overview

Jira AI Issue Solver is a Go-based service that automatically processes Jira tickets and creates GitHub pull requests with AI-generated solutions. It runs AI providers (Claude, Gemini) inside containers against cloned repositories, managing the full lifecycle from ticket detection through PR creation and review feedback handling.

## Architecture

### Package Structure

The application uses consumer-defined interfaces and clear package boundaries:

- **`tracker/`** — `IssueTracker` interface for work item operations; `jira/` sub-package adapts `services.JiraService` to this interface
- **`workspace/`** — `Manager` interface for ticket-scoped workspace lifecycle (clone, cleanup, TTL); `FSManager` implementation
- **`container/`** — `Manager` interface for container lifecycle; `Runner` (CLI executor), `Resolver` (image/config resolution), `RuntimeManager` (orchestration)
- **`taskfile/`** — `Writer` interface for generating AI task files; `MarkdownWriter` implementation; appends universal instructions and (for new tickets only) workflow from repo-level files or project-config fallback
- **`repoconfig/`** — Parses `.ai-bot/config.yaml` from target repositories for per-repo AI/container settings and repo imports
- **`projectresolver/`** — `Resolver` interface mapping ticket keys to project settings (component-to-repo, status transitions, imports)
- **`executor/`** — `Pipeline` implementing new-ticket and PR-feedback execution flows
- **`jobmanager/`** — `Coordinator` with concurrency control, retry tracking, and circuit breaker
- **`scanner/`** — `WorkItemScanner` (new tickets) and `FeedbackScanner` (PR review comments); stateless, event-driven
- **`commentfilter/`** — Shared bot-loop prevention (ignored users, known bots, thread depth limits)
- **`recovery/`** — `StartupRunner` for crash recovery (orphan container cleanup, stuck ticket reset, workspace TTL)
- **`costtracker/`** — `FileTracker` for daily AI session cost tracking with budget enforcement
- **`services/`** — Infrastructure implementations: `JiraService` (Jira REST API), `GitHubService` (GitHub App auth, Git Data API, PR operations)
- **`models/`** — Configuration (`Config`), Jira API types, domain types (`WorkItem`, `SearchCriteria`, `ProjectSettings`)

### Design Principles

- **Consumer-defined interfaces**: Each consumer declares only the methods it needs (no shared service interfaces)
- **Test doubles via func-field stubs**: `*test/` sub-packages (e.g., `executortest/stubs.go`) with assignable function fields
- **Deterministic output**: Map keys sorted when building strings/commands
- **Nil slice normalization**: Empty results return `[]T{}`, not nil
- **Stateless scanning**: Scanners derive "addressed" state from bot replies, no timestamp markers

### Configuration Model

Multi-project configuration system (`models/config.go`):

- **Project-based**: Each project has its own status transitions, component-to-repo mappings, and PR field settings
- **Ticket type-specific status transitions**: Different issue types (Bug, Story, Task) can have different workflow statuses
- **Workspaces**: Configurable base directory and TTL for ticket-scoped workspace cleanup
- **Container**: Runtime selection (podman/docker/auto), default image, resource limits
- **Guardrails**: Concurrency limits, retry limits, circuit breaker, daily cost budget, container timeout
- **Environment variable support**: All configuration via `JIRA_AI_<SECTION>_<FIELD>` env vars or YAML

Key configuration features:
- `GetProjectConfigForTicket()` retrieves the appropriate project config based on ticket key
- `StatusTransitions` maps ticket types to their workflow statuses (todo, in_progress, in_review)
- `ComponentToRepo` maps Jira components to GitHub repository URLs (case-insensitive; viper lowercases YAML map keys)
- `Imports` (project-level) declares auxiliary repos to clone into the workspace; merged with repo-level imports from `.ai-bot/config.yaml`; optional `install` command runs inside the container after cloning
- `Instructions` (project-level) provides universal fallback instructions (validation commands, coding standards); appended to all task types; repo-level `.ai-bot/instructions.md` takes precedence
- `NewTicketWorkflow` (project-level) provides workflow instructions appended only to new-ticket task files; repo-level `.ai-bot/new-ticket-workflow.md` takes precedence

### Workflow

1. **Ticket Discovery**: `WorkItemScanner` polls for tickets in "todo" status via the `IssueTracker` interface
2. **Job Submission**: Scanner submits jobs to `Coordinator`, which enforces concurrency, retry, and circuit breaker limits
3. **Execution Pipeline** (`executor.Pipeline`):
   - Resolves project config and maps ticket component to target repository
   - Creates/reuses a workspace (clone + branch)
   - Loads repo config (`.ai-bot/config.yaml`) and clones any declared imports into the workspace
   - Generates a task file describing the work (appends project instructions from `.ai-bot/instructions.md` or project-config fallback)
   - Resolves container image from repo-level config (`.ai-bot/container.json`, `.devcontainer/`) or global default
   - Starts a container, runs import install commands (if configured), then runs the AI provider
   - Reads AI-generated PR description (`.ai-bot/pr.md`) if present; falls back to Jira-derived content
   - Commits changes, pushes, and creates a PR
   - Transitions the ticket through configured statuses and posts PR link
4. **PR Feedback Processing**: `FeedbackScanner` monitors "in review" tickets, finds unaddressed review comments (filtering bots and ignored users), and submits feedback jobs through the same `Coordinator` → `Pipeline` path
5. **Crash Recovery**: On startup, `StartupRunner` cleans up orphan containers, resets stuck "in progress" tickets, and purges expired workspaces

### Bot-Loop Prevention

Configurable via `github.known_bot_usernames`, `github.ignored_usernames`, and `github.max_thread_depth`:
- **Known bots**: Comments are processed initially but loop prevention stops bot-to-bot reply chains
- **Ignored usernames**: Comments completely skipped (for CI bots like packit-as-a-service[bot])
- **Thread depth**: Maximum bot replies per thread (default: 5)

### Security Features

- **Security level redaction**: Tickets with security levels get redacted PR titles/descriptions
- **SSH key signing**: Optional commit signing via SSH keys (`github.ssh_key_path`)
- **Container isolation**: AI runs inside containers with configurable resource limits
- **Cost budget**: Daily AI session cost tracking with automatic pausing

## Common Development Commands

### Running the Application

```bash
# Using config file
go run main.go -config config.yaml

# Using environment variables (container mode)
export JIRA_AI_JIRA_BASE_URL=...
export JIRA_AI_JIRA_USERNAME=...
# ... (see config.example.yaml for all options)
go run main.go
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with verbose output and race detection
go test -v -race ./...

# Run tests for a specific package
go test -v ./executor

# Run a specific test
go test -v ./tracker/jira -run TestAdapter_SearchWorkItems
```

### Building

```bash
# Build the binary
go build -o jira-ai-issue-solver main.go

# Build container image
make build
```

### Linting and Formatting

```bash
# Auto-format code (gofmt + gci import ordering)
make fmt

# Run linter (golangci-lint)
make lint
```

### Debugging

```bash
# Interactive debug session
./debug.sh

# Debug with Delve directly
make debug

# Debug tests
make debug-tests

# VS Code debugging: Press F5 and select configuration
```

See docs/debugging.md for breakpoint locations and common troubleshooting.

### Container Operations

```bash
# Build and run locally
make build
make run

# View logs
make logs

# Stop and clean up
make stop
make clean
```

## Important Implementation Details

### Multi-Project Configuration

When adding new projects or modifying configuration:
- Each project requires at least one ticket type with complete status transitions (todo, in_progress, in_review)
- Project keys are matched case-insensitively via `GetProjectConfigForTicket()`
- Component names in `component_to_repo` are matched case-insensitively (viper lowercases YAML map keys)
- Status names are case-sensitive and must exactly match the Jira workflow status names

### PR URL Handling

Two modes for storing PR URLs:
1. **Designated field mode**: If `git_pull_request_field_name` is configured, updates that custom Jira field
2. **Comment mode**: If no designated field, adds structured comment `[AI-BOT-PR] <url>` for easy extraction

### Error Handling

Error handling can be configured per project:
- `disable_error_comments`: If true, errors are logged but not posted to Jira
- Failed ticket processing reverts to original status (no status change on error)
- All errors logged with structured logging (zap) including ticket key and context

## Code Patterns

### Service Initialization

Services are initialized in `main.go` with dependency injection. The startup sequence:
1. Load config and create logger
2. Create infrastructure services (JiraService, GitHubService)
3. Create adapters (IssueTracker, ProjectResolver, WorkspaceManager, ContainerManager)
4. Create cost tracker
5. Create executor pipeline
6. Create job coordinator
7. Run crash recovery
8. Start scanners and HTTP server

### Configuration Validation

All configuration is validated in `models.Config.validate()`:
- Required fields checked (Jira credentials, GitHub App auth, bot username, workspace base dir)
- Status transitions validated per ticket type
- At least one project must be configured
- Each project must have at least one ticket type and component mapping

### Logging

Use structured logging with zap throughout:
```go
logger.Info("Processing ticket", zap.String("ticket", ticketKey))
logger.Error("Failed to process", zap.String("ticket", ticketKey), zap.Error(err))
```

Log levels: debug, info, warn, error (configured via `logging.level`)

### Test Requirements

**Every code change must include corresponding unit tests.** Code and tests are always committed together — never defer test writing to a later step.

When making changes:
- **New functions/methods**: Add tests covering the happy path, error cases, and edge cases
- **Changed behavior**: Update existing tests to cover the new behavior; add new tests for new code paths
- **Interface changes**: Update all stubs/mocks and any tests that use the old signature
- **Bug fixes**: Add a test that reproduces the bug first, then verify it passes with the fix

Run `go test -v -race ./...` after every change and fix failures before continuing.

### Testing with Stubs

Each package provides test doubles in a `*test/` sub-package with func-field stubs:
```go
stub := &executortest.StubPipeline{
    ExecuteFunc: func(ctx context.Context, job jobmanager.Job) (jobmanager.Result, error) {
        return jobmanager.Result{}, nil
    },
}
```

## Environment Variable Naming

Environment variables follow the pattern `JIRA_AI_<SECTION>_<FIELD>`:
- `JIRA_AI_JIRA_BASE_URL` -> `jira.base_url`
- `JIRA_AI_GITHUB_APP_ID` -> `github.app_id`
- `JIRA_AI_AI_PROVIDER` -> `ai_provider`
- `JIRA_AI_WORKSPACES_BASE_DIR` -> `workspaces.base_dir`
- `JIRA_AI_GUARDRAILS_MAX_CONCURRENT_JOBS` -> `guardrails.max_concurrent_jobs`

See `models/config.go` LoadConfig() for complete environment variable binding.

## File Structure Notes

- `main.go`: Application entry point, service wiring, HTTP server, graceful shutdown
- `models/`: Configuration and data structures (Jira types, domain types)
- `services/`: Infrastructure service implementations (Jira REST API, GitHub App/Git Data API)
- `tracker/`: IssueTracker interface and Jira adapter
- `workspace/`: Ticket-scoped workspace management
- `container/`: Container runtime detection, image resolution, lifecycle management
- `executor/`: New-ticket and PR-feedback execution pipelines
- `jobmanager/`: Concurrency control, retry tracking, circuit breaker
- `scanner/`: Polling-based ticket and feedback discovery
- `commentfilter/`: Bot-loop prevention logic
- `recovery/`: Crash recovery and startup cleanup
- `costtracker/`: Daily AI cost tracking
- `projectresolver/`: Ticket-to-project-config mapping
- `taskfile/`: AI task file generation (universal instructions + new-ticket workflow from repo files or project-config fallback)
- `repoconfig/`: Per-repo `.ai-bot/config.yaml` parsing (PR, AI, imports)
- `config.example.yaml`: Complete configuration reference with comments
- `docs/`: Architecture, debugging, setup guides, and [repo-level configuration](docs/repo-configuration.md)
