# Jira AI Issue Solver

A Go application that automatically processes Jira tickets labeled with "good-for-ai" by using Claude CLI to generate code changes and create pull requests.

## Features

- **Periodic Ticket Scanning**: Automatically scans for tickets with the "good-for-ai" label at configurable intervals
- **AI-Powered Code Generation**: Uses Claude CLI to analyze tickets and generate code changes
- **GitHub Integration**: Creates forks, branches, and pull requests automatically
- **Jira Integration**: Updates ticket status and adds comments with PR links
- **Status Management**: Automatically manages ticket status transitions during processing
- **PR Feedback Processing**: Automatically processes PR review feedback and applies fixes

## How It Works

### Periodic Scanning

The service runs two periodic scanners:

#### 1. Ticket Processing Scanner
This scanner processes new tickets:

1. Searches for Jira tickets where the configured Jira user is set as a contributor that are in the configured "todo" status
2. Processes each ticket by updating status to "In Progress"
3. Forks the repository associated with the ticket to the bot's GitHub account
4. Clones the forked repository and creates a new branch
5. Uses Claude CLI to generate code changes based on the ticket description and comments
6. Commits the changes and pushes the branch to the forked repository
7. Creates a Pull Request from the bot's fork to the original repository
8. Adds a comment to the ticket with a link to the PR and updates the ticket status to "In Review"

#### 2. PR Feedback Scanner
This scanner processes PR review feedback:

1. Searches for Jira tickets assigned to the current user that are in "In Review" status and have a PR URL set
2. Checks the associated GitHub PR for "request changes" reviews
3. Collects all feedback from reviews and comments
4. Creates a new branch with fixes based on the feedback
5. Generates a new PR with the applied fixes
6. Updates the original ticket with the new PR information

### Configuration

The scanner interval can be configured using the `SCANNER_INTERVAL_SECONDS` environment variable (default: 300 seconds).

## Installation

### Prerequisites

- Go 1.21 or later
- Claude CLI installed and configured
- GitHub App with appropriate permissions
- Jira API access

### Setup

1. Clone the repository:
```bash
git clone <repository-url>
cd jira-ai-issue-solver
```

2. Install dependencies:
```bash
go mod download
```

3. Create a configuration file (optional) or set environment variables:
```bash
cp config.example.env .env
# Edit .env with your configuration
```

4. Build the application:
```bash
go build -o jira-ai-solver
```

## Configuration

The application uses a YAML configuration file. Copy `config.example.yaml` to `config.yaml` and update the values:

### Configuration File

Create a `config.yaml` file with the following structure:

```yaml
# Server Configuration
server:
  port: 8080

# Jira Configuration
jira:
  base_url: https://your-domain.atlassian.net
  username: your-username
  api_token: your-jira-api-token
  interval_seconds: 300
  disable_error_comments: false
  git_pull_request_field_name: "Git Pull Request"  # Required for PR feedback processing
  status_transitions:
    todo: "To Do"
    in_progress: "In Progress"
    in_review: "In Review"

# GitHub Configuration
github:
  personal_access_token: your-personal-access-token-here
  bot_username: your-org-ai-bot
  bot_email: ai-bot@your-org.com
  target_branch: main

# Claude CLI Configuration
claude:
  cli_path: claude-cli
  timeout: 300
  dangerously_skip_permissions: true
  allowed_tools: "Bash Edit"
  disallowed_tools: "Python"

# Scanner Configuration
scanner:
  interval_seconds: 300

# Component to Repository Mapping
component_to_repo:
  frontend: https://github.com/your-org/frontend.git
  backend: https://github.com/your-org/backend.git
  api: https://github.com/your-org/api.git

# Temporary Directory
temp_dir: /tmp/jira-ai-issue-solver

# Jira Configuration
jira_config:
  disable_error_comments: false
```

### Jira Configuration

The `jira` section contains Jira-specific settings:

- `base_url`: Your Jira instance URL
- `username`: Your Jira username
- `api_token`: Your Jira API token
- `interval_seconds`: How often to scan for new tickets (default: 300 seconds)
- `disable_error_comments`: When set to `true`, prevents the application from adding error comments to Jira tickets when processing fails. Useful for testing or to avoid spamming tickets with error messages.
- `status_transitions`: Configuration for ticket status transitions during processing
  - `todo`: Status name for tickets ready for AI processing (default: "To Do")
  - `in_progress`: Status name to set when AI starts processing (default: "In Progress")
  - `in_review`: Status name to set when PR is created (default: "In Review")

### GitHub Configuration

The `github` section contains GitHub-specific settings:

- `personal_access_token`: Your GitHub Personal Access Token
- `bot_username`: The username of the GitHub bot account
- `bot_email`: The email address for the GitHub bot account
- `target_branch`: The target branch for pull requests (default: "main"). This allows you to create PRs against a specific branch for testing purposes. For example, you can set this to "develop" or "staging" to test changes before merging to main.
- `ssh_key_path`: Path to SSH private key for commit signing (optional). When set, commits will be signed using SSH keys for enhanced security.

#### SSH Commit Signing Setup

To enable SSH commit signing for the bot:

