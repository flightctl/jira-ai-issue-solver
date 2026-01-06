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
		zap.String("in_progress_status", statusTransitions.InProgress),
		zap.String("in_review_status", statusTransitions.InReview))

	// Update the ticket status to the configured "In Progress" status
	err = p.jiraService.UpdateTicketStatus(ticketKey, statusTransitions.InProgress)
	if err != nil {
		p.logger.Error("Failed to update ticket status",
			zap.String("ticket", ticketKey),
			zap.String("target_status", statusTransitions.InProgress),
			zap.String("ticket_type", ticketType),
			zap.Error(err))
		// Continue processing even if status update fails
	}

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

	// Generate a prompt for Claude CLI
	prompt := p.generatePrompt(ticket)

	// Run AI service to generate code changes
	_, err = p.aiService.GenerateCode(prompt, repoDir)
	if err != nil {
		p.logger.Error("Failed to generate code changes",
			zap.String("ticket", ticketKey),
			zap.String("repo_dir", repoDir),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to generate code changes: %v", err))
		return err
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

// generatePrompt generates a prompt for Claude CLI based on the ticket
func (p *TicketProcessorImpl) generatePrompt(ticket *models.JiraTicketResponse) string {
	prompt := fmt.Sprintf("Please help me fix the issue described in Jira ticket %s.\n\n", ticket.Key)
	prompt += fmt.Sprintf("Summary: %s\n\n", ticket.Fields.Summary)
	prompt += fmt.Sprintf("Description: %s\n\n", ticket.Fields.Description)

	// Add comments if available, filtering out bot comments
	if ticket.Fields.Comment.Comments != nil {
		prompt += "Comments:\n"
		for _, comment := range ticket.Fields.Comment.Comments {
			// Skip comments made by our Jira bot
			if comment.Author.Name == p.config.Jira.Username {
				continue
			}
			prompt += fmt.Sprintf("- %s: %s\n", comment.Author.DisplayName, comment.Body)
		}
		prompt += "\n"
	}

	prompt += "Please analyze the codebase and implement the necessary changes to fix this issue. " +
		"Make sure to follow the existing code style and patterns in the codebase."

	return prompt
}

// redactPRContentForSecurity creates redacted PR title and body when ticket has security level
func redactPRContentForSecurity(ticketKey string) (title string, body string) {
	title = fmt.Sprintf("%s: Update", ticketKey)
	body = fmt.Sprintf("This PR addresses ticket %s.", ticketKey)
	return
}
