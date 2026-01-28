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

   ```bash
   # Using config file (recommended for local development)
   go run main.go -config config.yaml

   # Using environment variables (recommended for containers)
   # All config.yaml options can be set via JIRA_AI_* env vars
   # See config.example.yaml for full list
   export JIRA_AI_JIRA_BASE_URL=your-jira-url
   export JIRA_AI_JIRA_USERNAME=your-username
   export JIRA_AI_JIRA_API_TOKEN=your-token
   export JIRA_AI_GITHUB_APP_ID=2591456
   export JIRA_AI_GITHUB_PRIVATE_KEY_PATH=/path/to/key.pem
   # ... (see config.example.yaml for all options)
   go run main.go
   ```

## Configuration

See **[config.example.yaml](config.example.yaml)** for complete configuration reference with detailed comments.

### Key Configuration Areas

- **Jira**: API credentials, project settings, status transitions
- **GitHub**: GitHub App credentials (App ID and private key)
- **AI Provider**: Claude or Gemini configuration
- **Assignee Mapping**: Map Jira emails to GitHub usernames (required for fork workflow)

### Important Notes

- **GitHub App authentication**: See [docs/admin-setup.md](docs/admin-setup.md) for creating the GitHub App
- **Contributor setup**: Developers must install the app on their forks - see [docs/contributor-setup.md](docs/contributor-setup.md)
- **Status transitions**: Configure per ticket type - see config.example.yaml for examples
- **Headless environments**: Claude requires API key for containers/Cloud Run

For detailed configuration explanations, see [docs/architecture.md](docs/architecture.md).

## Debugging

Quick start:

```bash
# Interactive debug script
./debug.sh

# Or use make commands
make debug          # Debug with main config
make debug-tests    # Debug tests
```

For VS Code debugging, breakpoint locations, and detailed troubleshooting, see **[docs/debugging.md](docs/debugging.md)**.

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

```bash
# Build container
make build

# Run with config file
make run

# Or run manually with environment variables
podman run -d -p 8080:8080 \
  -v /path/to/github-app-key.pem:/app/github-app-key.pem:ro \
  -v ./config.yaml:/app/config.yaml:ro \
  jira-ai-issue-solver:latest --config=/app/config.yaml

# View logs
make logs

# Stop container
make stop
```

See config.example.yaml for all environment variable options (use `JIRA_AI_*` prefix).

### Cloud Run Deployment

Quick deployment to Google Cloud Run with automatic secrets management:

```bash
# Configure your settings
cp config.example.yaml config.yaml
# Edit config.yaml with your settings

# Deploy (requires gcloud CLI and yq)
make deploy PROJECT_ID="your-project-id" REGION="us-central1" SERVICE_NAME="jira-ai-solver"
```

The deployment script automatically creates secrets from config.yaml and deploys to Cloud Run. See `deploy.sh` for options and `deploy-example.sh` for a full example.

## Architecture

For detailed architecture, component design, and workflow documentation, see **[docs/architecture.md](docs/architecture.md)**.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

[Add your license information here]
