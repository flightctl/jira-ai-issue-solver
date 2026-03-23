# Repository Configuration

This guide explains how to configure target repositories so the bot knows
how to build, test, and containerize your project. All configuration is
optional — the bot works without any repo-level files by using its own
defaults.

## Configuration Files

The bot checks for the following repo-level files:

| File | Purpose |
|------|---------|
| File | Purpose | Applies to |
|------|---------|------------|
| `.ai-bot/instructions.md` | Universal AI guidance: validation commands, coding standards | All task types |
| `.ai-bot/new-ticket-workflow.md` | Multi-phase workflow for new tickets (assess → fix → test → review) | New tickets only |
| `.ai-bot/feedback-workflow.md` | Workflow for PR feedback (context recovery, artifact updates) | PR feedback only |
| `.ai-bot/config.yaml` | Bot settings: PR preferences, validation commands, AI provider config, repo imports | Bot behavior |
| `.ai-bot/container.json` | Container settings: image, env, resource limits | Container setup |
| `.devcontainer/devcontainer.json` | Standard devcontainer config (practical subset supported) | Container setup |

### Runtime Files (Bot ↔ AI Contract)

The bot writes input files for the AI and reads output files after the
session. All `.ai-bot/` runtime files are automatically excluded from
commits at the GitHub API level.

**Bot → AI (inputs):**

| File | Written by | Purpose |
|------|-----------|---------|
| `.ai-bot/task.md` | Bot | Session-specific instructions (what to do) |
| `.ai-bot/issue.md` | Bot | Original ticket context (key, summary, description) |
| `.ai-bot/attachments/` | Bot | Downloaded Jira attachments |

**AI → Bot (outputs):**

| File | Written by | Purpose | Session type |
|------|-----------|---------|--------------|
| `.ai-bot/pr.md` | AI | PR title (first line) and description (remaining lines) | Both |
| `.ai-bot/comment-responses.json` | AI | Per-comment response summaries (see format below) | Feedback only |
| `.ai-bot/session-output.json` | Wrapper script | Session metadata (cost, exit code, validation status) | Both |

**AI → AI (cross-session context):**

| File | Written by | Purpose |
|------|-----------|---------|
| `.ai-bot/session-context.md` | AI workflow | Decision log — initial session context plus feedback round summaries |
| `.ai-bot/diagnosis.md` | AI workflow | Root cause analysis |
| `.ai-bot/implementation-notes.md` | AI workflow | File changes, design rationale, test strategy |
| `.ai-bot/test-verification.md` | AI workflow | Test results summary |
| `.ai-bot/review.md` | AI workflow | Self-review findings |

Cross-session files are written by the AI workflow (not the bot) and
persist across sessions. The feedback workflow reads these to recover
context from the initial implementation session.

#### `comment-responses.json` Format

The bot reads this file after feedback sessions to post descriptive
replies to PR review comments. If the file is missing or unparseable,
the bot falls back to generic "Addressed in \<commit\>" replies.

```json
[
  {"comment_id": 123, "response": "Switched to Optional pattern as suggested."},
  {"comment_id": 456, "response": "Kept the fallback path — needed for v1 backward compat."}
]
```

The `comment_id` values correspond to the IDs included in the task file's
review comment headers (e.g., `> [@reviewer, line 42, comment_id 123]`).
Keep responses concise (1-2 sentences).

These files live in the **target repository** (the repo the bot clones and
works on), not in the bot's own repository.

## Container Configuration

Container config determines the environment where the AI agent runs. The
bot resolves container settings from multiple sources in priority order:

```text
1. .ai-bot/container.json        (highest priority — bot-specific)
2. .devcontainer/devcontainer.json (standard devcontainer format)
3. Bot-level defaults             (from the bot's config.yaml)
4. Built-in fallback              (ubuntu:latest)
```

Only the highest-priority repo-level source is used — sources 1 and 2 do
**not** stack. Within the selected source, any field left unset falls
through to bot-level defaults, then to the built-in fallback.

### `.ai-bot/container.json`

This is the preferred format when you want full control over the bot's
container environment. It uses a simple flat JSON schema.

