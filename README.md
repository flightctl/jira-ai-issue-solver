# Jira AI Issue Solver

An AI-powered tool that automatically processes Jira issues and creates GitHub pull requests with suggested solutions.

## Features

- **Automatic Issue Processing**: Scans Jira for new issues and processes them with AI
- **GitHub Integration**: Creates pull requests with AI-generated solutions
- **Multiple AI Providers**: Support for Claude and Gemini AI services
- **Configurable Workflows**: Customizable status transitions and repository mappings
- **PR Feedback Processing**: Handles pull request review feedback and updates Jira tickets

## Quick Start

### Prerequisites

- Go 1.24+
- Jira API access
- GitHub Personal Access Token
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

**Option A: Using environment variables (recommended for containers):**
```bash
# Jira Configuration
export JIRA_BASE_URL=your-jira-url
export JIRA_USERNAME=your-username
export JIRA_API_TOKEN=your-token
export JIRA_GIT_PULL_REQUEST_FIELD_NAME="Git Pull Request"
export JIRA_INTERVAL_SECONDS=300
export JIRA_DISABLE_ERROR_COMMENTS=false
export JIRA_STATUS_TRANSITIONS_TODO="To Do"
export JIRA_STATUS_TRANSITIONS_IN_PROGRESS="In Progress"
export JIRA_STATUS_TRANSITIONS_IN_REVIEW="In Review"

# GitHub Configuration
export GITHUB_PERSONAL_ACCESS_TOKEN=your-github-token
export GITHUB_BOT_USERNAME=your-bot-username
export GITHUB_BOT_EMAIL=your-bot-email
export GITHUB_TARGET_BRANCH=main
export GITHUB_PR_LABEL=ai-pr
export GITHUB_SSH_KEY_PATH=/path/to/ssh/key

# AI Configuration
export AI_PROVIDER=claude
export AI_GENERATE_DOCUMENTATION=true
export CLAUDE_CLI_PATH=claude
export CLAUDE_TIMEOUT=300
export CLAUDE_DANGEROUSLY_SKIP_PERMISSIONS=false
export CLAUDE_ALLOWED_TOOLS="Bash Edit"
export CLAUDE_DISALLOWED_TOOLS=Python

# Or for Gemini
export AI_PROVIDER=gemini
export AI_GENERATE_DOCUMENTATION=true
export GEMINI_CLI_PATH=gemini
export GEMINI_TIMEOUT=300
export GEMINI_MODEL=gemini-2.5-pro
export GEMINI_API_KEY=your-gemini-api-key

# Component Mapping
export JIRA_AI_COMPONENT_TO_REPO="component1=repo1,component2=repo2"

go run main.go
```

**Option B: Using config file:**
```bash
go run main.go -config config.yaml
```

## Configuration

See `config.example.yaml` for a complete configuration example. Key sections:

- **Jira**: API credentials and status transitions
- **GitHub**: Personal access token and bot settings
- **AI Provider**: Choose between Claude or Gemini
- **Component Mapping**: Map Jira components to GitHub repositories

### Status Transitions

The tool supports configurable status transitions for different ticket types. You must define specific status transitions for each ticket type (e.g., Bug, Story, Task) that you want the tool to process.

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

The tool will automatically detect the ticket type from the Jira issue and use the appropriate status transitions. **All ticket types that you want the tool to process must be explicitly configured.** The tool will generate JQL queries that search for tickets across all configured ticket types and their respective statuses.

### Documentation Generation

The tool can automatically generate documentation files (`CLAUDE.md` or `GEMINI.md`) in repositories when processing tickets. These files serve as indexes to all markdown documentation in the repository.

#### Configuration
```yaml
ai:
  generate_documentation: true  # Set to false to disable documentation generation (CLAUDE.md or GEMINI.md)
```

#### Environment Variables
```bash
export AI_GENERATE_DOCUMENTATION=true
```

When enabled, the tool will:
- Check if the documentation file already exists
- If not, generate a comprehensive index of all markdown files in the repository
- Include links to existing documentation rather than duplicating content
- Organize the documentation with a table of contents and logical sections

This feature is enabled by default but can be disabled by setting the configuration option to `false`.

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

For detailed debugging information, see [DEBUGGING.md](DEBUGGING.md).

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

The container is designed to be secure and uses environment variables by default (no config files baked into the image).

```bash
# Build container
make build

# Run container with environment variables (recommended)
podman run -d \
  -p 8080:8080 \
  -e JIRA_BASE_URL=your-jira-url \
  -e JIRA_USERNAME=your-username \
  -e JIRA_API_TOKEN=your-token \
  -e JIRA_GIT_PULL_REQUEST_FIELD_NAME="Git Pull Request" \
  -e JIRA_INTERVAL_SECONDS=300 \
  -e JIRA_DISABLE_ERROR_COMMENTS=false \
  -e JIRA_STATUS_TRANSITIONS_TODO="To Do" \
  -e JIRA_STATUS_TRANSITIONS_IN_PROGRESS="In Progress" \
  -e JIRA_STATUS_TRANSITIONS_IN_REVIEW="In Review" \
  -e GITHUB_PERSONAL_ACCESS_TOKEN=your-github-token \
  -e GITHUB_BOT_USERNAME=your-bot-username \
  -e GITHUB_BOT_EMAIL=your-bot-email \
  -e GITHUB_TARGET_BRANCH=main \
  -e GITHUB_PR_LABEL=ai-pr \
  -e AI_PROVIDER=claude \
  -e CLAUDE_CLI_PATH=claude \
  -e CLAUDE_TIMEOUT=300 \
  -e JIRA_AI_COMPONENT_TO_REPO="component1=repo1,component2=repo2" \
  jira-ai-issue-solver:latest

# Or use make commands
make run

# Stop container
make stop

# View logs
make logs
```

**Note**: The container uses environment variables by default. If you need to use a config file, mount it and use the `--config` flag:
```bash
podman run -d \
  -p 8080:8080 \
  -v ./config.yaml:/app/config.yaml:ro \
  jira-ai-issue-solver:latest --config=/app/config.yaml
```

### Cloud Run Deployment

The project includes a comprehensive deployment system for Google Cloud Run that automatically handles secrets management and environment configuration.

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

```bash
# Get service URL
gcloud run services describe your-service-name --region=your-region --format="value(status.url)"

# View logs
gcloud beta logging tail "resource.type=cloud_run_revision AND resource.labels.service_name=your-service-name" --project=your-project-id

# Check service status
gcloud run services describe your-service-name --region=your-region
```

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