1. **Generate SSH Key for Signing** (separate from authentication keys):
   ```bash
   ssh-keygen -t ed25519 -C "bot@your-org.com" -f ~/.ssh/git_signing_key
   ```

2. **Add Public Key to GitHub**:
   - Copy the public key: `cat ~/.ssh/git_signing_key.pub`
   - Go to GitHub Settings → SSH and GPG keys → New SSH key
   - Paste the public key and give it a descriptive name like "Bot Signing Key"

3. **Configure in Bot**:
   ```yaml
   github:
     ssh_key_path: "/path/to/git_signing_key"
   ```

4. **Verify Signing**:
   ```bash
   git log --show-signature
   ```

**Note**: SSH signing requires Git 2.34+ and is the recommended approach for modern Git workflows.

### Component Mapping

The application uses a component-to-repository mapping to determine which repository to use for each ticket:

```yaml
component_to_repo:
  frontend: https://github.com/your-org/frontend.git
  backend: https://github.com/your-org/backend.git
  api: https://github.com/your-org/api.git
```

The application will:
1. Look at the first component assigned to a Jira ticket
2. Use the component name to find the corresponding repository URL
3. Process the ticket using that repository

### PR Feedback Processing

The application includes automatic PR feedback processing functionality:

#### Setup Requirements

1. **Custom Field Configuration**: You must configure a custom field in Jira to store the PR URL:
   ```yaml
   jira:
     git_pull_request_field_name: "Git Pull Request"  # Replace with your actual field name
   ```

2. **Field ID Discovery**: To find your custom field ID:
   - Go to Jira Administration → Issues → Custom fields
   - Find your PR URL field and note the field ID (e.g., `customfield_10001`)

#### How PR Feedback Processing Works

1. **Automatic Detection**: The scanner automatically detects tickets in "In Review" status that have a PR URL set
2. **Review Analysis**: It checks the GitHub PR for any "request changes" reviews
3. **Feedback Collection**: All feedback from reviews and comments is collected
4. **AI-Powered Fixes**: The AI service analyzes the feedback and generates code fixes
5. **Direct PR Update**: Changes are pushed directly to the existing PR branch, updating the original PR
6. **Automatic Updates**: The original PR is automatically updated with the feedback fixes

#### Supported Feedback Types

- **Review Comments**: Comments from PR reviews with "request changes" status
- **Line Comments**: Inline comments on specific code lines
- **General Comments**: General PR comments
- **File Changes**: Information about which files were modified

#### Example Workflow

1. AI creates initial PR for a ticket
2. Reviewer requests changes on the PR
3. PR feedback scanner detects the "request changes" review
4. AI analyzes the feedback and applies fixes to the existing PR branch
5. Original PR is automatically updated with the fixes
6. Process continues until the PR is approved

### Running the Application

```bash
# Run with default config.yaml
go run main.go

# Run with custom config file
go run main.go -config /path/to/config.yaml

# Build and run
go build -o jira-ai-solver
./jira-ai-solver -config config.yaml
```

## Testing

The project includes comprehensive unit tests for all components. Run the tests using:

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test ./handlers
go test ./services
```

## Usage

### Setting Up Jira Tickets

To have a ticket processed by the AI:

1. Ensure the ticket is in the configured "todo" status
2. The scanner will automatically pick up the ticket and process it

### Ticket Processing Flow

1. **Scanning**: The scanner periodically searches for tickets in "todo" status assigned to the configured user
2. **Processing**: When found, the ticket status is updated to "In Progress"
3. **Code Generation**: Claude CLI analyzes the ticket and generates code changes
4. **Pull Request**: A PR is created with the changes
5. **Completion**: The ticket status is changed to "In Review"



### Status Transitions

The application automatically transitions Jira ticket statuses during processing. These status transitions are configurable in the `jira.status_transitions` section of the configuration file:

```yaml
jira:
  status_transitions:
    todo: "To Do"               # Status for tickets ready for AI processing
    in_progress: "In Progress"  # Status when AI starts processing
    in_review: "In Review"      # Status when PR is created
```

**Default Flow:**
- **todo** → **in_progress** (when processing starts)
- **in_progress** → **in_review** (when PR is created)
- **in_progress** → **Open** (if processing fails)

**Ticket Scanning:**
The scanner looks for tickets where the configured Jira user is set as a contributor and are in the configured "todo" status. This approach ensures only appropriate tickets are processed.

**Custom Status Names:**
You can customize the status names to match your Jira workflow. For example:
```yaml
jira:
  status_transitions:
    todo: "To Do"
    in_progress: "Development"
    in_review: "Code Review"
```

This would:
- Look for tickets in "To Do" status (configured as `todo`) for processing
- Transition tickets to "Development" (configured as `in_progress`) when processing starts
- Transition tickets to "Code Review" (configured as `in_review`) when the PR is created

## Architecture

The application is built with a clean architecture pattern:

- **Services**: Handle external API interactions (Jira, GitHub, Claude CLI)
- **Handlers**: Process incoming requests and coordinate between services
- **Models**: Define data structures and configuration
- **Scanner**: Periodically searches for and processes tickets

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Submit a pull request

## License

This project is licensed under the MIT License - see the LICENSE file for details.