```json
{
  "image": "your-org/dev-environment:latest",
  "postCreateCommand": "make setup",
  "env": {
    "GOPATH": "/go",
    "LANG": "en_US.UTF-8"
  },
  "resourceLimits": {
    "memory": "16g",
    "cpus": "4"
  }
}
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `image` | string | Container image reference. If omitted, the bot's default image is used. |
| `postCreateCommand` | string | Shell command to run after the container starts (e.g., dependency installation). |
| `env` | object | Static environment variables set in the container. These are separate from runtime env vars (API keys) that the bot injects. |
| `resourceLimits.memory` | string | Memory limit in container runtime format (e.g., `"8g"`, `"512m"`). |
| `resourceLimits.cpus` | string | CPU limit in container runtime format (e.g., `"4"`, `"0.5"`). |

All fields are optional. A minimal file that only sets the image:

```json
{
  "image": "your-org/dev-environment:latest"
}
```

An empty `{}` is valid — the bot will use its defaults for everything but
will record that a repo-level config was found (visible in logs).

### `.devcontainer/devcontainer.json`

If your repository already has a devcontainer config, the bot can use it.
Only a practical subset of the [devcontainer spec](https://containers.dev/implementors/json_reference/)
is supported:

```jsonc
{
  // Image to use (required for the bot — build-based configs are not supported)
  "image": "mcr.microsoft.com/devcontainers/go:1.24",

  // Run after the container starts (string or string array)
  "postCreateCommand": "go mod download",

  // Environment variables inside the container
  "containerEnv": {
    "GOPATH": "/go"
  }
}
```

**Supported fields:**

| Field | Type | Supported |
|-------|------|-----------|
| `image` | string | Yes |
| `postCreateCommand` | string or string[] | Yes — arrays are joined with `&&` |
| `containerEnv` | object | Yes |
| `build` | object | No — logged as warning, ignored |
| `features` | object | No — logged as warning, ignored |
| `forwardPorts` | array | No — logged as warning, ignored |
| `customizations` | object | No — logged as warning, ignored |
| `mounts` | array | No — logged as warning, ignored |
| `runArgs` | array | No — logged as warning, ignored |
| `remoteUser` | string | No — logged as warning, ignored |
| `remoteEnv` | object | No — logged as warning, ignored |

JSONC syntax (single-line `//` comments, multi-line `/* */` comments, and
trailing commas) is fully supported.

**Important:** The bot does not support `build`-based devcontainer configs.
If your devcontainer uses a Dockerfile instead of a pre-built image, create
an `.ai-bot/container.json` with a pre-built image reference instead.

### Field Merging

When the bot resolves container config, fields merge from lower-priority
sources to higher-priority ones:

```text
Built-in fallback:  image=ubuntu:latest
Bot defaults:       image=admin-default:latest, memory=8g, cpus=4
Repo config:        image=repo-image:latest, memory=16g
────────────────────────────────────────────────────────────
Resolved:           image=repo-image:latest, memory=16g, cpus=4
```

