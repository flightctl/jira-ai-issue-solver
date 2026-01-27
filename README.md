# Jira AI Issue Solver

An AI-powered tool that automatically processes Jira issues and creates GitHub
pull requests with suggested solutions.

## Features

- **Automatic Issue Processing**: Scans Jira for new issues and processes them
  with AI
- **GitHub Integration**: Creates pull requests with AI-generated solutions
- **Multiple AI Providers**: Support for Claude and Gemini AI services
- **Configurable Workflows**: Customizable status transitions and repository
  mappings
- **PR Feedback Processing**: Handles pull request review feedback and updates
  Jira tickets

## Getting Started

Choose your path based on your role:

### 👤 For Contributors

**Want to work on Jira tickets assigned to you?**

→ **[Contributor Setup Guide](docs/contributor-setup.md)** _(2 minutes to set up!)_

Install the GitHub App on your fork and start getting AI-generated PRs for your
tickets.

### 🔧 For Administrators

**Setting up the bot for your organization?**

→ **[Admin Setup Guide](docs/admin-setup.md)**

Step-by-step instructions for creating and configuring the GitHub App.

### 📚 For the Curious

**Want to understand how it all works?**

→ **[Architecture & Workflow Guide](docs/architecture.md)**

Comprehensive guide explaining the fork-based workflow, GitHub App
authentication, and system architecture.

---

## Quick Start

### Prerequisites

- Go 1.24+
- Jira API access
- GitHub App credentials (App ID and private key) -
  see [docs/admin-setup.md](docs/admin-setup.md) for setup instructions
- Claude CLI or Gemini CLI (depending on your AI provider choice)

### Installation

1. Clone the repository:

    ```bash
    git clone <repository-url>
    cd jira-ai-issue-solver
    ```

2. Copy and configure the configuration file:

    ```bash
    cp config.example.yaml config.yaml
    # Edit config.yaml with your settings
    ```

3. Run the application:

   a. Using environment variables (recommended for containers):

      <!-- markdownlint-disable MD013 -->
      ```bash
      # Jira Configuration
      export JIRA_AI_JIRA_BASE_URL=your-jira-url
      export JIRA_AI_JIRA_USERNAME=your-username
      export JIRA_AI_JIRA_API_TOKEN=your-token
      export JIRA_AI_JIRA_INTERVAL_SECONDS=300
      
      # Jira Assignee to GitHub Username Mapping (REQUIRED for GitHub App workflow)
      # Maps Jira assignee emails to GitHub usernames for fork-based workflow
      export JIRA_AI_JIRA_ASSIGNEE_TO_GITHUB_USERNAME='{"alice@example.com":"alice","bob@example.com":"bob-github"}'
      
      # GitHub App Configuration (REQUIRED)
      # See docs/admin-setup.md for how to create a GitHub App
      export JIRA_AI_GITHUB_APP_ID=2591456
      export JIRA_AI_GITHUB_PRIVATE_KEY_PATH=/path/to/your-github-app-private-key.pem
      export JIRA_AI_GITHUB_BOT_USERNAME=bugs-buddy-jira-ai-issue-solver
      # Note: GITHUB_BOT_EMAIL is auto-constructed from APP_ID and BOT_USERNAME
      export JIRA_AI_GITHUB_TARGET_BRANCH=main
      export JIRA_AI_GITHUB_PR_LABEL=ai-pr
      export JIRA_AI_GITHUB_SSH_KEY_PATH=/path/to/ssh/key  # Optional: for commit signing
      
      # AI Configuration
      export JIRA_AI_AI_PROVIDER=claude
      export JIRA_AI_AI_GENERATE_DOCUMENTATION=true
      export JIRA_AI_AI_MAX_RETRIES=5
      export JIRA_AI_AI_RETRY_DELAY_SECONDS=2
      
      # Claude Configuration
      export JIRA_AI_CLAUDE_CLI_PATH=claude
      export JIRA_AI_CLAUDE_TIMEOUT=300
      export JIRA_AI_CLAUDE_DANGEROUSLY_SKIP_PERMISSIONS=true
      export JIRA_AI_CLAUDE_ALLOWED_TOOLS="Bash Edit"
      export JIRA_AI_CLAUDE_DISALLOWED_TOOLS=Python
      export JIRA_AI_CLAUDE_API_KEY=your-anthropic-api-key  # Required for headless/container environments
      
      # Or for Gemini
      export JIRA_AI_AI_PROVIDER=gemini
      export JIRA_AI_GEMINI_CLI_PATH=gemini
      export JIRA_AI_GEMINI_TIMEOUT=300
      export JIRA_AI_GEMINI_MODEL=gemini-2.5-pro
      export JIRA_AI_GEMINI_API_KEY=your-gemini-api-key
      
      go run main.go
      ```
      <!-- markdownlint-enable MD013 -->

      **Note:** Environment variables use the `JIRA_AI_` prefix. See
      `config.example.yaml` for all available configuration options and
      project-specific settings (status transitions, component mappings, etc.).

   a. Using config file:

      ```bash
      go run main.go -config config.yaml
      ```

