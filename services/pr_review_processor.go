package services

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// PRReviewProcessor defines the interface for processing PR review feedback
type PRReviewProcessor interface {
	// ProcessPRReviewFeedback processes feedback for tickets in "In Review" status
	ProcessPRReviewFeedback(ticketKey string) error
}

// PRReviewProcessorImpl implements the PRReviewProcessor interface
type PRReviewProcessorImpl struct {
	jiraService   JiraService
	githubService GitHubService
	aiService     AIService
	config        *models.Config
	logger        *zap.Logger
}

// NewPRReviewProcessor creates a new PRReviewProcessor
func NewPRReviewProcessor(
	jiraService JiraService,
	githubService GitHubService,
	aiService AIService,
	config *models.Config,
	logger *zap.Logger,
) PRReviewProcessor {
	return &PRReviewProcessorImpl{
		jiraService:   jiraService,
		githubService: githubService,
		aiService:     aiService,
		config:        config,
		logger:        logger,
	}
}

// ProcessPRReviewFeedback processes feedback for a ticket that has PR review feedback
func (p *PRReviewProcessorImpl) ProcessPRReviewFeedback(ticketKey string) error {
	p.logger.Info("Processing PR review feedback for ticket", zap.String("ticket", ticketKey))

	// Get the ticket details
	ticket, err := p.jiraService.GetTicket(ticketKey)
	if err != nil {
		p.logger.Error("Failed to get ticket details", zap.String("ticket", ticketKey), zap.Error(err))
		return err
	}

	// Get the PR URL from the custom field
	prURL, err := p.getPRURLFromTicket(ticket)
	if err != nil {
		p.logger.Error("Failed to get PR URL from ticket", zap.String("ticket", ticketKey), zap.Error(err))
		return err
	}

	if prURL == "" {
		p.logger.Info("No PR URL found for ticket", zap.String("ticket", ticketKey))
		return nil
	}

	// Extract PR details from the URL
	owner, repo, prNumber, err := p.extractPRInfoFromURL(prURL)
	if err != nil {
		p.logger.Error("Failed to extract PR info from URL", zap.String("ticket", ticketKey), zap.String("pr_url", prURL), zap.Error(err))
		return err
	}

	// Get detailed PR information including reviews
	prDetails, err := p.githubService.GetPRDetails(owner, repo, prNumber)
	if err != nil {
		p.logger.Error("Failed to get PR details", zap.String("ticket", ticketKey), zap.String("owner", owner), zap.String("repo", repo), zap.Int("pr_number", prNumber), zap.Error(err))
		return err
	}

	// Get the last processing timestamp from PR comments
	lastProcessedTime, err := p.getLastProcessingTimestamp(owner, repo, prNumber)
	if err != nil {
		p.logger.Error("Failed to get last processing timestamp", zap.String("ticket", ticketKey), zap.Error(err))
		// Continue with processing, will use a default time
		lastProcessedTime = time.Time{}
	}

	// Filter reviews and comments by timestamp and bot user
	filteredReviews := p.filterReviewsByTimestamp(prDetails.Reviews, lastProcessedTime)
	filteredComments := p.filterCommentsByTimestamp(prDetails.Comments, lastProcessedTime)

	// Check if there are any "request changes" reviews in the filtered set
	hasRequestChanges := p.hasRequestChangesReviews(filteredReviews)
	if !hasRequestChanges && len(filteredComments) == 0 {
		p.logger.Info("No new 'request changes' reviews or comments found for PR", zap.String("ticket", ticketKey), zap.Int("pr_number", prNumber), zap.Time("last_processed", lastProcessedTime))
		return nil
	}

	// 2. Collect all feedback from reviews and comments (including handled ones for context)
	feedback := p.collectFeedback(prDetails.Reviews, prDetails.Comments, lastProcessedTime)

	// Get the repository URL from the PR details (our fork)
	repoURL, err := p.getRepositoryURLFromPR(prDetails)
	if err != nil {
		p.logger.Error("Failed to get repository URL from PR", zap.String("ticket", ticketKey), zap.Error(err))
		return err
	}

	// Clone the repository and apply fixes
	err = p.applyFeedbackFixes(ticketKey, repoURL, prDetails, feedback)
	if err != nil {
		p.logger.Error("Failed to apply feedback fixes", zap.String("ticket", ticketKey), zap.Error(err))
		return err
	}

	// Update the processing timestamp in PR comments
	err = p.updateProcessingTimestamp(owner, repo, prNumber, ticketKey)
	if err != nil {
		p.logger.Error("Failed to update processing timestamp", zap.String("ticket", ticketKey), zap.Error(err))
		// Continue even if timestamp update fails
	}

	p.logger.Info("Successfully processed PR review feedback for ticket", zap.String("ticket", ticketKey))
	return nil
}

