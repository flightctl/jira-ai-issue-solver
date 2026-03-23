# Testing Setup

This document describes how to deploy and test the bot after an
administrator has created the GitHub App and provided credentials.

## What You'll Receive from the Administrator

1. **App ID** (a number, e.g., `2591456`)
2. **Private key file** (`.pem` file)
3. **App name** (without `[bot]` suffix, e.g., `bugs-buddy-jira-ai-issue-solver`)
4. **Installation confirmation** (which repos the app is installed on)

## Step 1: Store the Private Key Securely

```bash
mkdir -p ~/keys
chmod 700 ~/keys

# Copy the .pem file to a secure location
cp /path/to/provided.pem ~/keys/github-app.private-key.pem
chmod 600 ~/keys/github-app.private-key.pem
```

## Step 2: Create Your Configuration File

Create `config.yaml` with all required sections. See
[config.example.yaml](../config.example.yaml) for the complete reference.

Minimal working configuration:

```yaml
jira:
  base_url: https://your-domain.atlassian.net
  username: your-jira-username
  api_token: your-jira-api-token
  interval_seconds: 300
  assignee_to_github_username:
    "alice@yourcompany.com": alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        Bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        backend: https://github.com/your-org/backend.git

github:
  app_id: 2591456                    # From administrator
  private_key_path: "/etc/bot/github-app.private-key.pem"  # Path INSIDE the container
  bot_username: bugs-buddy           # App name, no [bot] suffix
  target_branch: main
  pr_label: ai-pr
  max_thread_depth: 5
  # Do NOT include [bot] suffixes — they are handled automatically
  known_bot_usernames:
    - "github-actions"
    - "dependabot"
    - "coderabbitai"

ai_provider: claude

claude:
  api_key: "sk-ant-api03-..."       # From https://console.anthropic.com/settings/keys

workspaces:
  base_dir: /var/lib/ai-bot/workspaces
  ttl_days: 7

container:
  runtime: auto
  default_image: "mcr.microsoft.com/devcontainers/universal:2"
  resource_limits:
    memory: "8g"
    cpus: "4"

guardrails:
  max_concurrent_jobs: 5
  max_retries: 3
  max_daily_cost_usd: 50.0
  max_container_runtime_minutes: 60
```

## Step 3: Run the Container

Run with the config file, private key, and workspace volume mounted:

```bash
podman run -d --name ai-bot -p 8080:8080 \
  -v ~/config.yaml:/app/config.yaml:ro \
  -v ~/keys/github-app.private-key.pem:/etc/bot/github-app.private-key.pem:ro \
  -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \
  --replace jira-ai-issue-solver:latest
```

- `:ro` — mount config and key as read-only for security
- The workspace volume must be read-write (the bot clones repos there)
- The `private_key_path` in config must match the container mount path

## Step 4: Verify Startup

Check the logs for successful initialization:

```bash
podman logs ai-bot 2>&1 | head -20
```

You should see:

```text
Container runtime detected    {"runtime": "podman", "path": "/usr/bin/podman"}
Scanners started
Starting server               {"port": 8080}
```

Check for errors:

```bash
# Look for fatal errors
podman logs ai-bot 2>&1 | grep -i "fatal\|error"

# Verify health endpoint
curl http://localhost:8080/health
# Should return: OK
```

## Step 5: Test with a Jira Ticket

1. Create a test ticket in the configured project (e.g., PROJ1)
2. Set the **Components** field to match a `component_to_repo` key (e.g., "backend")
3. Set the ticket status to the configured "todo" status (e.g., "To Do")
4. Add the bot's Jira username as a contributor on the ticket
5. Wait for the scanner to pick it up (up to `interval_seconds`)
6. Monitor progress:

```bash
podman logs -f ai-bot
```

## Common Issues and Solutions

### Error: "GitHub App is not installed on {repo}"

The app isn't installed on the repository.

- Verify with the admin which repositories the app is installed on
- The app needs to be installed on both the **upstream** repo (for PR creation)
  and the **developer's fork** (for pushing code)

### Error: "could not read private key: permission denied"

The container can't read the private key file.

```bash
# Fix host permissions
chmod 644 ~/keys/github-app.private-key.pem

# Or run container as your user
podman run -d --name ai-bot -p 8080:8080 \
  --user $(id -u):$(id -g) \
  -v ~/config.yaml:/app/config.yaml:ro \
  -v ~/keys/github-app.private-key.pem:/etc/bot/github-app.private-key.pem:ro \
  -v /var/lib/ai-bot/workspaces:/var/lib/ai-bot/workspaces \
  --replace jira-ai-issue-solver:latest
```

### Error: "failed to get installation ID"

App ID or private key is incorrect.

- Verify `app_id` matches what the admin provided
- Verify `bot_username` matches the app name (without `[bot]` suffix)
- Contact admin to verify the app still exists and the key hasn't been revoked

### Error: "Failed to detect container runtime"

No container runtime (podman or docker) is available.

```bash
# Check if podman is installed
podman --version

# Or docker
docker --version
```

The bot needs a container runtime to launch AI agent containers. Install
podman (preferred) or docker on the host.

### Tickets not being picked up

1. Ticket **Components** field must match a `component_to_repo` key (case-sensitive)
2. Bot's Jira username must be a contributor on the ticket
3. Ticket must be in the configured "todo" status
4. Check `jira.assignee_to_github_username` has the assignee's mapping

## Security Best Practices

1. **Never commit the private key to git**:
   ```bash
   echo "*.pem" >> .gitignore
   echo "keys/" >> .gitignore
   ```

2. **Keep keys in a secure location**: `chmod 600` on key files, `chmod 700` on key directories

3. **Use read-only mounts**: Mount config and keys with `:ro` in the container

4. **Rotate keys periodically**: Request a new key from admin, update deployment, have admin revoke the old key

## Testing Checklist

- [ ] Container starts without fatal errors
- [ ] Health endpoint returns OK (`curl http://localhost:8080/health`)
- [ ] Logs show "Container runtime detected" and "Scanners started"
- [ ] No "GitHub App is not installed" errors for target repositories
- [ ] Bot picks up a test ticket and transitions it to "In Progress"
- [ ] Bot creates a PR with AI-generated changes
- [ ] Bot transitions ticket to "In Review" and posts PR URL
- [ ] Bot responds to PR review comments (feedback loop)

## Questions?

1. Check logs: `podman logs ai-bot`
2. Verify config matches [config.example.yaml](../config.example.yaml)
3. Confirm with admin that the app is installed on correct repositories
