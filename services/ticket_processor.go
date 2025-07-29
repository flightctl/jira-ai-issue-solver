package services

import (
	"fmt"
	"strings"
	"time"

	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
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
func (p *TicketProcessorImpl) ProcessTicket(ticketKey string) error {
	p.logger.Info("Processing ticket", zap.String("ticket", ticketKey))

	// Get the ticket details
	ticket, err := p.jiraService.GetTicket(ticketKey)
	if err != nil {
		p.logger.Error("Failed to get ticket details", zap.String("ticket", ticketKey), zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to get ticket details: %v", err))
		return err
	}

	// Get the repository URL from the component mapping
	if len(ticket.Fields.Components) == 0 {
		p.logger.Warn("No components found on ticket", zap.String("ticket", ticketKey))
		p.handleFailure(ticketKey, "No components found on ticket")
		return fmt.Errorf("no components found on ticket")
	}

	// Use the first component to find the repository
	firstComponent := ticket.Fields.Components[0].Name
	repoURL, ok := p.config.ComponentToRepo[firstComponent]
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
	statusTransitions := p.config.Jira.StatusTransitions.GetStatusTransitions(ticketType)

	// Update the ticket status to the configured "In Progress" status
	err = p.jiraService.UpdateTicketStatus(ticketKey, statusTransitions.InProgress)
	if err != nil {
		p.logger.Error("Failed to update ticket status",
			zap.String("ticket", ticketKey),
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

	// Check if a fork already exists
	exists, forkURL, err := p.githubService.CheckForkExists(owner, repo)
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

		// Wait for the fork to be ready by checking if it exists
		for i := 0; i < 10; i++ { // Try up to 10 times (50 seconds total)
			exists, forkURL, err = p.githubService.CheckForkExists(owner, repo)
			if err != nil {
				p.logger.Warn("Failed to check fork readiness",
					zap.String("ticket", ticketKey),
					zap.Int("attempt", i+1),
					zap.Error(err))
				time.Sleep(5 * time.Second)
				continue
			}

			if exists {
				p.logger.Info("Fork is ready",
					zap.String("ticket", ticketKey),
					zap.Int("attempts", i+1))
				break
			}

			p.logger.Debug("Fork not ready yet, waiting",
				zap.String("ticket", ticketKey),
				zap.Int("attempt", i+1))
			time.Sleep(5 * time.Second)
		}

		if !exists {
			p.logger.Error("Fork failed to become ready after multiple attempts",
				zap.String("ticket", ticketKey))
			p.handleFailure(ticketKey, "Fork failed to become ready after multiple attempts")
			return fmt.Errorf("fork failed to become ready after multiple attempts")
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

	// Commit the changes
	err = p.githubService.CommitChanges(repoDir, fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary), coAuthorName, coAuthorEmail)
	if err != nil {
		p.logger.Error("Failed to commit changes",
			zap.String("ticket", ticketKey),
			zap.String("repo_dir", repoDir),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to commit changes: %v", err))
		return err
	}

	// Push the changes
	err = p.githubService.PushChanges(repoDir, branchName)
	if err != nil {
		p.logger.Error("Failed to push changes",
			zap.String("ticket", ticketKey),
			zap.String("repo_dir", repoDir),
			zap.String("branch_name", branchName),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to push changes: %v", err))
		return err
	}

	// Create a pull request
	prTitle := fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary)

	// Build PR description with Jira ticket URL and assignee information
	prBody := fmt.Sprintf("This PR addresses the issue described in [%s](%s/browse/%s).\n\n**Summary:** %s\n\n**Description:** %s",
		ticketKey, p.config.Jira.BaseURL, ticketKey, ticket.Fields.Summary, ticket.Fields.Description)

	// Add assignee information if available
	if ticket.Fields.Assignee != nil {
		prBody += fmt.Sprintf("\n\n**Assignee:** %s (%s)", ticket.Fields.Assignee.DisplayName, ticket.Fields.Assignee.EmailAddress)
	}

	// When creating a pull request from a fork, the head parameter should be in the format "forkOwner:branchName"
	head := fmt.Sprintf("%s:%s", p.config.GitHub.BotUsername, branchName)
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

	// Update the Git Pull Request field on the Jira ticket
	if p.config.Jira.GitPullRequestFieldName != "" {
		err = p.jiraService.UpdateTicketFieldByName(ticketKey, p.config.Jira.GitPullRequestFieldName, pr.HTMLURL)
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
	}

	// Add a comment to the ticket
	comment := fmt.Sprintf("AI-generated pull request created: %s", pr.HTMLURL)
	err = p.jiraService.AddComment(ticketKey, comment)
	if err != nil {
		p.logger.Error("Failed to add comment",
			zap.String("ticket", ticketKey),
			zap.String("comment", comment),
			zap.Error(err))
		// Continue processing even if comment fails
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
	// Add a comment to the ticket only if error comments are not disabled
	if !p.config.Jira.DisableErrorComments {
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
