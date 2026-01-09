package services

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// TicketProcessor defines the interface for processing Jira tickets
type TicketProcessor interface {
	// ProcessTicket processes a single Jira ticket
	ProcessTicket(ticketKey string) error
}

// TicketProcessorImpl implements the TicketProcessor interface
type TicketProcessorImpl struct {
	jiraService   JiraService
	githubService GitHubService
	aiService     AIService
	config        *models.Config
	logger        *zap.Logger
}

// NewTicketProcessor creates a new TicketProcessor
func NewTicketProcessor(
	jiraService JiraService,
	githubService GitHubService,
	aiService AIService,
	config *models.Config,
	logger *zap.Logger,
) TicketProcessor {
	return &TicketProcessorImpl{
		jiraService:   jiraService,
		githubService: githubService,
		aiService:     aiService,
		config:        config,
		logger:        logger,
	}
}

// ProcessTicket processes a Jira ticket
//
//nolint:gocyclo // High complexity is inherent to workflow orchestration
func (p *TicketProcessorImpl) ProcessTicket(ticketKey string) error {
	p.logger.Info("Processing ticket", zap.String("ticket", ticketKey))

	// Get the ticket details
	ticket, err := p.jiraService.GetTicket(ticketKey)
	if err != nil {
		p.logger.Error("Failed to get ticket details", zap.String("ticket", ticketKey), zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to get ticket details: %v", err))
		return err
	}

	// Check if ticket has security level set for redaction throughout the process
	hasSecurityLevel, err := p.jiraService.HasSecurityLevel(ticketKey)
	if err != nil {
		p.logger.Warn("Failed to check security level for ticket",
			zap.String("ticket", ticketKey),
			zap.Error(err))
		// Continue with normal processing if security check fails
		hasSecurityLevel = false
	}

	if hasSecurityLevel {
		p.logger.Info("Ticket has security level set, will redact sensitive information",
			zap.String("ticket", ticketKey))
	}

	// Get the repository URL from the component mapping
	if len(ticket.Fields.Components) == 0 {
		p.logger.Warn("No components found on ticket", zap.String("ticket", ticketKey))
		p.handleFailure(ticketKey, "No components found on ticket")
		return fmt.Errorf("no components found on ticket")
	}

	// Get project configuration for this ticket
	projectConfig := p.config.GetProjectConfigForTicket(ticketKey)
	if projectConfig == nil {
		p.handleFailure(ticketKey, "No project configuration found for ticket")
		return fmt.Errorf("no project configuration found for ticket %s", ticketKey)
	}

	// Use the first component to find the repository
	firstComponent := ticket.Fields.Components[0].Name
	// Convert to lowercase for lookup since Viper lowercases all config keys
	componentKey := strings.ToLower(firstComponent)
	repoURL, ok := projectConfig.ComponentToRepo[componentKey]
	if !ok || repoURL == "" {
		p.logger.Error("No repository mapping found for component",
			zap.String("ticket", ticketKey),
			zap.String("component", firstComponent))
		p.handleFailure(ticketKey, fmt.Sprintf("No repository mapping found for component: %s", firstComponent))
		return fmt.Errorf("no repository mapping found for component: %s", firstComponent)
	}
	p.logger.Info("Found repository mapping for component",
		zap.String("ticket", ticketKey),
		zap.String("component", firstComponent),
		zap.String("repo_url", repoURL))

	// Get status transitions for this ticket type
	ticketType := ticket.Fields.IssueType.Name
	statusTransitions := projectConfig.StatusTransitions.GetStatusTransitions(ticketType)

	p.logger.Info("Retrieved status transitions for ticket",
		zap.String("ticket", ticketKey),
		zap.String("ticket_type", ticketType),
		zap.String("in_review_status", statusTransitions.InReview))

	// Note: We don't update to "In Progress" status here
	// All work is done first, then we transition directly to "In Review" on success
	// This prevents tickets from getting stuck in "In Progress" if something fails

	// Extract owner and repo from the repository URL
	owner, repo, err := ExtractRepoInfo(repoURL)
	if err != nil {
		p.logger.Error("Failed to extract repo info",
			zap.String("ticket", ticketKey),
			zap.String("repo_url", repoURL),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to extract repo info: %v", err))
		return err
	}
	p.logger.Debug("Extracted repo info",
		zap.String("ticket", ticketKey),
		zap.String("owner", owner),
		zap.String("repo", repo))

	// Determine which fork to use based on authentication method
	var forkOwner string
	var forkURL string

	if p.config.GitHub.AppID != 0 {
		// GitHub App mode: use assignee's fork
		if ticket.Fields.Assignee == nil {
			p.handleFailure(ticketKey, "Ticket has no assignee (required for GitHub App workflow)")
			return fmt.Errorf("ticket %s has no assignee", ticketKey)
		}

		assigneeEmail := ticket.Fields.Assignee.EmailAddress
		githubUsername, ok := p.config.Jira.AssigneeToGitHubUsername[assigneeEmail]
		if !ok {
			p.handleFailure(ticketKey, fmt.Sprintf("No GitHub username mapping found for assignee %s", assigneeEmail))
			return fmt.Errorf("no GitHub username mapping found for assignee %s (add to config: jira.assignee_to_github_username)", assigneeEmail)
		}

		forkOwner = githubUsername
		p.logger.Info("Using assignee's fork (GitHub App mode)",
			zap.String("ticket", ticketKey),
			zap.String("assignee", assigneeEmail),
			zap.String("githubUsername", githubUsername))

		// Check if assignee's fork exists
		exists, err := p.githubService.CheckForkExistsForUser(owner, repo, forkOwner)
		if err != nil {
			p.logger.Error("Failed to check if fork exists",
				zap.String("ticket", ticketKey),
				zap.String("forkOwner", forkOwner),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to check if fork exists: %v", err))
			return err
		}

		if !exists {
			message := fmt.Sprintf(
				"Setup Required: The assignee (%s) needs to:\n"+
					"1. Fork the repository %s/%s on GitHub\n"+
					"2. Install the GitHub App on their fork\n\n"+
					"Please contact your GitHub administrator if you need help with this setup.",
				assigneeEmail, owner, repo)
			p.handleFailure(ticketKey, message)
			return fmt.Errorf("fork %s/%s does not exist", forkOwner, repo)
		}

		// Verify GitHub App is installed on the fork
		_, err = p.githubService.GetInstallationIDForRepo(forkOwner, repo)
		if err != nil {
			message := fmt.Sprintf(
				"GitHub App Installation Required: The assignee (%s) has forked the repository but needs to:\n"+
					"1. Install the GitHub App on their fork %s/%s\n\n"+
					"Installation instructions:\n"+
					"- Go to https://github.com/%s/%s/settings/installations\n"+
					"- Install the app to enable automated code generation\n\n"+
					"Error details: %v",
				assigneeEmail, forkOwner, repo, forkOwner, repo, err)
			p.handleFailure(ticketKey, message)
			return fmt.Errorf("GitHub App not installed on fork %s/%s: %w", forkOwner, repo, err)
		}

		// Get fork clone URL
		forkURL, err = p.githubService.GetForkCloneURLForUser(owner, repo, forkOwner)
		if err != nil {
			p.logger.Error("Failed to get fork clone URL",
				zap.String("ticket", ticketKey),
				zap.String("forkOwner", forkOwner),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to get fork clone URL: %v", err))
			return err
		}

		p.logger.Info("Using assignee's fork",
			zap.String("ticket", ticketKey),
			zap.String("fork", fmt.Sprintf("%s/%s", forkOwner, repo)))
	} else {
		// PAT mode: use bot's fork (legacy behavior)
		forkOwner = p.config.GitHub.BotUsername
		p.logger.Info("Using bot's fork (PAT mode)", zap.String("ticket", ticketKey))

		// Check if a fork already exists
		var exists bool
		exists, forkURL, err = p.githubService.CheckForkExists(owner, repo)
		if err != nil {
			p.logger.Error("Failed to check if fork exists",
				zap.String("ticket", ticketKey),
				zap.String("owner", owner),
				zap.String("repo", repo),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to check if fork exists: %v", err))
			return err
		}

		if !exists {
			// Create a fork
			forkURL, err = p.githubService.ForkRepository(owner, repo)
			if err != nil {
				p.logger.Error("Failed to create fork",
					zap.String("ticket", ticketKey),
					zap.String("owner", owner),
					zap.String("repo", repo),
					zap.Error(err))
				p.handleFailure(ticketKey, fmt.Sprintf("Failed to create fork: %v", err))
				return err
			}
			p.logger.Info("Fork created successfully, waiting for fork to be ready",
				zap.String("ticket", ticketKey),
				zap.String("fork_url", forkURL))

			// Wait for the fork to be ready
			time.Sleep(10 * time.Second)
		}
	}

	// Clone the repository
	repoDir := strings.Join([]string{p.config.TempDir, ticketKey}, "/")
	err = p.githubService.CloneRepository(forkURL, repoDir)
	if err != nil {
		p.logger.Error("Failed to clone repository",
			zap.String("ticket", ticketKey),
			zap.String("fork_url", forkURL),
			zap.String("repo_dir", repoDir),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to clone repository: %v", err))
		return err
	}

	// Switch to the target branch if we're not already on it
	err = p.githubService.SwitchToTargetBranch(repoDir)
	if err != nil {
		p.logger.Error("Failed to switch to target branch",
			zap.String("ticket", ticketKey),
			zap.String("repo_dir", repoDir),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to switch to target branch: %v", err))
		return err
	}

	// Create a new branch
	branchName := ticketKey
	err = p.githubService.CreateBranch(repoDir, branchName)
	if err != nil {
		p.logger.Error("Failed to create branch",
			zap.String("ticket", ticketKey),
			zap.String("repo_dir", repoDir),
			zap.String("branch_name", branchName),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to create branch: %v", err))
		return err
	}

	// Generate documentation file (CLAUDE.md or GEMINI.md) if it doesn't exist and is enabled in config
	if p.config.AI.GenerateDocumentation {
		err = p.aiService.GenerateDocumentation(repoDir)
		if err != nil {
			p.logger.Warn("Failed to generate documentation",
				zap.String("ticket", ticketKey),
				zap.String("repo_dir", repoDir),
				zap.Error(err))
			// Continue processing even if documentation generation fails
		}
	} else {
		p.logger.Info("Documentation generation disabled in configuration",
			zap.String("ticket", ticketKey),
			zap.String("ai_provider", p.config.AIProvider))
	}

	// Generate a prompt for AI
	prompt := p.generatePrompt(ticket)

	// Run AI service to generate code changes with retry logic
	// AI systems can sometimes be non-deterministic or fail silently without making changes
	var hasChanges bool
	maxRetries := p.config.AI.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5 // Fallback to safe default
	}

	p.logger.Info("Starting AI code generation with retry logic",
		zap.String("ticket", ticketKey),
		zap.Int("maxRetries", maxRetries))

	for attempt := 1; attempt <= maxRetries; attempt++ {
		p.logger.Info("AI code generation attempt",
			zap.String("ticket", ticketKey),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries))

		// Run AI service
		_, err = p.aiService.GenerateCode(prompt, repoDir)
		if err != nil {
			p.logger.Error("AI code generation failed",
				zap.String("ticket", ticketKey),
				zap.Int("attempt", attempt),
				zap.String("repo_dir", repoDir),
				zap.Error(err))
			// Don't retry on hard errors - fail fast
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to generate code changes: %v", err))
			return err
		}

		// Check if AI actually made any changes
		hasChanges, err = p.githubService.HasChanges(repoDir)
		if err != nil {
			p.logger.Error("Failed to check for changes",
				zap.String("ticket", ticketKey),
				zap.Int("attempt", attempt),
				zap.Error(err))
			// Don't fail - assume changes exist to be safe
			hasChanges = true
			break
		}

		if hasChanges {
			p.logger.Info("AI successfully generated code changes",
				zap.String("ticket", ticketKey),
				zap.Int("attempt", attempt))
			break
		}

		// No changes detected
		p.logger.Warn("AI completed but made no changes",
			zap.String("ticket", ticketKey),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries))

		// If we haven't reached max retries, wait before trying again
		if attempt < maxRetries {
			retryDelay := time.Duration(p.config.AI.RetryDelaySeconds) * time.Second
			p.logger.Info("Waiting before retry",
				zap.String("ticket", ticketKey),
				zap.Duration("delay", retryDelay))
			time.Sleep(retryDelay)
		}
	}

	// After all retries, check if we got changes
	if !hasChanges {
		p.logger.Error("AI failed to generate any changes after all retries",
			zap.String("ticket", ticketKey),
			zap.Int("attempts", maxRetries))
		p.handleFailure(ticketKey, fmt.Sprintf("AI generated no code changes after %d attempts. This may indicate the ticket requirements are unclear or the AI system is experiencing issues.", maxRetries))
		return fmt.Errorf("AI generated no code changes after %d attempts", maxRetries)
	}

	// Get assignee info for co-author
	var coAuthorName, coAuthorEmail string
	if ticket.Fields.Assignee != nil {
		coAuthorName = ticket.Fields.Assignee.DisplayName
		coAuthorEmail = ticket.Fields.Assignee.EmailAddress
	}

	// Commit the changes (redact commit message if security level is set)
	commitMessage := fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary)
	if hasSecurityLevel {
		commitMessage = fmt.Sprintf("%s: Security-related changes", ticketKey)
	}

	// Use API commits for GitHub App (creates verified commits)
	// Use CLI commits for PAT (requires git push separately)
	if p.config.GitHub.AppID != 0 {
		// GitHub App mode: Use API to create verified commit
		p.logger.Info("Creating verified commit via GitHub API",
			zap.String("ticket", ticketKey),
			zap.String("owner", forkOwner),
			zap.String("repo", repo),
			zap.String("branch", branchName))

		commitSHA, err := p.githubService.CommitChangesViaAPI(forkOwner, repo, branchName, commitMessage, repoDir, coAuthorName, coAuthorEmail)
		if err != nil {
			p.logger.Error("Failed to commit changes via API",
				zap.String("ticket", ticketKey),
				zap.String("repo_dir", repoDir),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to commit changes: %v", err))
			return err
		}

		p.logger.Info("Successfully created verified commit",
			zap.String("ticket", ticketKey),
			zap.String("commit_sha", commitSHA))

		// No push needed - commit is already on GitHub
	} else {
		// PAT mode: Use git CLI commit + push
		err = p.githubService.CommitChanges(repoDir, commitMessage, coAuthorName, coAuthorEmail)
		if err != nil {
			p.logger.Error("Failed to commit changes",
				zap.String("ticket", ticketKey),
				zap.String("repo_dir", repoDir),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to commit changes: %v", err))
			return err
		}

		// Push the changes
		err = p.githubService.PushChanges(repoDir, branchName, forkOwner, repo)
		if err != nil {
			p.logger.Error("Failed to push changes",
				zap.String("ticket", ticketKey),
				zap.String("repo_dir", repoDir),
				zap.String("branch_name", branchName),
				zap.String("forkOwner", forkOwner),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to push changes: %v", err))
			return err
		}
	}

	// Create PR content (redact if security level is set)
	prTitle := fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary)
	prBody := fmt.Sprintf("This PR addresses the issue described in [%s](%s/browse/%s).\n\n**Summary:** %s\n\n**Description:** %s",
		ticketKey, p.config.Jira.BaseURL, ticketKey, ticket.Fields.Summary, ticket.Fields.Description)

	// Add assignee information if available
	if ticket.Fields.Assignee != nil {
		prBody += fmt.Sprintf("\n\n**Assignee:** %s (%s)", ticket.Fields.Assignee.DisplayName, ticket.Fields.Assignee.EmailAddress)
	}

	if hasSecurityLevel {
		prTitle, prBody = redactPRContentForSecurity(ticketKey)
	}

	// When creating a pull request from a fork, the head parameter should be in the format "forkOwner:branchName"
	head := fmt.Sprintf("%s:%s", forkOwner, branchName)
	pr, err := p.githubService.CreatePullRequest(owner, repo, prTitle, prBody, head, p.config.GitHub.TargetBranch)
	if err != nil {
		p.logger.Error("Failed to create pull request",
			zap.String("ticket", ticketKey),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.String("head", head),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to create pull request: %v", err))
		return err
	}

	// Update the Git Pull Request field or add a comment to the Jira ticket
	if projectConfig.GitPullRequestFieldName != "" {
		// If we have a designated field, update it
		err = p.jiraService.UpdateTicketFieldByName(ticketKey, projectConfig.GitPullRequestFieldName, pr.HTMLURL)
		if err != nil {
			p.logger.Error("Failed to update Git Pull Request field",
				zap.String("ticket", ticketKey),
				zap.String("pr_url", pr.HTMLURL),
				zap.Error(err))
			// Continue processing even if field update fails
		} else {
			p.logger.Info("Successfully updated Git Pull Request field",
				zap.String("ticket", ticketKey),
				zap.String("pr_url", pr.HTMLURL))
		}
	} else {
		// If no designated field, add a structured comment for easy extraction
		// (Jira comments are not redacted since they're internal)
		comment := fmt.Sprintf("[AI-BOT-PR] %s", pr.HTMLURL)

		err = p.jiraService.AddComment(ticketKey, comment)
		if err != nil {
			p.logger.Error("Failed to add comment",
				zap.String("ticket", ticketKey),
				zap.String("comment", comment),
				zap.Error(err))
			// Continue processing even if comment fails
		} else {
			p.logger.Info("Added structured PR comment to ticket",
				zap.String("ticket", ticketKey),
				zap.String("pr_url", pr.HTMLURL))
		}
	}

	// Update the ticket status to the configured "In Review" status
	err = p.jiraService.UpdateTicketStatus(ticketKey, statusTransitions.InReview)
	if err != nil {
		p.logger.Error("Failed to update ticket status",
			zap.String("ticket", ticketKey),
			zap.Error(err))
		// Continue processing even if status update fails
	}

	p.logger.Info("Successfully processed ticket", zap.String("ticket", ticketKey))
	return nil
}

// handleFailure handles a failure in processing a ticket
func (p *TicketProcessorImpl) handleFailure(ticketKey, errorMessage string) {
	// Get project configuration for this ticket to check if error comments are disabled
	projectConfig := p.config.GetProjectConfigForTicket(ticketKey)

	// Add a comment to the ticket only if error comments are not disabled
	disableComments := false
	if projectConfig != nil {
		disableComments = projectConfig.DisableErrorComments
	}

	if !disableComments {
		err := p.jiraService.AddComment(ticketKey, fmt.Sprintf("AI failed to process this ticket: %s", errorMessage))
		if err != nil {
			p.logger.Error("Failed to add error comment", zap.String("ticket", ticketKey), zap.Error(err))
		}
	} else {
		p.logger.Warn("Error commenting disabled, not adding error comment for ticket", zap.String("ticket", ticketKey), zap.String("error_message", errorMessage))
	}

}

// generatePrompt generates a prompt for AI based on the ticket
func (p *TicketProcessorImpl) generatePrompt(ticket *models.JiraTicketResponse) string {
	var prompt strings.Builder

	prompt.WriteString("You are an expert software engineer tasked with implementing changes to resolve a Jira ticket.\n\n")

	// Ticket information in structured XML format
	prompt.WriteString("<ticket>\n")
	prompt.WriteString(fmt.Sprintf("<key>%s</key>\n", ticket.Key))
	prompt.WriteString(fmt.Sprintf("<type>%s</type>\n", ticket.Fields.IssueType.Name))
	prompt.WriteString(fmt.Sprintf("<summary>%s</summary>\n\n", ticket.Fields.Summary))

	prompt.WriteString("<description>\n")
	if ticket.Fields.Description != "" {
		prompt.WriteString(ticket.Fields.Description)
	} else {
		prompt.WriteString("(No detailed description provided - use summary and comments to understand requirements)")
	}
	prompt.WriteString("\n</description>\n\n")

	// Add comments if available, filtering out bot comments
	if len(ticket.Fields.Comment.Comments) > 0 {
		hasNonBotComments := false
		var commentsBuilder strings.Builder
		commentsBuilder.WriteString("<comments>\n")

		for _, comment := range ticket.Fields.Comment.Comments {
			// Skip comments made by our Jira bot
			if comment.Author.Name == p.config.Jira.Username {
				continue
			}
			hasNonBotComments = true
			commentsBuilder.WriteString(fmt.Sprintf("<comment author=\"%s\" date=\"%s\">\n%s\n</comment>\n\n",
				comment.Author.DisplayName,
				comment.Created,
				comment.Body))
		}
		commentsBuilder.WriteString("</comments>\n\n")

		if hasNonBotComments {
			prompt.WriteString(commentsBuilder.String())
		}
	}

	prompt.WriteString("</ticket>\n\n")

	// Clear task definition with strong testing emphasis
	prompt.WriteString("<task>\n")
	prompt.WriteString("Your task is to implement the changes described in this ticket:\n\n")
	prompt.WriteString("1. Analyze the requirements and identify all files that need to be modified or created\n")
	prompt.WriteString("2. Review the codebase structure and follow existing patterns (consult CLAUDE.md or GEMINI.md if available)\n")
	prompt.WriteString("3. Implement the necessary code changes using the project's coding conventions\n")
	prompt.WriteString("4. Write comprehensive unit tests for your changes (see testing requirements below)\n")
	prompt.WriteString("5. Ensure all changes pass the project's linting and formatting checks\n")
	prompt.WriteString("</task>\n\n")

	// Explicit testing requirements
	prompt.WriteString("<testing_requirements>\n")
	prompt.WriteString("Write comprehensive unit tests for all new or modified functionality:\n\n")
	prompt.WriteString("- Test the contract/behavior, not implementation details\n")
	prompt.WriteString("- Cover happy path cases (expected successful operations)\n")
	prompt.WriteString("- Cover error cases (invalid input, edge conditions, failures)\n")
	prompt.WriteString("- Cover boundary conditions (empty inputs, nil values, limits)\n")
	prompt.WriteString("- Use descriptive test names that explain the scenario and expected outcome\n")
	prompt.WriteString("- Mock external dependencies (databases, APIs, file systems) but not internal logic\n\n")
	prompt.WriteString("If modifying existing code with tests:\n")
	prompt.WriteString("- Ensure existing tests still pass after your changes\n")
	prompt.WriteString("- Add new tests for any new behavior or edge cases you've introduced\n")
	prompt.WriteString("- Update tests if the contract/behavior intentionally changed\n\n")
	prompt.WriteString("If adding new code without existing tests:\n")
	prompt.WriteString("- Create a new test file following the project's test naming conventions\n")
	prompt.WriteString("- Provide thorough coverage of the new functionality\n")
	prompt.WriteString("</testing_requirements>\n\n")

	// Graceful degradation
	prompt.WriteString("<unclear_requirements>\n")
	prompt.WriteString("If any requirements are unclear or ambiguous:\n\n")
	prompt.WriteString("- State what clarification would be helpful\n")
	prompt.WriteString("- Proceed with the most reasonable interpretation based on the context\n")
	prompt.WriteString("- Document any assumptions you make in your implementation\n")
	prompt.WriteString("</unclear_requirements>\n\n")

	// Structured approach
	prompt.WriteString("<approach>\n")
	prompt.WriteString("Before making changes, briefly outline:\n\n")
	prompt.WriteString("1. Your understanding of what needs to be done\n")
	prompt.WriteString("2. Which files you'll modify or create (both implementation and test files)\n")
	prompt.WriteString("3. Your high-level implementation strategy\n")
	prompt.WriteString("4. What test scenarios you'll cover\n\n")
	prompt.WriteString("Then proceed with the implementation and tests.\n")
	prompt.WriteString("</approach>\n")

	return prompt.String()
}

// redactPRContentForSecurity creates redacted PR title and body when ticket has security level
func redactPRContentForSecurity(ticketKey string) (title string, body string) {
	title = fmt.Sprintf("%s: Update", ticketKey)
	body = fmt.Sprintf("This PR addresses ticket %s.", ticketKey)
	return
}