## Configuration

See `config.example.yaml` for a complete configuration example. Key sections:

- **Jira**: API credentials, status transitions, and assignee-to-GitHub-username
  mapping
- **GitHub**: GitHub App credentials (App ID and private key) and bot settings
- **AI Provider**: Choose between Claude or Gemini, with retry configuration
- **Component Mapping**: Map Jira components to GitHub repositories (per-project
  configuration)

### GitHub App Setup

This tool uses GitHub App authentication (not Personal Access Tokens). To set
up a GitHub App:

1. See [docs/admin-setup.md](docs/admin-setup.md) for step-by-step
   instructions on creating a GitHub App
2. Each developer must install the app on their fork - see
   [docs/contributor-setup.md](docs/contributor-setup.md) for quick setup or
   [docs/architecture.md](docs/architecture.md) for detailed workflow explanation
3. Configure assignee-to-GitHub-username mapping so the bot knows which fork to
   use for each ticket assignee

### Status Transitions

The tool supports configurable status transitions for different ticket types.
You must define specific status transitions for each ticket type (e.g., Bug,
Story, Task) that you want the tool to process.

#### Simple Configuration (Backward Compatible)

```yaml
jira:
  status_transitions:
    todo: "To Do"
    in_progress: "In Progress"
    in_review: "In Review"
```

#### Ticket-Type-Specific Configuration

```yaml
jira:
  status_transitions:
    # Specific transitions for Bug tickets
    Bug:
      todo: "Open"
      in_progress: "In Progress"
      in_review: "Code Review"
    
    # Specific transitions for Story tickets
    Story:
      todo: "Backlog"
      in_progress: "Development"
      in_review: "Testing"
    
    # Specific transitions for Task tickets
    Task:
      todo: "To Do"
      in_progress: "In Progress"
      in_review: "Review"
```

The tool will automatically detect the ticket type from the Jira issue and use
the appropriate status transitions. **All ticket types that you want the tool
to process must be explicitly configured.** The tool will generate JQL queries
that search for tickets across all configured ticket types and their respective
statuses.

### Documentation Generation

The tool can automatically generate documentation files (`CLAUDE.md` or
`GEMINI.md`) in repositories when processing tickets. These files serve as
indexes to all markdown documentation in the repository.

#### Configuration

<!-- markdownlint-disable MD013 -->
```yaml
ai:
  generate_documentation: true  # Set to false to disable documentation generation (CLAUDE.md or GEMINI.md)
```
<!-- markdownlint-enable MD013 -->

#### Environment Variables

```bash
export AI_GENERATE_DOCUMENTATION=true
```

When enabled, the tool will:

- Check if the documentation file already exists
- If not, generate a comprehensive index of all markdown files in the repository
- Include links to existing documentation rather than duplicating content
- Organize the documentation with a table of contents and logical sections

This feature is enabled by default but can be disabled by setting the
configuration option to `false`.

### AI Retry Configuration

The tool includes automatic retry logic when AI code generation doesn't produce
changes. This helps handle cases where the AI needs multiple attempts to
understand the task or generate valid code.

#### Configuration

```yaml
ai:
  max_retries: 5              # Maximum retry attempts (1-10, default: 5)
  retry_delay_seconds: 2      # Delay between retries (0-300, default: 2)
```

#### Environment Variables

```bash
export JIRA_AI_AI_MAX_RETRIES=5
export JIRA_AI_AI_RETRY_DELAY_SECONDS=2
```

**Retry Behavior:**

