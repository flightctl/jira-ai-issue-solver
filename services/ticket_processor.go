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

	// Get project configuration for this ticket
	projectConfig := p.config.GetProjectConfigForTicket(ticketKey)
	if projectConfig == nil {
		p.handleFailure(ticketKey, "No project configuration found for ticket")
		return fmt.Errorf("no project configuration found for ticket %s", ticketKey)
	}

	// Get all repository URLs for this ticket
	repoURLs, err := p.getRepositoryURLs(ticket, projectConfig)
	if err != nil {
		p.logger.Error("Failed to resolve repository URLs",
			zap.String("ticket", ticketKey),
			zap.Error(err))
		p.handleFailure(ticketKey, fmt.Sprintf("Failed to resolve repository URLs: %v", err))
		return err
	}

	if len(repoURLs) == 0 {
		p.logger.Warn("No repositories found for ticket", zap.String("ticket", ticketKey))
		p.handleFailure(ticketKey, "No repositories found for ticket")
		return fmt.Errorf("no repositories found for ticket %s", ticketKey)
	}

	p.logger.Info("Found repositories for ticket",
		zap.String("ticket", ticketKey),
		zap.Int("repo_count", len(repoURLs)),
		zap.Strings("repo_urls", repoURLs))

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

	// Process each repository
	var allPRURLs []string
	for i, repoURL := range repoURLs {
		// Extract repository owner and name for identification
		owner, repo, err := ExtractRepoInfo(repoURL)
		if err != nil {
			p.logger.Error("Failed to extract repo info for identification",
				zap.String("ticket", ticketKey),
				zap.String("repo_url", repoURL),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to extract repo info for %s: %v", repoURL, err))
			return err
		}

		repoIdentifier := fmt.Sprintf("%s/%s", owner, repo)

		p.logger.Info("Processing repository",
			zap.String("ticket", ticketKey),
			zap.String("repo_identifier", repoIdentifier),
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("repo_index", i+1),
			zap.Int("total_repos", len(repoURLs)),
			zap.String("repo_url", repoURL))

		prURL, err := p.processRepository(ticketKey, repoURL, ticket, hasSecurityLevel, repoIdentifier)
		if err != nil {
			p.logger.Error("Failed to process repository",
				zap.String("ticket", ticketKey),
				zap.String("repo_identifier", repoIdentifier),
				zap.String("repo_url", repoURL),
				zap.Error(err))
			p.handleFailure(ticketKey, fmt.Sprintf("Failed to process repository %s: %v", repoIdentifier, err))
			return err
		}

		if prURL != "" {
			allPRURLs = append(allPRURLs, prURL)
		}
	}

	// Update the Git Pull Request field or add comments to the Jira ticket with all PR URLs
	if len(allPRURLs) > 0 {
		err = p.updateTicketWithPRs(ticketKey, projectConfig, allPRURLs)
		if err != nil {
			p.logger.Error("Failed to update ticket with PR URLs",
				zap.String("ticket", ticketKey),
				zap.Strings("pr_urls", allPRURLs),
				zap.Error(err))
			// Continue processing even if ticket update fails
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

// processRepository handles the processing of a single repository for a ticket
func (p *TicketProcessorImpl) processRepository(ticketKey, repoURL string, ticket *models.JiraTicketResponse, hasSecurityLevel bool, repoIdentifier string) (string, error) {
	// Extract owner and repo from the repository URL
	owner, repo, err := ExtractRepoInfo(repoURL)
	if err != nil {
		p.logger.Error("Failed to extract repo info",
			zap.String("ticket", ticketKey),
			zap.String("repo_url", repoURL),
			zap.Error(err))
		return "", fmt.Errorf("failed to extract repo info: %w", err)
	}
	p.logger.Debug("Extracted repo info",
		zap.String("ticket", ticketKey),
		zap.String("owner", owner),
		zap.String("repo", repo))

	// Check if a fork already exists
	exists, forkURL, err := p.githubService.CheckForkExists(owner, repo)
	if err != nil {
		return "", fmt.Errorf("failed to check if fork exists: %w", err)
	}

	if !exists {
		// Create a fork
		forkURL, err = p.githubService.ForkRepository(owner, repo)
		if err != nil {
			return "", fmt.Errorf("failed to create fork: %w", err)
		}
		p.logger.Info("Fork created successfully, waiting for fork to be ready",
			zap.String("ticket", ticketKey),
			zap.String("fork_url", forkURL))

		// Wait for the fork to be ready by checking if it exists
		time.Sleep(10 * time.Second)
	}

	// Clone the repository - always use repo-specific directory with owner/repo format
	// Replace '/' with '-' to make it filesystem-safe
	repoSafeName := strings.ReplaceAll(repoIdentifier, "/", "-")
	repoDir := strings.Join([]string{p.config.TempDir, ticketKey, repoSafeName}, "/")

	err = p.githubService.CloneRepository(forkURL, repoDir)
	if err != nil {
		return "", fmt.Errorf("failed to clone repository: %w", err)
	}

	// Switch to the target branch if we're not already on it
	err = p.githubService.SwitchToTargetBranch(repoDir)
	if err != nil {
		return "", fmt.Errorf("failed to switch to target branch: %w", err)
	}

	// Create a new branch - always use repo-specific branch name with owner/repo format
	// Replace '/' with '-' to make it branch-safe
	branchName := fmt.Sprintf("%s-%s", ticketKey, repoSafeName)

	err = p.githubService.CreateBranch(repoDir, branchName)
	if err != nil {
		return "", fmt.Errorf("failed to create branch: %w", err)
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

	// Generate a prompt for AI CLI - always include repository context
	prompt := p.generatePromptWithRepoContext(ticket, repoIdentifier)

	// Run AI service to generate code changes
	_, err = p.aiService.GenerateCode(prompt, repoDir)
	if err != nil {
		return "", fmt.Errorf("failed to generate code changes: %w", err)
	}

	// Get assignee info for co-author
	var coAuthorName, coAuthorEmail string
	if ticket.Fields.Assignee != nil {
		coAuthorName = ticket.Fields.Assignee.DisplayName
		coAuthorEmail = ticket.Fields.Assignee.EmailAddress
	}

	// Commit the changes (redact commit message if security level is set)
	var commitMessage string
	if hasSecurityLevel {
		commitMessage = fmt.Sprintf("%s: Security-related changes", ticketKey)
	} else {
		commitMessage = fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary)
	}

	err = p.githubService.CommitChanges(repoDir, commitMessage, coAuthorName, coAuthorEmail)
	if err != nil {
		return "", fmt.Errorf("failed to commit changes: %w", err)
	}

	// Push the changes
	err = p.githubService.PushChanges(repoDir, branchName)
	if err != nil {
		return "", fmt.Errorf("failed to push changes: %w", err)
	}

	// Create PR content (redact if security level is set)
	var prTitle, prBody string
	if hasSecurityLevel {
		prTitle = fmt.Sprintf("%s: Update", ticketKey)
		prBody = fmt.Sprintf("This PR addresses ticket %s.", ticketKey)
	} else {
		prTitle = fmt.Sprintf("%s: %s", ticketKey, ticket.Fields.Summary)
		prBody = fmt.Sprintf("This PR addresses the issue described in [%s](%s/browse/%s).\n\n**Summary:** %s\n\n**Description:** %s",
			ticketKey, p.config.Jira.BaseURL, ticketKey, ticket.Fields.Summary, ticket.Fields.Description)

		// Add assignee information if available
		if ticket.Fields.Assignee != nil {
			prBody += fmt.Sprintf("\n\n**Assignee:** %s (%s)", ticket.Fields.Assignee.DisplayName, ticket.Fields.Assignee.EmailAddress)
		}
	}

	// When creating a pull request from a fork, the head parameter should be in the format "forkOwner:branchName"
	head := fmt.Sprintf("%s:%s", p.config.GitHub.BotUsername, branchName)
	pr, err := p.githubService.CreatePullRequest(owner, repo, prTitle, prBody, head, p.config.GitHub.TargetBranch)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	p.logger.Info("Successfully created pull request",
		zap.String("ticket", ticketKey),
		zap.String("repo", repo),
		zap.String("pr_url", pr.HTMLURL))

	return pr.HTMLURL, nil
}

// updateTicketWithPRs updates the Jira ticket with all PR URLs
func (p *TicketProcessorImpl) updateTicketWithPRs(ticketKey string, projectConfig *models.ProjectConfig, prURLs []string) error {
	if projectConfig.GitPullRequestFieldName != "" {
		// If we have a designated field, update it with all PR URLs (join with newlines)
		fieldValue := strings.Join(prURLs, "\n")

		err := p.jiraService.UpdateTicketFieldByName(ticketKey, projectConfig.GitPullRequestFieldName, fieldValue)
		if err != nil {
			p.logger.Error("Failed to update Git Pull Request field",
				zap.String("ticket", ticketKey),
				zap.Strings("pr_urls", prURLs),
				zap.Error(err))
			return err
		} else {
			p.logger.Info("Successfully updated Git Pull Request field",
				zap.String("ticket", ticketKey),
				zap.Strings("pr_urls", prURLs))
		}
	} else {
		// If no designated field, add structured comments for easy extraction
		// (Jira comments are not redacted since they're internal)
		for i, prURL := range prURLs {
			// Extract repository owner/name from PR URL for better identification
			repoIdentifier := "unknown/unknown"
			if owner, repo, err := ExtractRepoInfo(prURL); err == nil {
				repoIdentifier = fmt.Sprintf("%s/%s", owner, repo)
			}

			comment := fmt.Sprintf("[AI-BOT-PR-%d-%s] %s", i+1, repoIdentifier, prURL)

			err := p.jiraService.AddComment(ticketKey, comment)
			if err != nil {
				p.logger.Error("Failed to add comment",
					zap.String("ticket", ticketKey),
					zap.String("comment", comment),
					zap.Error(err))
				return err
			} else {
				p.logger.Info("Added structured PR comment to ticket",
					zap.String("ticket", ticketKey),
					zap.String("repo_identifier", repoIdentifier),
					zap.String("pr_url", prURL))
			}
		}
	}

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

// generatePromptWithRepoContext generates a prompt for AI CLI with repository context
func (p *TicketProcessorImpl) generatePromptWithRepoContext(ticket *models.JiraTicketResponse, repoIdentifier string) string {
	prompt := fmt.Sprintf("Please help me fix the issue described in Jira ticket %s.\n\n", ticket.Key)
	prompt += fmt.Sprintf("Working on repository: %s\n\n", repoIdentifier)
	prompt += fmt.Sprintf("Summary: %s\n\n", ticket.Fields.Summary)
	prompt += fmt.Sprintf("Description: %s\n\n", ticket.Fields.Description)

	// Add components context
	if len(ticket.Fields.Components) > 0 {
		prompt += "Components involved in this ticket:\n"
		for _, component := range ticket.Fields.Components {
			prompt += fmt.Sprintf("- %s\n", component.Name)
		}
		prompt += "\n"
	}

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

	prompt += fmt.Sprintf("Please analyze the codebase in this repository (%s) and implement the necessary changes to fix this issue. ", repoIdentifier) +
		"Focus on the parts relevant to this repository. " +
		"Make sure to follow the existing code style and patterns in the codebase. " +
		"Note that this issue may span multiple repositories, so focus only on the changes needed in this specific repository."

	return prompt
}

// getRepositoryURLs resolves all repository URLs for a ticket by checking labels first, then components
func (p *TicketProcessorImpl) getRepositoryURLs(ticket *models.JiraTicketResponse, projectConfig *models.ProjectConfig) ([]string, error) {
	var repoURLs []string
	repoURLSet := make(map[string]bool) // To deduplicate URLs

	// Try to get repository URL from labels first
	if projectConfig.RepoLabelName != "" {
		labelPrefix := projectConfig.RepoLabelName + ":"
		for _, label := range ticket.Fields.Labels {
			if strings.HasPrefix(label, labelPrefix) {
				repoURL := strings.TrimSpace(strings.TrimPrefix(label, labelPrefix))
				if repoURL != "" && !repoURLSet[repoURL] {
					repoURLSet[repoURL] = true
					repoURLs = append(repoURLs, repoURL)
					p.logger.Debug("Found repository URL in label",
						zap.String("ticket", ticket.Key),
						zap.String("label", label),
						zap.String("repo_url", repoURL))
				}
			}
		}
	}

	// If we found repositories from labels, use those
	if len(repoURLs) > 0 {
		p.logger.Info("Using repository URLs from labels",
			zap.String("ticket", ticket.Key),
			zap.Int("repo_count", len(repoURLs)))
		return repoURLs, nil
	}

	// Fallback to component mapping if no repos found in labels
	if len(ticket.Fields.Components) == 0 {
		return nil, fmt.Errorf("no components found on ticket and no repo URLs in labels")
	}

	// Map components to repositories
	for _, component := range ticket.Fields.Components {
		// Convert to lowercase for lookup since Viper lowercases all config keys
		componentKey := strings.ToLower(component.Name)
		if repoURL, ok := projectConfig.ComponentToRepo[componentKey]; ok && repoURL != "" {
			if !repoURLSet[repoURL] {
				repoURLSet[repoURL] = true
				repoURLs = append(repoURLs, repoURL)
				p.logger.Debug("Found repository mapping for component",
					zap.String("ticket", ticket.Key),
					zap.String("component", component.Name),
					zap.String("repo_url", repoURL))
			}
		} else {
			p.logger.Warn("No repository mapping found for component",
				zap.String("ticket", ticket.Key),
				zap.String("component", component.Name))
		}
	}

	if len(repoURLs) == 0 {
		return nil, fmt.Errorf("no repository mappings found for any components: %v",
			func() []string {
				var componentNames []string
				for _, comp := range ticket.Fields.Components {
					componentNames = append(componentNames, comp.Name)
				}
				return componentNames
			}())
	}

	p.logger.Info("Using repository URLs from component mapping",
		zap.String("ticket", ticket.Key),
		zap.Int("repo_count", len(repoURLs)))

	return repoURLs, nil
}

// getRepoURLFromLabels extracts repository URL from ticket labels based on configured label name
func (p *TicketProcessorImpl) getRepoURLFromLabels(ticket *models.JiraTicketResponse, projectConfig *models.ProjectConfig) (string, error) {
	if projectConfig.RepoLabelName == "" {
		return "", nil // No label name configured
	}

	labelPrefix := projectConfig.RepoLabelName + ":"
	for _, label := range ticket.Fields.Labels {
		if strings.HasPrefix(label, labelPrefix) {
			repoURL := strings.TrimSpace(strings.TrimPrefix(label, labelPrefix))
			if repoURL != "" {
				p.logger.Debug("Found repository URL in label",
					zap.String("ticket", ticket.Key),
					zap.String("label", label),
					zap.String("repo_url", repoURL))
				return repoURL, nil
			}
		}
	}

	return "", nil // No matching label found
}
