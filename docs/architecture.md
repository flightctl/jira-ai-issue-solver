# Architecture & Workflow

This document explains how the Jira AI Issue Solver works with GitHub App
authentication and the fork-based workflow.

## Table of Contents

1. [Overview](#overview)
2. [Fork-Based Workflow](#fork-based-workflow)
3. [GitHub App Authentication](#github-app-authentication)
4. [Component Architecture](#component-architecture)
5. [Security Considerations](#security-considerations)
6. [Troubleshooting](#troubleshooting)

## Overview

The Jira AI Issue Solver automatically processes Jira tickets and creates GitHub
pull requests with AI-generated code changes. It uses a **fork-based workflow**
where the bot pushes code to the developer's fork (not the bot's own fork),
enabling true collaboration between the bot and the human developer.

**Key Features:**

- GitHub App authentication with fine-grained permissions
- Fork-based workflow enabling bot + developer collaboration
- Automatic installation ID discovery per repository
- Verified commits with GitHub App signature
- Configurable per-project workflows

## Fork-Based Workflow

### How It Works

```text
Main Repository (org/repo)
    ↑
    | Pull Request from alice:jira/PROJ-123
    |
Developer's Fork (alice/repo)
    ← Bot pushes code changes (via GitHub App)
    ← Developer pushes additional changes (owns the fork)

GitHub App Installation:
  - Installed on main repo (for creating PRs, reading comments)
  - Installed on alice's fork (for pushing code)
```

### Step-by-Step Process

1. **Ticket Assignment**: Jira ticket `PROJ-123` is assigned to Alice
   (<alice@company.com>)
2. **Username Mapping**: Bot maps `alice@company.com` → GitHub username `alice`
3. **Fork Discovery**: Bot discovers Alice's fork at `alice/repo`
4. **Installation Verification**: Bot verifies GitHub App is installed on
   Alice's fork
5. **Clone & Branch**: Bot clones Alice's fork and creates branch `jira/PROJ-123`
6. **AI Generation**: Bot uses AI to generate code changes
7. **Commit & Push**: Bot commits and pushes to Alice's fork
8. **Create PR**: Bot creates PR from `alice:jira/PROJ-123` → `org:main`
9. **Collaboration**: Alice can now push additional changes to the same branch
   in her fork

### Benefits

✅ **True Collaboration**: Both bot and developer can push to the same branch
✅ **Standard Workflow**: Uses GitHub's standard fork-based model
✅ **Clear Attribution**: PR shows both bot commits and developer commits
✅ **Developer Control**: Developer owns the fork and can uninstall the app anytime

## GitHub App Authentication

### What is a GitHub App?

A **GitHub App** is GitHub's recommended authentication method for automated
tools. Unlike user accounts with Personal Access Tokens, GitHub Apps:

- Have **fine-grained permissions** (only what's needed)
- Use **short-lived tokens** (1 hour, auto-refreshed)
- Show as **`app-name[bot]`** in GitHub (clear audit trail)
- Support **per-installation tokens** (different token for each repo)
- Have **higher rate limits** than user accounts

### How Authentication Works

```text
1. Bot starts with:
   - App ID (e.g., 2591456)
   - Private key (.pem file)

2. Bot creates JWT (JSON Web Token):
   - Signed with private key
   - Proves "I am app 123456"
   - Valid for 10 minutes

3. Bot discovers installation:
   GET /repos/alice/repo/installation
   Authorization: Bearer <JWT>
   Response: { "id": 789012 }

4. Bot exchanges JWT for installation token:
   POST /app/installations/789012/access_tokens
   Authorization: Bearer <JWT>
   Response: { "token": "ghs_...", "expires_at": "..." }

5. Bot uses installation token:
   - For all operations on alice/repo
   - Token scoped to that specific installation
   - Auto-refreshed when expired
```

### Installation Management

The bot maintains a cache of installation tokens per repository:

- **Discovery**: Automatically discovers installation IDs for repositories
- **Caching**: Caches installation tokens to avoid repeated API calls
- **Thread-safe**: Uses mutex locking for concurrent access
- **Auto-refresh**: Tokens are automatically refreshed when expired

## Component Architecture

### Core Services

```text
┌─────────────────────────────────────────────────────────────┐
│                     Main Application                         │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────────┐      ┌──────────────────┐            │
│  │ Jira Scanner     │      │ PR Feedback      │            │
│  │ Service          │      │ Scanner Service  │            │
│  └────────┬─────────┘      └────────┬─────────┘            │
│           │                         │                       │
│           └───────────┬─────────────┘                       │
│                       │                                     │
│                       ▼                                     │
│            ┌────────────────────┐                           │
│            │ Ticket Processor   │                           │
│            └─────────┬──────────┘                           │
│                      │                                      │
│         ┌────────────┼────────────┐                         │
│         │            │            │                         │
│         ▼            ▼            ▼                         │
│  ┌───────────┐ ┌──────────┐ ┌─────────┐                    │
│  │  Jira     │ │ GitHub   │ │   AI    │                    │
│  │ Service   │ │ Service  │ │ Service │                    │
│  └───────────┘ └──────────┘ └─────────┘                    │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

### Service Responsibilities

#### JiraService

- Search for tickets matching configured criteria
- Update ticket status (To Do → In Progress → In Review)
- Add comments and update custom fields
- Retrieve ticket details and assignee information

#### GitHubService

- Manage GitHub App authentication and installation tokens
- Clone repositories and manage Git operations
- Create and manage branches
- Commit and push changes (via API for verified commits)
- Create pull requests and manage PR comments

#### AIService

- Interface for AI providers (Claude, Gemini)
- Generate code changes based on ticket descriptions
- Process PR feedback and generate responses
- Retry logic for handling AI failures

#### TicketProcessor

- Orchestrates the end-to-end workflow
- Maps assignees to GitHub usernames
- Verifies fork existence and app installation
- Coordinates between Jira, GitHub, and AI services

#### Scanner Services

- **JiraIssueScannerService**: Periodically scans for new tickets
- **PRFeedbackScannerService**: Monitors tickets in review for PR comments
- Bot loop prevention and concurrent processing protection

### Configuration Model

Multi-project configuration with per-project settings:

```yaml
jira:
  projects:
    - project_keys: ["PROJ1", "PROJ2"]
      status_transitions:
        Bug:
          todo: "Open"
          in_progress: "In Progress"
          in_review: "Code Review"
      component_to_repo:
        frontend: https://github.com/org/frontend.git
        backend: https://github.com/org/backend.git
```

**Key features:**

- Project-specific status transitions per ticket type
- Component-to-repository mapping (case-sensitive)
- Optional Git PR field for storing PR URLs
- Per-project error handling configuration

## Security Considerations

### Private Key Protection

The GitHub App private key is the most sensitive credential:

✅ **Development**: Store outside git repo, use `chmod 600`
✅ **Production**: Use secret manager (Google Secret Manager, AWS Secrets Manager)
✅ **Containers**: Mount as read-only volume
❌ **Never**: Commit to git, share publicly, or log in plaintext

### Token Security

- **Short-lived**: Installation tokens expire after 1 hour
- **Auto-refresh**: Automatically refreshed by the SDK
- **Scoped**: Each token is scoped to a specific repository installation
- **In-memory**: Tokens are only kept in memory, never written to disk

### Principle of Least Privilege

The GitHub App requests only the minimum permissions needed:

- ✅ **Contents: Read & Write** - Clone, create branches, push commits
- ✅ **Pull Requests: Read & Write** - Create PRs, read/post comments
- ❌ **No Issues access** (unless explicitly needed)
- ❌ **No Admin access**
- ❌ **No Secrets access**
- ❌ **No Actions/Workflows access**

### Installation Isolation

Each fork has its own installation:

- If one installation is compromised, others are unaffected
- Users can uninstall the app from their fork anytime
- No cross-repository access beyond what's granted per installation

### Audit Trail

All bot actions are clearly attributed:

- Commits show as authored by `app-name[bot]`
- PRs created by `app-name[bot]`
- Comments posted by `app-name[bot]`
- Full separation from human developer actions

## Troubleshooting

### "GitHub App is not installed on {owner}/{repo}"

**Cause**: The app isn't installed on the repository.

**Solutions:**

1. **For main repo**: Admin must install the app (see [admin-setup.md](admin-setup.md))
2. **For developer fork**: Developer must install the app (see
   [contributor-setup.md](contributor-setup.md))
3. Verify the app is installed: Go to
   `https://github.com/{owner}/{repo}/settings/installations`

### "failed to get installation ID"

**Cause**: App ID or private key is incorrect, or the app was deleted/revoked.

**Solutions:**

1. Verify `JIRA_AI_GITHUB_APP_ID` matches the app ID from GitHub
2. Verify `JIRA_AI_GITHUB_BOT_USERNAME` matches the app name (WITHOUT `[bot]`
   suffix - it's added automatically)
3. Check the private key file exists and is readable
4. Verify the private key hasn't been revoked: Check app settings → Private keys
5. Test the private key: `openssl rsa -in key.pem -check -noout`

### "403 Forbidden" when creating PR

**Cause**: App doesn't have required permissions or isn't installed on main repo.

**Solutions:**

1. Check app permissions: `https://github.com/settings/apps/{app-name}` → Permissions
2. Ensure "Pull requests: Read & write" is enabled
3. Verify app is installed on the main repository (where PR will be created)
4. Check installation hasn't been suspended

### "404 Not Found" when pushing

**Cause**: Fork doesn't exist or GitHub username mapping is wrong.

**Solutions:**

1. Verify the fork exists: `https://github.com/{username}/{repo}`
2. Check `jira.assignee_to_github_username` mapping in config
3. Ensure the mapped username matches the actual GitHub account
4. Verify the repository is actually a fork (not a separate repo with same
   name)

### "No GitHub username mapping found for assignee"

**Cause**: Missing assignee mapping in configuration.

**Solution:**

Add to `config.yaml`:

```yaml
jira:
  assignee_to_github_username:
    "alice@company.com": alice
```

Or set environment variable:

```bash
export JIRA_AI_JIRA_ASSIGNEE_TO_GITHUB_USERNAME='{"alice@company.com":"alice"}'
```

### Rate Limiting

**Symptoms**: `API rate limit exceeded` errors

**Cause**: Too many API calls in a short period.

**GitHub App Rate Limits:**

- 5,000 requests per hour per installation
- 12,500 requests per hour for app-level endpoints

**Solutions:**

1. Increase `jira.interval_seconds` to reduce polling frequency
2. Check for inefficient API usage in logs
3. Verify installation token caching is working (should see "using cached
   token" logs)
4. Check rate limit status: `GET /rate_limit` with installation token

### Bot loop prevention

**Symptoms**: Bot responds to its own comments or other bots infinitely.

**Cause**: Bot doesn't recognize other bot usernames.

**Solution:**

Add bot usernames to config (without `[bot]` suffix):

```yaml
github:
  known_bot_usernames:
    - "github-actions"
    - "dependabot"
    - "renovate"
    - "coderabbitai"    # Add any code review bots your team uses
```

The bot automatically handles the `[bot]` suffix for GitHub App bots.

### AI generates no changes

**Symptoms**: Bot completes but PR has no file changes.

**Cause**: AI completed successfully but didn't modify any files.

**Solution:**

The bot automatically retries up to 5 times (configurable):

```yaml
ai:
  max_retries: 5              # Number of retry attempts if AI generates no changes
  retry_delay_seconds: 2      # Delay between retries
```

Note: Total retry time is constrained to 30 minutes maximum
(`max_retries × retry_delay_seconds ≤ 1800 seconds`).

If retries are exhausted, the ticket fails with an error message. Check:

1. Ticket description is clear and actionable
2. AI has sufficient context (repository structure, existing code)
3. AI service logs for errors or timeouts

## Rate Limits & Performance

### GitHub API Limits

**GitHub-imposed limits (not user-configurable):**

GitHub enforces the following rate limits for GitHub Apps:

- 5,000 requests/hour per installation (for repository-specific operations)
- 12,500 requests/hour for app-level calls (e.g., listing installations)
- Higher limits than user accounts with Personal Access Tokens

These limits cannot be changed. Users must work within these constraints.

**Best Practices for staying within limits:**

- Cache installation tokens (implemented by default)
- Batch operations where possible
- Monitor rate limit headers in responses
- Adjust scan intervals based on usage (increase `jira.interval_seconds` to reduce API calls)

### Concurrent Processing

The bot uses `sync.Map` to prevent duplicate processing:

- Each ticket is processed at most once concurrently
- Automatic cleanup when processing completes
- Safe for multiple scanner goroutines

### AI Service Timeouts

Configurable timeouts prevent indefinite hangs:

```yaml
claude:
  timeout: 300  # seconds (5 minutes)

gemini:
  timeout: 300  # seconds (5 minutes)
```

Adjust based on:

- Complexity of typical tickets
- AI service response times
- Network conditions

## Resources

- [GitHub Apps Documentation](https://docs.github.com/en/apps)
- [GitHub App Permissions](https://docs.github.com/en/rest/overview/permissions-required-for-github-apps)
- [go-github SDK](https://github.com/google/go-github) - Official Go SDK
- [ghinstallation](https://github.com/bradleyfalzon/ghinstallation) - GitHub
  App auth for Go

## Related Documentation

- **[Contributor Setup](contributor-setup.md)** - Quick guide for developers
- **[Admin Setup](admin-setup.md)** - How to create and configure the GitHub App
- **[Testing Setup](testing-setup.md)** - Testing in your environment
- **[Debugging Guide](debugging.md)** - Debugging the application