- The bot retries if the AI generates no file changes
- Total retry time is limited to prevent excessive waits (max 30 minutes total)
- Each retry uses the same prompt to give the AI another chance
- If all retries are exhausted without changes, the ticket processing fails

### Claude API Key for Headless Environments

When running in containers or headless environments (Cloud Run, Kubernetes,
etc.), Claude CLI requires an API key for authentication instead of interactive
login.

#### Configuration

```yaml
claude:
  api_key: "sk-ant-api03-..."  # Get from https://console.anthropic.com/settings/keys
```

#### Environment Variables

```bash
export JIRA_AI_CLAUDE_API_KEY=sk-ant-api03-...
```

**Note:** Without an API key in headless environments, Claude CLI will fail with
authentication errors.

### Assignee to GitHub Username Mapping

**REQUIRED**: The GitHub App workflow requires mapping Jira assignees to GitHub
usernames. This enables the bot to push code to the correct developer's fork.

#### Configuration

```yaml
jira:
  assignee_to_github_username:
    "alice@yourcompany.com": alice
    "bob.smith@yourcompany.com": bob-github
```

**Note:** Email addresses must be quoted to avoid YAML parsing errors.

#### Environment Variables

xxx
<!-- markdownlint-disable MD013 -->
```bash
# For environment variables, use JSON format:
export JIRA_AI_JIRA_ASSIGNEE_TO_GITHUB_USERNAME='{"alice@example.com":"alice","bob@example.com":"bob"}'
```
<!-- markdownlint-enable MD013 -->

**Workflow:**

1. Ticket is assigned to a user in Jira (e.g., <alice@yourcompany.com>)
2. Bot maps assignee email to GitHub username (alice)
3. Bot discovers alice's fork (alice/repository)
4. Bot pushes code to alice's fork
5. Bot creates PR from alice's fork to main repository
6. Alice can collaborate by pushing to the same branch in her fork

## Debugging

The project includes comprehensive debugging support:

### Quick Debug Start

```bash
# Interactive debug script
./debug.sh

# Or use make commands
make debug          # Debug with main config
make debug-tests    # Debug tests
```

### VS Code Debugging

1. Open the project in VS Code
2. Install the Go extension
3. Press `F5` or use the Run and Debug panel
4. Select a debug configuration:
   - **Debug Jira AI Issue Solver** (main config)
   - **Debug Tests** (run tests in debug mode)

### Command Line Debugging

```bash
# Direct Delve commands
dlv debug main.go -- -config config.yaml
dlv test ./... -- -v

# Common Delve commands
break main.main    # Set breakpoint at main function
break services/    # Set breakpoint in services package
continue (c)       # Continue execution
next (n)           # Step over
step (s)           # Step into
print variable     # Print variable value
vars               # Show variables
goroutines         # Show goroutines
quit (q)           # Exit debugger
```

### Debugging Specific Components

```bash
# Jira service
break services.NewJiraService
break services.(*JiraService).GetIssue

# GitHub service
break services.NewGitHubService
break services.(*GitHubService).CreatePullRequest

# AI service
break services.(*ClaudeService).ProcessIssue
break services.(*GeminiService).ProcessIssue

# Scanner services
break services.(*JiraIssueScannerService).Start
break services.(*PRFeedbackScannerService).Start
```

For detailed debugging information, see [docs/debugging.md](docs/debugging.md).

## Development

### Running Tests

```bash
go test ./...
go test -v ./...
```

### Building

```bash
go build -o jira-ai-issue-solver main.go
```

### Docker/Podman

The container is designed to be secure and uses environment variables by default
(no config files baked into the image).

