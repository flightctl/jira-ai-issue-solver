# Repository Configuration

This guide explains how to configure target repositories so the bot knows
how to build, test, and containerize your project. All configuration is
optional — the bot works without any repo-level files by using its own
defaults.

## Configuration Files

The bot checks for the following repo-level files:

| File | Purpose |
|------|---------|
| `.ai-bot/config.yaml` | Bot-specific settings: PR preferences, validation commands, AI provider config, repo imports |
| `.ai-bot/instructions.md` | AI guidance: workflow references, validation commands, coding standards (injected into the task prompt) |
| `.ai-bot/container.json` | Bot-specific container settings: image, env, resource limits |
| `.devcontainer/devcontainer.json` | Standard devcontainer config (practical subset supported) |

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

## AI Instructions (`.ai-bot/instructions.md`)

This is the primary mechanism for giving the AI agent project-specific
guidance. The file contents are appended to the task prompt as a
"Project Instructions" section, so the AI sees them regardless of which
provider is in use (Claude, Gemini, etc.).

```markdown
## Workflow
Follow the bugfix workflow at .ai-workflows/bugfix/skills/controller.md.

## Validation
After making changes, run these commands to verify correctness:
- `make build`
- `make test`
- `make lint`

## Coding Standards
- Follow the existing code style in the repository.
- Add unit tests for all new functions.
- Do not modify generated files.
```

**Key characteristics:**

- **Provider-agnostic**: Unlike `CLAUDE.md` or `GEMINI.md`, this file
  reaches every AI provider through the task prompt.
- **Read automatically**: The bot reads this file before writing the task
  file. No configuration needed — just create the file.
- **Optional**: If the file is absent or empty, nothing is appended. The
  AI still gets the standard instructions ("implement this task, validate
  your changes, don't push to git").
- **Composable with imports**: Use `imports` in `.ai-bot/config.yaml` to
  clone a shared workflow repo, then reference the imported files from
  `instructions.md`.

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
| You want to tell the AI about your build/test commands | `.ai-bot/instructions.md` |
| You want to restrict which tools the AI can use | `.ai-bot/config.yaml` |
| Your devcontainer uses a Dockerfile (no `image` field) | `.ai-bot/container.json` (with a pre-built image) |
| You want the AI to follow a multi-step workflow | `.ai-bot/instructions.md` + `imports` in `.ai-bot/config.yaml` |
| You want shared AI skills/guidelines from another repo | `imports` in `.ai-bot/config.yaml` |
| You want provider-agnostic AI guidance | `.ai-bot/instructions.md` |

## Complete Example

A repository with all configuration files and a shared workflow import:

```text
your-repo/
├── .ai-bot/
│   ├── config.yaml          # Bot + AI settings + imports
│   ├── instructions.md      # AI guidance (injected into task prompt)
│   └── container.json       # Container settings (takes priority over devcontainer)
├── .devcontainer/
│   └── devcontainer.json    # Standard devcontainer (used if no .ai-bot/container.json)
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

And `.ai-bot/instructions.md`:

```markdown
## Workflow
Follow the bugfix workflow at .ai-workflows/bugfix/skills/controller.md.

## Validation
After making changes, run `make build`, `make test`, and `make lint`.
Fix all errors before finishing.
```

In practice, most teams need only one or two of these files. If you already
have a `.devcontainer/devcontainer.json` with an `image` field and don't
need custom bot settings, no additional files are needed. Adding just an
`instructions.md` is the quickest way to improve AI output quality.
