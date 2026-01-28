# Testing Setup - Using GitHub App from Upstream Repository

This document describes how to configure your test environment once the upstream repository administrator has created the GitHub App and provided you with the credentials.

## What You'll Receive from the Administrator

The upstream repository administrator should provide:

1. **App ID** (a number, e.g., `2591456`)
2. **Private key file** (`.pem` file, e.g., `bugs-buddy.2025-01-02.private-key.pem`)
3. **App name** (WITHOUT `[bot]` suffix, e.g., `bugs-buddy-jira-ai-issue-solver`)
4. **Installation confirmation** (which repos the app is installed on)

## Step 1: Store the Private Key Securely

1. Create a directory for keys (if it doesn't exist):

   ```bash
   mkdir -p ~/keys
   chmod 700 ~/keys  # Only you can access this directory
   ```

2. Save the private key file:

   ```bash
   # Copy the .pem file provided by the admin to:
   ~/keys/jira-ai-issue-solver.private-key.pem

   # Set appropriate permissions
   chmod 600 ~/keys/jira-ai-issue-solver.private-key.pem
   ```

## Step 2: Update Your Configuration File

Update your `config.yaml` (e.g., `~/testing/config.yaml`) with the GitHub App credentials:

```yaml
github:
  # ===== GitHub App Authentication =====
  # Replace these values with what the admin provided
  app_id: 2591456  # ← Replace with actual App ID
  private_key_path: "/etc/jira-ai-issue-solver/bugs-buddy.private-key.pem"

  # ===== Bot Identity =====
  # Use the app name provided by admin (do NOT include [bot] suffix)
  # Note: Bot email is automatically constructed from app_id and bot_username
  bot_username: bugs-buddy-jira-ai-issue-solver  # ← Replace with actual app name

  target_branch: main
  pr_label: ai-pr

  # Bot loop prevention
  max_thread_depth: 5
  known_bot_usernames:
    - "github-actions[bot]"
    - "dependabot[bot]"
    - "renovate[bot]"
```

**Important**: Keep `private_key_path` as `/etc/jira-ai-issue-solver/...` - this is the path **inside the container**, not on your host.

## Step 3: Run the Container

Run the container with both the config file and private key mounted:

```bash
podman run -d --name jira-ai-solver -p 8081:8080 \
  -v ~/testing/config.yaml:/app/config.yaml:ro \
  -v ~/keys/bugs-buddy.private-key.pem:/etc/jira-ai-issue-solver/bugs-buddy.private-key.pem:ro \
  --replace jira-ai-issue-solver:latest
```

### Explanation

- `-v ~/testing/config.yaml:/app/config.yaml:ro` - Mount your config file
- `-v ~/keys/...pem:/etc/jira-ai-issue-solver/...pem:ro` - Mount the private key
- `:ro` - Mount as read-only for security
- `--replace` - Replace existing container with same name

## Step 4: Verify It's Working

1. Check the logs for successful initialization:

   ```bash
   podman logs jira-ai-solver 2>&1 | grep -i github
   ```

   You should see:

   ```text
   GitHub service initialized
   Starting Jira issue scanner service...
   Starting PR feedback scanner service...
   ```

   **NOT**:

   ```text
   GitHub configuration not provided - GitHub services will be disabled
   ```

2. Look for GitHub App authentication being used:

   ```bash
   podman logs jira-ai-solver 2>&1 | grep -i "installation"
   ```

   When the bot accesses a PR, you should see logs mentioning installation tokens.

## Step 5: Test with a Jira Ticket

1. Create a test Jira ticket in the configured project (e.g., EDM)
2. Set the status to the "todo" status configured in your config
3. Wait for the scanner to pick it up (check interval in config)
4. Monitor logs:

   ```bash
   podman logs -f jira-ai-solver
   ```

## Common Issues and Solutions

### Error: "GitHub App is not installed on {repo}"

**Problem**: The app isn't installed on the repository you're trying to access.

**Solution**:

- Verify with the admin which repositories the app is installed on
- Make sure your Jira ticket's component maps to a repository where the app IS installed
- The app needs to be installed on the **upstream** repository (where PRs are created), not just your fork

### Error: "could not read private key: permission denied"

**Problem**: The container can't read the private key file due to permissions.

**Solution**:

```bash
chmod 644 ~/keys/jira-ai-issue-solver.private-key.pem
```

Or run container as your user:

```bash
podman run -d --name jira-ai-solver -p 8081:8080 \
  --user $(id -u):$(id -g) \
  -v ~/testing/config.yaml:/app/config.yaml:ro \
  -v ~/keys/jira-ai-issue-solver.private-key.pem:/etc/jira-ai-issue-solver/jira-ai-issue-solver.private-key.pem:ro \
  --replace jira-ai-issue-solver:latest
```

### Error: "failed to get installation ID"

**Problem**: The App ID or private key is incorrect, or the app was deleted/revoked.

**Solution**:

- Verify the App ID matches what the admin provided
- Verify the bot_username exactly matches the app name (do NOT include `[bot]`
  suffix - it's added automatically)
- Contact the admin to verify the app still exists and the private key hasn't
  been revoked

## Security Best Practices

1. **Never commit the private key to git**:

   ```bash
   # Add to .gitignore
   echo "*.pem" >> .gitignore
   echo "keys/" >> .gitignore
   ```

2. **Keep keys in a secure location**:
   - Use `chmod 600` on the key file
   - Store in a directory with `chmod 700`
   - Don't share the key file in plain text channels

3. **Rotate keys periodically**:
   - Request a new private key from the admin
   - Update your deployment
   - Have admin revoke the old key

## Testing Checklist

Before considering the setup complete, verify:

- [ ] Container starts without errors
- [ ] Logs show "GitHub service initialized"
- [ ] Logs show scanner services starting
- [ ] No "GitHub App is not installed" errors for target repositories
- [ ] Bot can create PRs on test tickets
- [ ] Bot can read and respond to PR review comments

## Questions?

If you encounter issues:

1. Check the logs: `podman logs jira-ai-solver`
2. Verify your config matches the template above
3. Confirm with the admin that the app is installed on the correct repositories
4. Check that all credentials (App ID, bot name, email format) exactly match what was provided