- `image` came from the repo config (highest priority that set it)
- `memory` came from the repo config (overrides bot default)
- `cpus` came from the bot default (repo config didn't set it)

Environment variables merge additively: repo-level keys override bot-level
keys with the same name, but bot-level keys not present in the repo config
are preserved.

## Design: Two Task Types, Two Instruction Channels

The bot handles two fundamentally different scenarios, and the AI needs
different guidance for each:

**New tickets** require the AI to understand a problem, design a
solution, implement it, and validate it. This is a complex, multi-phase
task that benefits from structured workflow guidance (assess the bug,
diagnose the root cause, implement the fix, run tests, self-review).

**PR feedback** requires the AI to read specific reviewer comments, make
targeted changes, and verify nothing broke. The scope is narrow and
well-defined — the AI doesn't need to re-assess or re-diagnose anything.

This is why the bot provides two separate instruction channels:

| Channel | File | Section heading | Applies to |
|---------|------|-----------------|------------|
| **Universal instructions** | `.ai-bot/instructions.md` | `## Project Instructions` | All task types |
| **New-ticket workflow** | `.ai-bot/new-ticket-workflow.md` | `## Workflow` | New tickets only |
| **Feedback workflow** | `.ai-bot/feedback-workflow.md` | `## Workflow` | PR feedback only |

Both are provider-agnostic (unlike `CLAUDE.md` or `GEMINI.md`) — they
reach every AI provider through the task prompt.

### What goes where

**Universal instructions** (`instructions.md`): Anything the AI should
do regardless of whether it's implementing a new ticket or addressing
review feedback. Validation commands, coding standards, project-specific
rules.

**New-ticket workflow** (`new-ticket-workflow.md`): Multi-phase
orchestration for new tickets. References to skill files, phase ordering,
iteration caps. This content would confuse the AI during feedback
handling ("why am I being told to assess and diagnose a bug when I just
need to change a variable name?"), which is why it's separate.

**Feedback workflow** (`feedback-workflow.md`): Structured process for
addressing PR review comments. Typically lighter than the new-ticket
workflow — read prior context, address comments, verify, update session
artifacts. Feedback tasks never see the new-ticket workflow, and vice
versa.

## Universal Instructions (`.ai-bot/instructions.md`)

This file is appended to **every** task file the bot generates — both
new tickets and PR feedback. Use it for guidance that applies universally.

```markdown
After making changes, validate with:
- `make build`
- `make test`
- `make lint`

Follow the existing code style in the repository.
Add unit tests for all new functions.
Do not modify generated files.
```

**Key characteristics:**

- **Provider-agnostic**: Reaches every AI provider through the task
  prompt, unlike provider-specific config files.
- **Read automatically**: The bot reads this file before writing the task
  file. No configuration needed — just create the file.
- **Optional**: If the file is absent or empty, nothing is appended. The
  AI still gets the standard instructions ("implement this task, validate
  your changes, don't push to git").
- **Composable with imports**: Use `imports` in `.ai-bot/config.yaml` to
  clone a shared workflow repo, then reference the imported files from
  `instructions.md`.
- **Prototyping**: Admins can set `instructions` in the bot's project
  config to prototype content before committing the file to the repo.
  The repo-level file takes precedence when present.

## New-Ticket Workflow (`.ai-bot/new-ticket-workflow.md`)

This file is appended **only** to new-ticket task files. Feedback tasks
never see it. Use it for multi-phase workflows that guide the AI through
a structured problem-solving process.

```markdown
Execute the following bugfix workflow phases in order.
Each phase is defined in the corresponding skill file.

1. Read and execute .ai-workflows/bugfix/skills/assess.md
   The bug report is in .ai-bot/task.md. Do not ask clarifying
   questions — make reasonable assumptions where needed.

2. Read and execute .ai-workflows/bugfix/skills/diagnose.md
   Write your root cause analysis to .ai-bot/diagnosis.md.

3. Read and execute .ai-workflows/bugfix/skills/fix.md
   Implement the minimal fix. Do not run tests yet.

4. Read and execute .ai-workflows/bugfix/skills/test.md
   Write regression tests and run the full suite. If tests fail,
   revise your fix and retest (up to 5 iterations).

5. Read and execute .ai-workflows/bugfix/skills/review.md
   Self-review your changes. If issues are found, correct them,
   retest, and re-review (up to 4 iterations).

6. Write a PR title and description to .ai-bot/pr.md.
   First line is the title. Remaining lines are the body.
   Include a Root Cause section from .ai-bot/diagnosis.md.
```

**Key characteristics:**

- **New tickets only**: The bot does not append this to feedback task
  files. Feedback handling uses the standard instructions ("address each
  review comment") plus universal project instructions.
- **References skill files directly**: Point to specific files in the
  workspace rather than referencing a controller. The AI follows explicit
  instructions, not an interactive workflow.
- **Iteration caps**: Always cap loops (test retries, review iterations)
  to prevent the AI from burning tokens in circles.
- **Prototyping**: Admins can set `new_ticket_workflow` in the bot's
  project config to iterate on workflow content before committing the
  file. The repo-level file takes precedence when present.

### Why not put workflows in `instructions.md`?

If `instructions.md` contains "execute the bugfix workflow phases:
assess, diagnose, fix, test, review," those instructions are also
appended to feedback task files. The AI receives a feedback task
("address this review comment about variable naming") alongside
instructions to "assess the bug and diagnose the root cause." This
creates conflicting signals. The AI may try to run the full workflow
when it should make a targeted change, or it may ignore the workflow
entirely. Neither outcome is reliable.

Separating the channels eliminates the conflict: universal guidance
applies everywhere, workflow guidance applies only where it makes sense.

## Bot Configuration (`.ai-bot/config.yaml`)

This file provides hints for the AI agent and settings for the bot's PR
creation behavior. It is separate from container configuration.

```yaml
# Shell commands the AI can use for validation.
# These are hints, not directives — the AI decides when and how to use
# them. The AI reads these directly from the config file; they are not
# injected into the task prompt.
validation_commands:
  - make build
  - make lint
  - make test

# Auxiliary repositories to clone into the workspace before AI execution.
# Useful for shared workflow skills, scripts, guidelines, or tooling.
# These are cloned once; on workspace reuse, existing directories are
# skipped. Repo-level imports take precedence over project-level imports
# (from the bot's config.yaml) when both declare the same path.
imports:
  - repo: https://github.com/your-org/ai-workflows
    path: .ai-workflows       # destination relative to workspace root
    ref: main                  # branch/tag/commit (optional; default branch if omitted)
    install: .ai-workflows/install.sh  # shell command run inside container after cloning (optional)

# Pull request creation settings.
pr:
  # Create PRs as drafts (default: false).
  draft: true

  # Prefix prepended to PR titles (e.g., "[AI] Fix null pointer in handler").
  title_prefix: "[AI]"

  # Labels applied to created PRs (in addition to the bot's global pr_label).
  labels:
    - ai-generated
    - needs-review

# AI provider preferences (override bot-level defaults).
ai:
  # Claude-specific settings.
  claude:
    # Restrict which tools the AI can use (space-separated).
    allowed_tools: "Bash Edit Read Write"

  # Gemini-specific settings.
  gemini:
    # Model to use for this repository.
    model: "gemini-2.5-pro"
```

All fields and sections are optional. A minimal file:

```yaml
validation_commands:
  - npm test
```

When the file is absent, the bot uses its own defaults and the AI discovers
project conventions from the repository itself (CLAUDE.md, Makefile, CI
config, etc.).

### Field Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `validation_commands` | string[] | `[]` | Shell commands for validation (hints for AI) |
| `imports` | object[] | `[]` | Auxiliary repos to clone into the workspace |
| `imports[].repo` | string | — | Clone URL of the auxiliary repo (required) |
| `imports[].path` | string | — | Destination directory relative to workspace root (required) |
| `imports[].ref` | string | `""` | Branch, tag, or commit to check out (default branch if empty) |
| `imports[].install` | string | `""` | Shell command to run inside the container after cloning (from `/workspace`) |
| `pr.draft` | bool | `false` | Create PRs as drafts |
| `pr.title_prefix` | string | `""` | Prefix for PR titles |
| `pr.labels` | string[] | `[]` | Labels to apply to PRs |
| `ai.claude.allowed_tools` | string | `""` | Space-separated list of allowed Claude tools |
| `ai.gemini.model` | string | `""` | Gemini model override |

## When to Use Which File

| Scenario | Recommended file |
|----------|-----------------|
| You have a pre-built dev image with your toolchain | `.ai-bot/container.json` |
| You already have a devcontainer config with an `image` field | `.devcontainer/devcontainer.json` (no extra file needed) |
| You want to customize PR labels or titles | `.ai-bot/config.yaml` |
| You want to tell the AI about your build/test commands | `.ai-bot/instructions.md` (applies to all tasks) |
| You want to restrict which tools the AI can use | `.ai-bot/config.yaml` |
| Your devcontainer uses a Dockerfile (no `image` field) | `.ai-bot/container.json` (with a pre-built image) |
| You want the AI to follow a multi-phase workflow for new tickets | `.ai-bot/new-ticket-workflow.md` + `imports` in `.ai-bot/config.yaml` |
| You want shared AI skills/guidelines from another repo | `imports` in `.ai-bot/config.yaml` |
| You want provider-agnostic coding standards | `.ai-bot/instructions.md` |
| You want structured feedback handling with session continuity | `.ai-bot/feedback-workflow.md` + `imports` in `.ai-bot/config.yaml` |
| You want the AI to generate PR titles/descriptions | Reference `.ai-bot/pr.md` in `new-ticket-workflow.md` |

## Complete Example

A repository with all configuration files and a shared workflow import:

```text
your-repo/
├── .ai-bot/
│   ├── config.yaml               # Bot + AI settings + imports
│   ├── instructions.md           # Universal AI guidance (all task types)
│   ├── new-ticket-workflow.md    # Multi-phase workflow (new tickets only)
│   ├── feedback-workflow.md      # Feedback workflow (PR feedback only)
│   └── container.json            # Container settings (takes priority over devcontainer)
├── .devcontainer/
│   └── devcontainer.json         # Standard devcontainer (used if no .ai-bot/container.json)
└── ...
```

With `.ai-bot/config.yaml`:

```yaml
imports:
  - repo: https://github.com/your-org/ai-workflows
    path: .ai-workflows
    ref: main
    install: .ai-workflows/install.sh

validation_commands:
  - make build
  - make test

pr:
  title_prefix: "[AI]"
  labels:
    - ai-generated
```

And `.ai-bot/instructions.md` (universal — applies to new tickets AND feedback):

```markdown
After making changes, validate with:
- `make build`
- `make test`
- `make lint`

Follow the existing code style in the repository.
Add unit tests for all new functions.
```

And `.ai-bot/new-ticket-workflow.md` (new tickets only — NOT applied to feedback):

```markdown
Execute the following bugfix workflow phases in order.
Each phase is defined in the corresponding skill file.

1. Read and execute .ai-workflows/bugfix/skills/assess.md
   The bug report is in .ai-bot/task.md. Do not ask clarifying
   questions — make reasonable assumptions where needed.

2. Read and execute .ai-workflows/bugfix/skills/diagnose.md
   Write your root cause analysis to .ai-bot/diagnosis.md.

3. Read and execute .ai-workflows/bugfix/skills/fix.md
   Implement the minimal fix. Do not run tests yet.

4. Read and execute .ai-workflows/bugfix/skills/test.md
   Write regression tests and run the full suite. If tests fail,
   revise your fix and retest (up to 5 iterations).

5. Read and execute .ai-workflows/bugfix/skills/review.md
   Self-review your changes. If issues are found, correct them,
   retest, and re-review (up to 4 iterations).

6. Write a PR title and description to .ai-bot/pr.md.
   First line is the title. Remaining lines are the body.
   Include a Root Cause section from .ai-bot/diagnosis.md.
```

### What the AI sees

For a **new ticket**, the task file contains:

1. **Task context** — ticket key, summary, description
2. **Standard instructions** — "Implement this task, validate your changes, don't push to git"
3. **Project Instructions** — from `instructions.md` (validation commands, coding standards)
4. **Workflow** — from `new-ticket-workflow.md` (multi-phase bugfix workflow)

For **PR feedback**, the task file contains:

1. **PR context** — PR number, title, branch
2. **Review comments** — grouped by file, with author attribution, line numbers, and comment IDs
3. **Standard instructions** — read prior session context, address each review comment, validate changes
4. **Required output** — write `comment-responses.json` with per-comment summaries
5. **Project Instructions** — from `instructions.md` (validation commands, coding standards)
6. **Workflow** — from `feedback-workflow.md` (session context recovery, artifact updates)

Feedback tasks get the universal instructions and the feedback workflow,
but **not** the new-ticket workflow. The AI reads the review comments,
recovers prior session context, makes targeted changes, writes
per-comment response summaries, and updates the session context for
continuity across review rounds.

### Starting simple

In practice, most teams need only one or two of these files. A good
progression:

1. **No files** — the bot works with defaults. The AI gets basic
   instructions ("implement this task, validate your changes").
2. **Add `instructions.md`** — tell the AI your validation commands.
   This is the single biggest improvement to output quality.
3. **Add `new-ticket-workflow.md`** — guide the AI through a structured
   problem-solving process for new tickets.
4. **Add `config.yaml` with imports** — pull in shared workflow skills
   from another repo so the workflow file can reference them.