// getPRURLFromTicket extracts the PR URL from the ticket's custom field or comments
func (p *PRReviewProcessorImpl) getPRURLFromTicket(ticket *models.JiraTicketResponse) (string, error) {
	var prURL string
	var err error

	// Get project configuration for this ticket
	projectConfig := p.config.GetProjectConfigForTicket(ticket.Key)
	if projectConfig == nil {
		return "", fmt.Errorf("no project configuration found for ticket %s", ticket.Key)
	}

	// First, try to get PR URL from the git custom field if configured for this project
	if projectConfig.GitPullRequestFieldName != "" {
		prURL, err = p.getPRURLFromGitField(ticket, projectConfig)
		if err != nil {
			p.logger.Debug("Failed to get PR URL from git field, will try comments",
				zap.String("ticket", ticket.Key),
				zap.Error(err))
		} else if prURL != "" {
			p.logger.Debug("Found PR URL in git field",
				zap.String("ticket", ticket.Key),
				zap.String("pr_url", prURL))
			return prURL, nil
		}
	}

	// If no PR URL found in git field (or field not configured), try comments
	p.logger.Debug("No PR URL found in git field, checking comments", zap.String("ticket", ticket.Key))
	prURL, err = p.getPRURLFromComments(ticket.Key)
	if err != nil {
		return "", fmt.Errorf("failed to get PR URL from comments: %w", err)
	}

	if prURL != "" {
		p.logger.Debug("Found PR URL in comments",
			zap.String("ticket", ticket.Key),
			zap.String("pr_url", prURL))
		return prURL, nil
	}

	// No PR URL found in either location
	return "", nil
}

// getPRURLFromGitField extracts the PR URL from the ticket's git custom field
func (p *PRReviewProcessorImpl) getPRURLFromGitField(ticket *models.JiraTicketResponse, projectConfig *models.ProjectConfig) (string, error) {
	// Get the field ID for the field name
	fieldID, err := p.jiraService.GetFieldIDByName(projectConfig.GitPullRequestFieldName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve field name '%s' to ID: %w", projectConfig.GitPullRequestFieldName, err)
	}
	// Log the fieldID for debugging
	p.logger.Debug("Resolved field name to field ID", zap.String("field_name", projectConfig.GitPullRequestFieldName), zap.String("field_id", fieldID))

	// Get the ticket with expanded fields to access custom fields
	fields, _, err := p.jiraService.GetTicketWithExpandedFields(ticket.Key)
	if err != nil {
		return "", fmt.Errorf("failed to get ticket with expanded fields: %w", err)
	}

	// Look for the custom field value
	if prURL, ok := fields[fieldID]; ok {
		// Handle string type
		if prURLStr, ok := prURL.(string); ok && prURLStr != "" {
			return prURLStr, nil
		}
		// Handle slice/array type (common in JIRA custom fields)
		if prURLSlice, ok := prURL.([]interface{}); ok && len(prURLSlice) > 0 {
			if firstURL, ok := prURLSlice[0].(string); ok && firstURL != "" {
				return firstURL, nil
			}
		}
		// Handle string slice type
		if prURLSlice, ok := prURL.([]string); ok && len(prURLSlice) > 0 {
			if prURLSlice[0] != "" {
				return prURLSlice[0], nil
			}
		}
	}
	// Log the full output for debugging
	p.logger.Debug("Full ticket fields", zap.Any("fields", fields))

	return "", nil
}

