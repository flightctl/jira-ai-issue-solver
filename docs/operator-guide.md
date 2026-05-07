# Operator Guide

This guide walks you through setting up a running instance of the Jira AI Issue
Solver from scratch. By the end, you'll have a bot that watches Jira tickets,
runs AI agents against your codebase, and opens pull requests with the results.

**Time estimate:** 30–60 minutes for first-time setup.

## Contents

- [How the Bot Works](#how-the-bot-works)
- [Prerequisites](#prerequisites)
- [Step 1: Create a Jira API Token](#step-1-create-a-jira-api-token)
- [Step 2: Set Up the GitHub App](#step-2-set-up-the-github-app)
- [Step 3: Get an AI Provider API Key](#step-3-get-an-ai-provider-api-key)
- [Step 4: Prepare Your Jira Project](#step-4-prepare-your-jira-project)
- [Step 5: Build a Dev Container Image](#step-5-build-a-dev-container-image)
- [Step 6: Write Your Configuration](#step-6-write-your-configuration)
- [Step 7: Build and Run the Bot](#step-7-build-and-run-the-bot)
- [Step 8: Verify It Works](#step-8-verify-it-works)
- [Step 9: Onboard Your Team](#step-9-onboard-your-team)
- [Step 10: Configure Target Repositories](#step-10-configure-target-repositories)
- [Next Steps](#next-steps)
- [Troubleshooting](#troubleshooting)

## How the Bot Works

The bot follows a fork-based workflow:

1. **Scans Jira** for tickets in a "todo" status that have the bot's username
   as a contributor
2. **Clones the target repository** into a workspace and creates a branch
3. **Launches an AI agent** inside an ephemeral container with the cloned repo
   mounted, along with a task file describing the work
4. **Creates a pull request** from the assignee's fork to the upstream
   repository
5. **Monitors for PR review comments** and sends feedback back through the AI
   for revisions

The bot uses the Jira ticket's **Components** field to determine which
workspace (one or more repositories) to target, and the ticket's
**assignee** (mapped to a GitHub username) to determine which fork to
push to in each repo.

For a deeper understanding of the architecture, see
[architecture.md](architecture.md).

## Prerequisites

Gather these before you begin:

| Requirement | Details |
|-------------|---------|
| **Jira Cloud instance** | With admin or project-admin access to configure fields and workflows |
| **GitHub organization** | With permission to create GitHub Apps |
| **Container runtime** | Podman (preferred) or Docker installed on the host |
| **AI provider account** | Anthropic (Claude) or Google (Gemini) API key |
| **Go 1.24+** | Only if building from source instead of using the container image |

## Step 1: Create a Jira API Token

The bot authenticates to Jira using basic auth (email + API token).

1. Log in to [Atlassian account settings](https://id.atlassian.com/manage-profile/security/api-tokens)
2. Click **Create API token**
3. Give it a descriptive name (e.g., `jira-ai-bot`)
4. Copy the token — you won't see it again

Save these for later:

- **Jira base URL**: `https://your-domain.atlassian.net`
- **Jira username**: Your Jira Cloud email address
- **Jira API token**: The token you just created

> **Tip:** Consider creating a dedicated Jira service account for the bot
> rather than using a personal account. This makes it clear which actions the
> bot took and avoids issues if the account owner leaves the organization.

## Step 2: Set Up the GitHub App

The bot uses a GitHub App for authentication — this gives it scoped access to
create branches, push commits, and open pull requests.

Follow the step-by-step instructions in **[admin-setup.md](admin-setup.md)**.
That guide covers:

- Creating the app with the correct permissions (Contents: Read/Write, Pull
  Requests: Read/Write)
- Generating and saving the private key
- Installing the app on your upstream repository

When you're done, you'll have:

- **App ID** (a number, e.g., `2591456`)
- **Private key file** (a `.pem` file)
- **App name** (e.g., `my-org-ai-bot` — without the `[bot]` suffix)

Store the private key securely:

```bash
mkdir -p ~/keys
chmod 700 ~/keys
cp /path/to/downloaded.pem ~/keys/github-app.private-key.pem
chmod 600 ~/keys/github-app.private-key.pem
```

> **Important:** The app must be installed on the **upstream repository**
> (where PRs are opened). Contributors will also install it on their forks
> — see [Step 9](#step-9-onboard-your-team).

## Step 3: Get an AI Provider API Key

The bot supports two AI providers. You only need one.

### Option A: Claude (Anthropic)

1. Go to [console.anthropic.com/settings/keys](https://console.anthropic.com/settings/keys)
2. Create an API key
3. Save the key (starts with `sk-ant-api03-...`)

### Option B: Claude via Vertex AI (Google Cloud)

If your organization uses Claude through Google Cloud:

1. Create a GCP service account with Vertex AI access
2. Download the service account key JSON file
3. Note your GCP project ID and region (e.g., `us-east5`)

### Option C: Gemini (Google)

1. Go to [Google AI Studio](https://aistudio.google.com/apikey)
2. Create an API key
3. Save the key

## Step 4: Prepare Your Jira Project

The bot relies on specific Jira fields and workflow statuses. Configure these
before running the bot.

### 4a: Know Your Workflow Statuses

The bot transitions tickets through three states. You need to know the exact
status names in your Jira workflow (they are **case-sensitive**):

| Bot state | What it means |
|-----------|---------------|
| `todo` | Ticket is ready for the bot to pick up |
| `in_progress` | Bot is working on it |
| `in_review` | PR is open, waiting for human review |

The actual status names depend on the Jira issue template your project uses.
For example, a common Red Hat template uses:
`NEW`, `ASSIGNED`, `POST`, `MODIFIED`, `VERIFIED`, `RELEASE_PENDING`, `CLOSED`
— where the bot would map `todo` → "NEW", `in_progress` → "ASSIGNED",
`in_review` → "POST". Other projects might use "To Do", "In Progress",
"In Review".

To find your exact status names:

1. Open a ticket in the target Jira project
2. Click the status dropdown to see available transitions
3. Note the exact names (including capitalization and spacing)

> **Different ticket types can have different statuses.** For example, Bugs
> might use "NEW" → "ASSIGNED" → "POST" while Stories use "Backlog" →
> "Development" → "Testing". The bot supports per-type configuration.

### 4b: Ensure the Components Field Exists

The bot uses the **Components** field on Jira tickets to determine which GitHub
repository to target. This is a standard Jira field, but your project needs at
least one component defined.

1. Go to **Project settings** → **Components**
2. Create components that map to your repositories (e.g., "backend", "frontend",
   "api")

These component names will appear in your configuration file.

### 4c: Set Up a PR URL Field

The bot needs a way to record PR URLs on tickets for traceability — both so
humans can find the PR from the ticket and so the feedback scanner can find
the PR to monitor for review comments.

Without a dedicated field, the bot falls back to posting PR URLs as Jira
comments with a `[AI-BOT-PR]` tag. This works, but a dedicated field is
more reliable and easier to query.

**Check if a suitable field already exists:**

1. Open a ticket in the target project and look for a field like "Git Pull
   Request", "Pull Request URL", or similar
2. If your project template already includes one, note the exact field name

**If no field exists, create one:**

1. Go to **Jira Settings** → **Issues** → **Custom fields**
2. Create a **URL** or **Text** field named something like "Git Pull Request"
3. Add it to the relevant issue screens

Note the exact field name — you'll use it in the `git_pull_request_field_name`
config setting.

### 4d: Map Assignees to GitHub Usernames

The bot pushes code to the **assignee's fork** of the target repository. For
this to work, you need a mapping from Jira user identifiers to GitHub
usernames. Collect these from your team:

| Jira email | GitHub username |
|-----------|----------------|
| `alice@yourcompany.com` | `alice` |
| `bob.smith@yourcompany.com` | `bob-github` |

## Step 5: Build a Dev Container Image

The bot doesn't run the AI directly — it launches AI agents inside
**dev containers**. These containers need three categories of tools:

| Category | Examples | Why |
|----------|----------|-----|
| **AI CLI** | Claude Code, Gemini CLI | The bot execs the AI CLI inside the container |
| **Build tools** | Go, Rust, Node.js, Make | The AI needs to compile and test its changes |
| **Linters and validators** | golangci-lint, eslint, pytest | The AI needs to verify its output matches project standards |

You can start with a generic image like
`mcr.microsoft.com/devcontainers/universal:2` (which includes many language
runtimes), but for best results you should build a custom image tailored to
your project.

### Example Containerfile

Here's a real-world example for a Go project using Claude Code:

```dockerfile
FROM fedora:43

# Build tools and system dependencies
RUN dnf install -y \
        make git bash coreutils grep \
        nodejs npm \
    && dnf clean all

# Go toolchain (match your project's go.mod version)
RUN curl -fsSL https://go.dev/dl/go1.24.6.linux-amd64.tar.gz \
    | tar -C /usr/local -xz \
    && ln -sf /usr/local/go/bin/go /usr/bin/go

# Linters (match your CI versions)
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b /usr/local/bin v1.64.8

# AI CLI
RUN npm install -g @anthropic-ai/claude-code@latest

# Non-root user
RUN useradd -m -s /bin/bash builder
USER builder
WORKDIR /workspace
```

> **Key principle:** Match what CI runs. If your CI uses Go 1.24 and
> golangci-lint v1.64.8, put those same versions in the image. The AI's
> changes should pass CI on the first try.

### Build and name the image

```bash
podman build -f Containerfile -t my-project-ai:latest .
```

The image name (e.g., `my-project-ai:latest`) is what you'll reference in
your bot configuration in the next step.

> **A real example** lives in this repository at
> `images/flightctl-ai/Containerfile` — a Go project image with Claude Code,
> Gemini CLI, linters, and system dependencies for building flightctl.

## Step 6: Write Your Configuration

Create a `config.yaml` file. This section walks through each block and
connects it back to where you gathered the information. See
[config.example.yaml](../config.example.yaml) for the full reference.

### 6a: Jira Credentials

> **From [Step 1](#step-1-create-a-jira-api-token):** You created a Jira API
> token and noted your base URL and username.

```yaml
jira:
  base_url: https://your-domain.atlassian.net   # Your Jira Cloud URL
  username: your-jira-email@yourcompany.com      # The email you used in Step 1
  api_token: your-jira-api-token                 # The API token from Step 1
  interval_seconds: 300                          # Poll every 5 minutes
```

### 6b: Assignee Mapping

> **From [Step 4d](#4d-map-assignees-to-github-usernames):** You collected
> Jira email → GitHub username pairs from your team.
>
> **Ongoing:** When new team members [onboard](#step-9-onboard-your-team),
> add their mapping here and restart the bot.

```yaml
  # (inside the jira: block)
  assignee_to_github_username:
    "alice@yourcompany.com": alice            # Emails MUST be quoted (YAML @)
    "bob.smith@yourcompany.com": bob-github
```

### 6c: Project Configuration

> **From [Step 4a](#4a-know-your-workflow-statuses):** You found the exact
> status names for each ticket type.
>
> **From [Step 4b](#4b-ensure-the-components-field-exists):** You created
> Jira components that map to repositories.
>
> **From [Step 4c](#4c-set-up-a-pr-url-field):** You identified or created
> a field for PR URLs.
>
> **From [Step 5](#step-5-build-a-dev-container-image):** You built a dev
> container image and noted its name.

```yaml
  # (inside the jira: block)
  projects:
    - project_keys:
        - "MYPROJ"                               # Your Jira project key

      # PR URL field name from Step 4c (recommended for traceability)
      git_pull_request_field_name: "Git Pull Request"

      # Status names must EXACTLY match your Jira workflow (case-sensitive!)
      # Each ticket type you want the bot to handle needs an entry.
      # These examples use a common Red Hat template; yours will differ.
      status_transitions:
        Bug:
          todo: "NEW"                            # From Step 4a
          in_progress: "ASSIGNED"
          in_review: "POST"
        Story:
          todo: "NEW"
          in_progress: "ASSIGNED"
          in_review: "POST"

      # Profiles bundle container and instruction settings.
      # Multiple components can share a profile.
      profiles:
        default:
          container:
            image: "my-project-ai:latest"        # The image you built in Step 5
            resource_limits:
              memory: "8g"
              cpus: "4"
          instructions: |
            After making changes, run:
            - `make build`
            - `make test`

      # Workspaces define named groups of repositories.
      # Multiple components can share a workspace.
      workspaces:
        default:
          repos:
            - name: your-repo
              url: https://github.com/your-org/your-repo.git
              profile: default
              # target_branch: main  # defaults to "main" if omitted

      # Component names match the Jira Components field (case-insensitive).
      # Each component maps to a workspace name.
      components:
        backend:                                 # Jira component name from Step 4b
          workspace: default
```

### 6d: GitHub App Credentials

> **From [Step 2](#step-2-set-up-the-github-app):** You created a GitHub App
> and saved the App ID, private key, and app name.

```yaml
github:
  app_id: 2591456                                # App ID from Step 2
  private_key_path: /etc/bot/key.pem             # Path INSIDE the bot's container
  bot_username: my-org-ai-bot                    # App name from Step 2, no [bot] suffix
  pr_label: ai-pr
  max_thread_depth: 5
  known_bot_usernames:                           # Other bots whose PR comments to ignore
    - "github-actions"
    - "dependabot"
```

> **Important:** `private_key_path` is the path where the `.pem` file will be
> mounted **inside the bot's container** — not the host path. This must match
> the volume mount you use in [Step 7](#step-7-build-and-run-the-bot).

### 6e: AI Provider

> **From [Step 3](#step-3-get-an-ai-provider-api-key):** You obtained an API
> key for Claude or Gemini.

Choose one:

```yaml
# Option A: Claude with direct API key
ai_provider: claude
claude:
  api_key: "sk-ant-api03-..."                    # API key from Step 3

# Option B: Claude via Vertex AI
ai_provider: claude
claude:
  vertex_project_id: "your-gcp-project-id"       # From Step 3
  vertex_region: "us-east5"
  vertex_credentials_file: "/path/to/sa-key.json" # Host path to GCP SA key

# Option C: Gemini
ai_provider: gemini
gemini:
  api_key: "your-gemini-api-key"                 # API key from Step 3
```

### 6f: Workspaces, Container Runtime, and Guardrails

These sections use sensible defaults. Adjust as needed.

```yaml
workspaces:
  base_dir: /var/lib/ai-bot/workspaces           # Per-ticket workspace directory
  ttl_days: 7                                    # Clean up after 7 days

container:
  runtime: auto                                  # Auto-detects podman or docker

guardrails:
  max_concurrent_jobs: 5                         # Max parallel AI sessions
  max_retries: 3                                 # Retries per ticket before giving up
  max_daily_cost_usd: 50.0                       # Pauses jobs when exceeded (resets midnight UTC)
  max_container_runtime_minutes: 60              # Kill AI containers after this
```

### Putting It All Together

Your final `config.yaml` is sections 6a through 6f combined into one file.
Before moving on, verify these cross-references:

<!-- markdownlint-disable MD013 -->
| Config field | Must match | Where you set it |
|-------------|-----------|-----------------|
| `jira.base_url`, `username`, `api_token` | Your Jira Cloud credentials | [Step 1](#step-1-create-a-jira-api-token) |
| `github.app_id` | The App ID shown on the GitHub App settings page | [Step 2](#step-2-set-up-the-github-app) |
| `github.bot_username` | The GitHub App name (without `[bot]` suffix) | [Step 2](#step-2-set-up-the-github-app) |
| `github.private_key_path` | The container mount path for the `.pem` file | [Step 7](#step-7-build-and-run-the-bot) (volume mount) |
| `claude.api_key` or `gemini.api_key` | Your AI provider API key | [Step 3](#step-3-get-an-ai-provider-api-key) |
| `status_transitions` values | Exact Jira workflow status names (case-sensitive) | [Step 4a](#4a-know-your-workflow-statuses) |
| `components` keys | Jira component names mapped to workspace names (case-insensitive) | [Step 4b](#4b-ensure-the-components-field-exists) |
| `git_pull_request_field_name` | Your Jira PR URL field name | [Step 4c](#4c-set-up-a-pr-url-field) |
| `assignee_to_github_username` | Jira email → GitHub username pairs | [Step 4d](#4d-map-assignees-to-github-usernames) |
| Profile `container.image` | The dev container image you built | [Step 5](#step-5-build-a-dev-container-image) |
<!-- markdownlint-enable MD013 -->

## Step 7: Build and Run the Bot

### Option A: Run in a Container (Recommended)

Build the image:

```bash
git clone https://github.com/your-org/jira-ai-issue-solver.git
cd jira-ai-issue-solver
make build
```

Run it, mounting your config, private key, and workspace volume:

```bash
podman run -d --name ai-bot -p 8080:8080 \
  -v ~/config.yaml:/app/config.yaml:ro \
  -v ~/keys/github-app.private-key.pem:/etc/bot/key.pem:ro \
  -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \
  --replace jira-ai-issue-solver:latest
```

Notes on the mounts:

- **Config and key** are mounted read-only (`:ro`) for security
- **Workspaces** must be read-write — the bot clones repos here
- The **key mount path** (`/etc/bot/key.pem`) must match
  `github.private_key_path` in your config

> **Container runtime access:** The bot spawns AI agent containers on the
> host. If running the bot itself in a container, you need to give it access
> to the host's container runtime socket. For rootless podman:
>
> ```bash
> podman run -d --name ai-bot -p 8080:8080 \
>   -v ~/config.yaml:/app/config.yaml:ro \
>   -v ~/keys/github-app.private-key.pem:/etc/bot/key.pem:ro \
>   -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \
>   -v /run/user/$(id -u)/podman/podman.sock:/run/podman/podman.sock \
>   -e CONTAINER_HOST=unix:///run/podman/podman.sock \
>   --replace jira-ai-issue-solver:latest
> ```

### Option B: Run the Binary Directly

If you prefer running outside a container (useful for development):

```bash
go build -o jira-ai-issue-solver main.go
./jira-ai-issue-solver -config config.yaml
```

When running directly, `github.private_key_path` should be the host path to
the `.pem` file.

## Step 8: Verify It Works

### 8a: Check the Logs

```bash
# If running in a container:
podman logs ai-bot 2>&1 | head -20
```

You should see:

```text
Container runtime detected    {"runtime": "podman", "path": "/usr/bin/podman"}
Scanners started
Starting server               {"port": 8080}
```

Look for errors:

```bash
podman logs ai-bot 2>&1 | grep -i "fatal\|error"
```

### 8b: Check the Health Endpoint

```bash
curl http://localhost:8080/health
# Should return: OK
```

### 8c: Test with a Real Ticket

1. Create a ticket in your configured Jira project
2. Set the **Components** field to match a `components` key in your config
   (e.g., "backend")
3. Set the **assignee** to someone in your `assignee_to_github_username` map
4. Add the bot's Jira username as a **Contributor** on the ticket
5. Set the ticket status to your configured "todo" status (e.g., "To Do")
6. Wait up to `interval_seconds` (default: 300 seconds) for the scanner to
   pick it up
7. Watch progress:

```bash
podman logs -f ai-bot
```

You should see the bot:

1. Detect the ticket and transition it to "In Progress"
2. Clone the repository and create a branch (`jira/MYPROJ-123`)
3. Start an AI container
4. Create a PR and transition the ticket to "In Review"
5. Post the PR URL to the Jira ticket

### Verification Checklist

- [ ] Container starts without fatal errors
- [ ] Health endpoint returns OK
- [ ] Logs show "Container runtime detected" and "Scanners started"
- [ ] Bot picks up a test ticket and transitions it to "In Progress"
- [ ] Bot creates a PR with AI-generated changes
- [ ] Bot transitions ticket to "In Review" and posts PR URL
- [ ] Bot responds to PR review comments (leave a review comment and wait)

## Step 9: Onboard Your Team

Once the bot is running, team members need to do two things:

### Share the GitHub App Installation Link

Every contributor must install the GitHub App on their **personal fork** so the
bot can push branches there. Send your team a message like:

> **AI Bot Setup (2 minutes)**
>
> The AI bot is now active for [PROJECT] tickets. To get AI-generated PRs for
> tickets assigned to you:
>
> 1. Install the GitHub App on your fork:
>    `https://github.com/apps/YOUR-APP-NAME`
>    → Select "Only select repositories" → choose your fork
>
> 2. Send me your **Jira email** and **GitHub username** so I can add the
>    mapping to the bot config.
>
> 3. That's it! When you're assigned a ticket with a Components field set,
>    the bot will create a PR from your fork.
>
> Full instructions: [Contributor Setup Guide](contributor-setup.md)

### Update Configuration for New Contributors

When new team members reach out, add their mapping to the config and restart:

```yaml
# In config.yaml → jira.assignee_to_github_username
"new-user@yourcompany.com": new-user-github     # ← add this
```

> **This is the same `assignee_to_github_username` field from
> [Step 6b](#6b-assignee-mapping).** Every contributor needs an entry here,
> or the bot won't know which fork to push to.

## Step 10: Configure Target Repositories

At this point the bot is running and processing tickets using the profile
settings from your `config.yaml`. For many teams, that's sufficient. But you
can also configure target repositories directly by adding an `.ai-bot/`
directory — this gives repository owners control over how the AI works in
their codebase without requiring changes to the bot's central config.

### The `.ai-bot/` Directory

Create this directory in the **target repository** (the repo the bot clones
and works on — not the bot's own repository). Each file is optional:

```text
your-repo/
└── .ai-bot/
    ├── instructions.md          # Universal AI guidance (all task types)
    ├── new-ticket-workflow.md   # Multi-phase workflow (new tickets only)
    ├── feedback-workflow.md     # Feedback workflow (PR feedback only)
    ├── config.yaml              # Bot settings: PR prefs, imports, AI config
    └── container.json           # Container overrides: image, env, limits
```

| File | What it does | When it's used |
|------|-------------|----------------|
| `instructions.md` | Tells the AI your validation commands, coding standards, and project rules | Appended to **every** task file (new tickets and PR feedback) |
| `new-ticket-workflow.md` | Guides the AI through a structured workflow (assess, diagnose, fix, test, review) | Appended **only** to new-ticket task files |
| `feedback-workflow.md` | Guides the AI through PR review comment handling with session context recovery | Appended **only** to feedback task files |
| `config.yaml` | Configures PR labels/titles, AI tool restrictions, validation command hints, and repo-level imports | Read by the bot before each session |
| `container.json` | Overrides the container image, environment variables, or resource limits for this repo | Merged with profile settings (repo takes priority) |

> **Repo-level files take precedence** over profile settings from the bot's
> `config.yaml`. For example, if your profile sets `instructions` and the
> repo has `.ai-bot/instructions.md`, the repo file wins. This lets repo
> owners customize without needing access to the bot's config.

### Starting Simple

Most teams need only one or two of these files. A good progression:

1. **No files** — the bot works with defaults. The AI gets basic instructions
   ("implement this task, validate your changes").
2. **Add `instructions.md`** — tell the AI your validation commands. This
   is the single biggest improvement to output quality.
3. **Add `new-ticket-workflow.md`** — guide the AI through a structured
   problem-solving process for new tickets.
4. **Add `config.yaml` with imports** — pull in shared workflow skills from
   another repository so the workflow file can reference them.

### Example: Minimal `instructions.md`

```markdown
After making changes, validate with:
- `make build`
- `make test`
- `make lint`

Follow the existing code style in the repository.
Add unit tests for all new functions.
```

### Imports: Sharing AI Workflows Across Repositories

If you have multiple repositories, you probably don't want to duplicate AI
workflow definitions in each one. **Imports** let you clone an external
repository into the workspace so the AI can reference its files.

**Why imports exist:** AI workflows — structured multi-phase instructions
like "assess, diagnose, fix, test, review" — can be complex and benefit
from centralized maintenance. Rather than copying workflow skill files into
every target repo, you maintain them in one place and import them.

Imports can be configured in two places:

1. **Profile-level** (in the bot's `config.yaml`) — applied to all repos
   using that profile
2. **Repo-level** (in `.ai-bot/config.yaml`) — specific to one repo

Example in `.ai-bot/config.yaml`:

```yaml
imports:
  - repo: https://github.com/your-org/ai-workflows
    path: .ai-workflows           # cloned here, relative to workspace root
    ref: main                     # branch/tag/commit (optional)
    install: .ai-workflows/install.sh  # run inside container after cloning (optional)
```

Then in `.ai-bot/new-ticket-workflow.md`, reference the imported files:

```markdown
Execute the following workflow phases in order:

1. Read and execute .ai-workflows/bugfix/skills/diagnose.md
2. Read and execute .ai-workflows/bugfix/skills/fix.md
3. Read and execute .ai-workflows/bugfix/skills/test.md
4. Read and execute .ai-workflows/bugfix/skills/review.md

Write a PR title and description to .ai-bot/pr.md.
```

For the complete reference on all `.ai-bot/` files, field formats, container
configuration resolution, and the full file contract between bot and AI, see
[repo-configuration.md](repo-configuration.md).

## Next Steps

- **Tune guardrails** — adjust concurrency limits, daily cost budget,
  container timeouts, and circuit breaker thresholds. See the `guardrails`
  section in [config.example.yaml](../config.example.yaml).

- **Add more projects** — add entries to the `jira.projects` list. Each
  project can have its own status transitions, workspaces, and profiles.

- **Review the architecture** at [architecture.md](architecture.md) to
  understand the full system design, crash recovery, and bot-loop prevention.

## Troubleshooting

### Bot starts but doesn't pick up tickets

1. **Components field not set.** The ticket must have a Components value that
   matches a key in your `components` config.
2. **Bot not added as contributor.** The bot's Jira username must be listed as
   a contributor on the ticket.
3. **Wrong status.** The ticket must be in the exact status configured as
   `todo` for that ticket type. Status names are case-sensitive.
4. **Assignee not mapped.** The ticket assignee's email must appear in
   `assignee_to_github_username`.
5. **No matching ticket type.** If the ticket is a "Bug" but you only
   configured status transitions for "Story", it won't be picked up.

### "GitHub App is not installed on {repo}"

The app must be installed on both the **upstream repository** (for PR
creation) and the **assignee's fork** (for pushing branches). Have the
assignee follow the [Contributor Setup Guide](contributor-setup.md).

### "could not read private key: permission denied"

The container user can't read the mounted `.pem` file. Run the container as
your host user so the existing `0600` permissions are honored:

```bash
podman run -d --name ai-bot -p 8080:8080 \
  --user $(id -u):$(id -g) \
  -v ~/config.yaml:/app/config.yaml:ro \
  -v ~/keys/github-app.private-key.pem:/etc/bot/key.pem:ro \
  -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \
  --replace jira-ai-issue-solver:latest
```

If you can't change the container user, grant read access via group ownership
rather than making the key world-readable:

```bash
chown :$(id -g) ~/keys/github-app.private-key.pem
chmod 640 ~/keys/github-app.private-key.pem
```

### "failed to get installation ID"

- Verify `app_id` matches the number from the GitHub App settings page.
- Verify `bot_username` is the app name **without** the `[bot]` suffix.
- Verify the private key hasn't been revoked.

### "Failed to detect container runtime"

The bot needs podman or docker on the host to spawn AI containers:

```bash
podman --version   # or: docker --version
```

If running the bot in a container, make sure the host's container runtime
socket is mounted (see the socket mount in
[Step 7](#option-a-run-in-a-container-recommended)).

### AI container starts but produces no changes

- Check that the dev container image has the AI CLI installed (Claude Code,
  Gemini CLI, etc.) — see [Step 5](#step-5-build-a-dev-container-image).
- Check that the AI provider API key is valid — look for authentication
  errors in the bot logs.
- Try increasing `guardrails.max_container_runtime_minutes` if the AI is
  running out of time on complex tickets.

### Cost budget exceeded

The bot pauses job creation when `guardrails.max_daily_cost_usd` is exceeded.
The budget resets at midnight UTC. Increase the limit or wait for the reset.
Check current spending in the logs.
