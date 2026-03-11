# Repository Configuration

This guide explains how to configure target repositories so the bot knows
how to build, test, and containerize your project. All configuration is
optional — the bot works without any repo-level files by using its own
defaults.

## Configuration Files

The bot checks for two types of repo-level files:

| File | Purpose |
|------|---------|
| `.ai-bot/config.yaml` | Bot-specific settings: PR preferences, validation commands, AI provider config |
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
| You want to tell the AI about your build/test commands | `.ai-bot/config.yaml` |
| You want to restrict which tools the AI can use | `.ai-bot/config.yaml` |
| Your devcontainer uses a Dockerfile (no `image` field) | `.ai-bot/container.json` (with a pre-built image) |

## Complete Example

A repository with all three files:

```text
your-repo/
├── .ai-bot/
│   ├── config.yaml          # Bot + AI settings
│   └── container.json       # Container settings (takes priority over devcontainer)
├── .devcontainer/
│   └── devcontainer.json    # Standard devcontainer (used if no .ai-bot/container.json)
└── ...
```

In practice, most teams need only one or two of these files. If you already
have a `.devcontainer/devcontainer.json` with an `image` field and don't
need custom bot settings, no additional files are needed.