// getPRURLFromComments extracts the PR URL from ticket comments
func (p *PRReviewProcessorImpl) getPRURLFromComments(ticketKey string) (string, error) {
	// Get the ticket with comments expanded
	ticket, err := p.jiraService.GetTicketWithComments(ticketKey)
	if err != nil {
		return "", fmt.Errorf("failed to get ticket with comments: %w", err)
	}

	// Structured AI bot PR comment pattern (preferred)
	structuredPRPattern := regexp.MustCompile(`\[AI-BOT-PR\]\s+(https://github\.com/[^/\s]+/[^/\s]+/pull/\d+)`)

	// Search through comments for structured PR URLs first
	// Look through comments in reverse order (newest first) to find the most recent PR URL
	// Only check comments made by our bot
	for i := len(ticket.Fields.Comment.Comments) - 1; i >= 0; i-- {
		comment := ticket.Fields.Comment.Comments[i]

		// Skip comments not made by our bot
		if comment.Author.Name != p.config.Jira.Username {
			continue
		}

		// First, look for structured AI bot PR comments
		structuredMatches := structuredPRPattern.FindStringSubmatch(comment.Body)
		if len(structuredMatches) > 1 {
			prURL := structuredMatches[1]
			p.logger.Debug("Found structured AI-bot PR URL in comment",
				zap.String("ticket", ticketKey),
				zap.String("pr_url", prURL),
				zap.String("comment_id", comment.ID),
				zap.String("comment_author", comment.Author.DisplayName))
			return prURL, nil
		}
	}

	// No structured PR comment found
	return "", nil
}

// extractPRInfoFromURL extracts owner, repo, and PR number from a GitHub PR URL
func (p *PRReviewProcessorImpl) extractPRInfoFromURL(prURL string) (owner, repo string, prNumber int, err error) {
	// GitHub PR URL format: https://github.com/owner/repo/pull/number
	re := regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	matches := re.FindStringSubmatch(prURL)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid GitHub PR URL format: %s", prURL)
	}

	owner = matches[1]
	repo = matches[2]
	_, err = fmt.Sscanf(matches[3], "%d", &prNumber)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number: %s", matches[3])
	}

	return owner, repo, prNumber, nil
}

// hasRequestChangesReviews checks if there are any "request changes" reviews
func (p *PRReviewProcessorImpl) hasRequestChangesReviews(reviews []models.GitHubReview) bool {
	for _, review := range reviews {
		if strings.ToLower(review.State) == "changes_requested" {
			return true
		}
	}
	return false
}

// collectFeedback collects all feedback from reviews and comments, marking them as handled or new
func (p *PRReviewProcessorImpl) collectFeedback(reviews []models.GitHubReview, comments []models.GitHubPRComment, lastProcessedTime time.Time) string {
	var feedback strings.Builder

	feedback.WriteString("## PR Review Feedback\n\n")

	// Add review feedback
	if len(reviews) > 0 {
		feedback.WriteString("### Reviews\n\n")
		for _, review := range reviews {
			// Skip reviews from our bot
			if review.User.Login == p.config.GitHub.BotUsername {
				continue
			}

			status := "🔄 NEW"
			if !review.SubmittedAt.After(lastProcessedTime) {
				status = "✅ HANDLED"
			}

			feedback.WriteString(fmt.Sprintf("**Review by %s (%s) - %s:**\n", review.User.Login, review.State, status))
			feedback.WriteString(review.Body)
			feedback.WriteString("\n\n")
		}
	}

	// Add comment feedback
	if len(comments) > 0 {
		p.logger.Debug("Collecting PR comments",
			zap.Int("total_comments", len(comments)))

		feedback.WriteString("### Comments\n\n")
		for _, comment := range comments {
			// Skip comments from our bot
			if comment.User.Login == p.config.GitHub.BotUsername {
				continue
			}

			status := "🔄 NEW"
			if !comment.CreatedAt.After(lastProcessedTime) {
				status = "✅ HANDLED"
			}

			// Format comment header based on whether it's line-based or general
			var commentHeader string
			if comment.Path == "" || comment.Line == 0 {
				// General conversation comment (no specific file/line)
				commentHeader = fmt.Sprintf("**Comment by %s - %s:**\n", comment.User.Login, status)
				p.logger.Debug("General conversation comment",
					zap.String("user", comment.User.Login),
					zap.String("status", status))
			} else if comment.StartLine > 0 && comment.StartLine != comment.Line {
				// Multi-line comment (line range)
				commentHeader = fmt.Sprintf("**Comment by %s on %s:%d-%d - %s:**\n",
					comment.User.Login, comment.Path, comment.StartLine, comment.Line, status)
				p.logger.Debug("Multi-line review comment",
					zap.String("user", comment.User.Login),
					zap.String("path", comment.Path),
					zap.Int("start_line", comment.StartLine),
					zap.Int("end_line", comment.Line),
					zap.String("status", status))
			} else {
				// Single-line comment
				commentHeader = fmt.Sprintf("**Comment by %s on %s:%d - %s:**\n",
					comment.User.Login, comment.Path, comment.Line, status)
				p.logger.Debug("Single-line review comment",
					zap.String("user", comment.User.Login),
					zap.String("path", comment.Path),
					zap.Int("line", comment.Line),
					zap.String("status", status))
			}

			feedback.WriteString(commentHeader)
			feedback.WriteString(comment.Body)
			feedback.WriteString("\n\n")
		}
	}

	return feedback.String()
}