```bash
# Build container
make build

# Run container with environment variables (recommended)
podman run -d \
  -p 8080:8080 \
  -v /path/to/github-app-key.pem:/app/github-app-key.pem:ro \
  -e JIRA_AI_JIRA_BASE_URL=your-jira-url \
  -e JIRA_AI_JIRA_USERNAME=your-username \
  -e JIRA_AI_JIRA_API_TOKEN=your-token \
  -e JIRA_AI_JIRA_INTERVAL_SECONDS=300 \
  -e JIRA_AI_JIRA_ASSIGNEE_TO_GITHUB_USERNAME='{"alice@example.com":"alice"}' \
  -e JIRA_AI_GITHUB_APP_ID=2591456 \
  -e JIRA_AI_GITHUB_PRIVATE_KEY_PATH=/app/github-app-key.pem \
  -e JIRA_AI_GITHUB_BOT_USERNAME=bugs-buddy-jira-ai-issue-solver \
  -e JIRA_AI_GITHUB_TARGET_BRANCH=main \
  -e JIRA_AI_GITHUB_PR_LABEL=ai-pr \
  -e JIRA_AI_AI_PROVIDER=claude \
  -e JIRA_AI_CLAUDE_CLI_PATH=claude \
  -e JIRA_AI_CLAUDE_TIMEOUT=300 \
  -e JIRA_AI_CLAUDE_API_KEY=your-anthropic-api-key \
  jira-ai-issue-solver:latest

# Or use make commands
make run

# Stop container
make stop

# View logs
make logs
```

**Notes**:

- The container uses environment variables by default with the `JIRA_AI_` prefix
- Mount the GitHub App private key file as a read-only volume
- For project-specific configuration (status transitions, component mappings),
  use a config file:

  ```bash
  podman run -d \
    -p 8080:8080 \
    -v /path/to/github-app-key.pem:/app/github-app-key.pem:ro \
    -v ./config.yaml:/app/config.yaml:ro \
    jira-ai-issue-solver:latest --config=/app/config.yaml
  ```

### Cloud Run Deployment

The project includes a comprehensive deployment system for Google Cloud Run that
automatically handles secrets management and environment configuration.

#### Prerequisites

- Google Cloud CLI (`gcloud`) installed and authenticated
- `yq` command-line tool for YAML parsing
- A `config.yaml` file with your configuration

#### Quick Deployment

1. **Set up your configuration:**

   ```bash
   cp config.example.yaml config.yaml
   # Edit config.yaml with your settings
   ```

2. **Deploy to Cloud Run:**

   ```bash
   make deploy PROJECT_ID="your-gcp-project-id" REGION="your-region" SERVICE_NAME="your-service-name"
   ```

   Example:

   ```bash
   make deploy PROJECT_ID="my-awesome-project" REGION="us-central1" SERVICE_NAME="jira-ai-solver"
   ```

3. **Alternative: Use the example script:**

   ```bash
   # Edit deploy-example.sh with your configuration
   ./deploy-example.sh
   ```

#### What the Deployment Does

The deployment process automatically:

- ✅ Creates Google Cloud Secret Manager secrets from your `config.yaml`
- ✅ Parses all configuration values as environment variables
- ✅ Deploys the container to Cloud Run with proper IAM permissions
- ✅ Runs health checks to verify the deployment
- ✅ Provides monitoring commands for logs and service status

#### Security Features

- 🔒 Automatically creates secrets from `config.yaml` (no hardcoded secrets)
- 🔒 Validates that `config.yaml` is git-ignored before deployment
- 🔒 Uses Google Cloud Secret Manager for sensitive data
- 🔒 No authentication required for the service (internal use)

#### Monitoring Your Deployment

After deployment, you can monitor your service:

<!-- markdownlint-disable MD013 -->
```bash
# Get service URL
gcloud run services describe your-service-name --region=your-region --format="value(status.url)"

# View logs
gcloud beta logging tail "resource.type=cloud_run_revision AND resource.labels.service_name=your-service-name" --project=your-project-id

# Check service status
gcloud run services describe your-service-name --region=your-region
```
<!-- markdownlint-enable MD013 -->

#### Deployment Options

The deployment supports several options:

```bash
# Skip creating/updating secrets (if already done)
make deploy PROJECT_ID="..." REGION="..." SERVICE_NAME="..." -- --skip-secrets

# Skip the actual deployment (just create secrets)
make deploy PROJECT_ID="..." REGION="..." SERVICE_NAME="..." -- --skip-deploy

# Skip testing the deployment
make deploy PROJECT_ID="..." REGION="..." SERVICE_NAME="..." -- --skip-test
```

## Architecture

The application consists of several key services:

- **JiraService**: Handles Jira API interactions
- **GitHubService**: Manages GitHub repository operations
- **AIService**: Interface for AI providers (Claude/Gemini)
- **JiraIssueScannerService**: Periodically scans Jira for new issues
- **PRFeedbackScannerService**: Processes PR review feedback

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

[Add your license information here]