// getRepositoryURLFromPR gets the repository URL from the PR details (our fork)
func (p *PRReviewProcessorImpl) getRepositoryURLFromPR(pr *models.GitHubPRDetails) (string, error) {
	// The PR head repo should be our fork
	if pr.Head.Repo.CloneURL == "" {
		return "", fmt.Errorf("no clone URL found in PR head repository")
	}

	// Return the clone URL as-is, let the GitHub service handle authentication
	// The GitHub service should use the Personal Access Token for authentication
	return pr.Head.Repo.CloneURL, nil
}

// applyFeedbackFixes applies the feedback fixes to the code
func (p *PRReviewProcessorImpl) applyFeedbackFixes(ticketKey, forkURL string, pr *models.GitHubPRDetails, feedback string) error {
	p.logger.Info("Applying feedback fixes for ticket", zap.String("ticket", ticketKey))

	// Clone the repository
	repoDir := fmt.Sprintf("%s/%s-feedback", p.config.TempDir, ticketKey)
	err := p.githubService.CloneRepository(forkURL, repoDir)
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	// Switch to the existing PR branch
	branchName := pr.Head.Ref
	err = p.githubService.SwitchToBranch(repoDir, branchName)
	if err != nil {
		return fmt.Errorf("failed to switch to PR branch: %w", err)
	}

	// Pull the latest changes from the remote branch
	err = p.githubService.PullChanges(repoDir, branchName)
	if err != nil {
		return fmt.Errorf("failed to pull latest changes: %w", err)
	}

	// Generate a prompt for the AI service to fix the code based on feedback
	prompt := p.generateFeedbackPrompt(pr, feedback)

	// Run AI service to generate code fixes
	_, err = p.aiService.GenerateCode(prompt, repoDir)
	if err != nil {
		return fmt.Errorf("failed to generate code fixes: %w", err)
	}

	// Get ticket details to get assignee info for co-author
	ticket, err := p.jiraService.GetTicket(ticketKey)
	var coAuthorName, coAuthorEmail string
	if err == nil && ticket.Fields.Assignee != nil {
		coAuthorName = ticket.Fields.Assignee.DisplayName
		coAuthorEmail = ticket.Fields.Assignee.EmailAddress
	}

	// Commit the changes
	commitMessage := fmt.Sprintf("%s: Apply PR feedback fixes", ticketKey)
	err = p.githubService.CommitChanges(repoDir, commitMessage, coAuthorName, coAuthorEmail)
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Push the changes to update the original PR
	err = p.githubService.PushChanges(repoDir, branchName)
	if err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	p.logger.Info("Successfully updated PR #%d with feedback fixes for ticket %s", zap.Int("pr_number", pr.Number), zap.String("ticket", ticketKey))
	return nil
}

// generateFeedbackPrompt generates a prompt for the AI service to fix code based on feedback
func (p *PRReviewProcessorImpl) generateFeedbackPrompt(pr *models.GitHubPRDetails, feedback string) string {
	var prompt strings.Builder

	prompt.WriteString("You are a code reviewer and developer. You need to fix the code based on the following PR review feedback.\n\n")
	prompt.WriteString("## Original PR Information\n")
	prompt.WriteString(fmt.Sprintf("**Title:** %s\n", pr.Title))
	prompt.WriteString(fmt.Sprintf("**Description:** %s\n", pr.Body))
	prompt.WriteString(fmt.Sprintf("**PR URL:** %s\n\n", pr.HTMLURL))

	prompt.WriteString("## Changed Files\n")
	for _, file := range pr.Files {
		prompt.WriteString(fmt.Sprintf("- %s (%s): +%d -%d\n", file.Filename, file.Status, file.Additions, file.Deletions))
		if file.Patch != "" {
			prompt.WriteString("```diff\n")
			prompt.WriteString(file.Patch)
			prompt.WriteString("\n```\n")
		}
	}
	prompt.WriteString("\n")

	prompt.WriteString("## Review Feedback\n")
	prompt.WriteString(feedback)
	prompt.WriteString("\n")

	prompt.WriteString("## Instructions\n")
	prompt.WriteString("1. Analyze the feedback carefully\n")
	prompt.WriteString("2. Understand what changes are being requested\n")
	prompt.WriteString("3. Apply the necessary fixes to the code\n")
	prompt.WriteString("4. Ensure the code quality is improved based on the feedback\n")
	prompt.WriteString("5. Make sure all requested changes are addressed\n")
	prompt.WriteString("6. Test your changes to ensure they work correctly\n\n")

	prompt.WriteString("Please apply the feedback and fix the code accordingly.")

	return prompt.String()
}

// getLastProcessingTimestamp retrieves the last processing timestamp from PR comments
func (p *PRReviewProcessorImpl) getLastProcessingTimestamp(owner, repo string, prNumber int) (time.Time, error) {
	comments, err := p.githubService.ListPRComments(owner, repo, prNumber)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get PR comments: %w", err)
	}

	timestampPattern := regexp.MustCompile(`🤖 AI Processing Timestamp: (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)`)
	var latestTimestamp time.Time

	for _, comment := range comments {
		if comment.User.Login == p.config.GitHub.BotUsername {
			matches := timestampPattern.FindStringSubmatch(comment.Body)
			if len(matches) == 2 {
				timestamp, err := time.Parse(time.RFC3339, matches[1])
				if err == nil && timestamp.After(latestTimestamp) {
					latestTimestamp = timestamp
				}
			}
		}
	}

	return latestTimestamp, nil
}

// updateProcessingTimestamp adds a comment with the current processing timestamp
func (p *PRReviewProcessorImpl) updateProcessingTimestamp(owner, repo string, prNumber int, ticketKey string) error {
	currentTime := time.Now().UTC()

	// Check if ticket has security level set and redact comment if needed
	hasSecurityLevel, err := p.jiraService.HasSecurityLevel(ticketKey)
	if err != nil {
		p.logger.Warn("Failed to check security level for ticket when adding timestamp comment",
			zap.String("ticket", ticketKey),
			zap.Error(err))
		// Continue with normal comment if security check fails
		hasSecurityLevel = false
	}

	commentBody := fmt.Sprintf(`🤖 AI Processing Timestamp: %s

AI has processed feedback for ticket %s at this time. Future processing will only consider feedback submitted after this timestamp.`,
		currentTime.Format(time.RFC3339), ticketKey)

	if hasSecurityLevel {
		p.logger.Info("Ticket has security level set, redacting timestamp comment",
			zap.String("ticket", ticketKey))
		commentBody = fmt.Sprintf(`🤖 AI Processing Timestamp: %s

AI has processed feedback for ticket %s at this time.`,
			currentTime.Format(time.RFC3339), ticketKey)
	}

	return p.githubService.AddPRComment(owner, repo, prNumber, commentBody)
}

// filterReviewsByTimestamp filters reviews by timestamp and bot user
func (p *PRReviewProcessorImpl) filterReviewsByTimestamp(reviews []models.GitHubReview, lastProcessedTime time.Time) []models.GitHubReview {
	var filtered []models.GitHubReview

	for _, review := range reviews {
		// Skip reviews from our bot to prevent loops
		if review.User.Login == p.config.GitHub.BotUsername {
			continue
		}

		// Skip reviews submitted before or at the last processed time
		if !review.SubmittedAt.After(lastProcessedTime) {
			continue
		}

		filtered = append(filtered, review)
	}

	return filtered
}

// filterCommentsByTimestamp filters comments by timestamp and bot user
func (p *PRReviewProcessorImpl) filterCommentsByTimestamp(comments []models.GitHubPRComment, lastProcessedTime time.Time) []models.GitHubPRComment {
	var filtered []models.GitHubPRComment

	for _, comment := range comments {
		// Skip comments from our bot to prevent loops
		if comment.User.Login == p.config.GitHub.BotUsername {
			continue
		}

		// Skip comments created before or at the last processed time
		if !comment.CreatedAt.After(lastProcessedTime) {
			continue
		}

		filtered = append(filtered, comment)
	}

	return filtered
}
