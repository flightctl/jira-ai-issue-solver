package services

import (
	"fmt"
	"os"
	"regexp"
	"sort"
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

// FeedbackData holds structured feedback information for a single group
type FeedbackData struct {
	NewFeedback      string                             // Formatted NEW feedback with comment IDs
	CommentMap       map[string]*models.GitHubPRComment // Map of comment IDs to comment objects
	ReviewCommentMap map[string]*models.GitHubReview    // Map of review IDs to review objects
}

// GroupedFeedbackData holds feedback grouped by file path
type GroupedFeedbackData struct {
	Groups  map[string]*FeedbackData // Key is file path, "" for general/reviews
	Summary string                   // Overall summary of handled items (shared across all groups)
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

	// 2. Collect and group feedback from reviews and comments (only NEW items, with summary of handled)
	// Feedback is grouped by file path for more focused AI processing
	groupedFeedback := p.collectFeedback(prDetails.Reviews, prDetails.Comments, lastProcessedTime)

	// Get the repository URL from the PR details (our fork)
	repoURL, err := p.getRepositoryURLFromPR(prDetails)
	if err != nil {
		// Check if this is a legacy PR with missing repository (deleted fork)
		if strings.Contains(err.Error(), "legacy PR with deleted fork") {
			p.logger.Info("Skipping legacy PR with deleted fork",
				zap.String("ticket", ticketKey),
				zap.Int("pr_number", prNumber),
				zap.String("pr_url", prURL))
			return nil // Skip gracefully without error
		}
		p.logger.Error("Failed to get repository URL from PR", zap.String("ticket", ticketKey), zap.Error(err))
		return err
	}

	// Clone the repository and apply fixes (processes each file group separately)
	err = p.applyFeedbackFixes(ticketKey, repoURL, prDetails, groupedFeedback, owner, repo, prNumber)
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

// hasRequestChangesReviews checks if there are any "request changes" reviews from non-bot users
func (p *PRReviewProcessorImpl) hasRequestChangesReviews(reviews []models.GitHubReview) bool {
	for _, review := range reviews {
		// Skip reviews from the bot itself to prevent infinite loops
		if review.User.Login == p.config.GitHub.BotUsername {
			continue
		}
		if strings.ToLower(review.State) == "changes_requested" {
			return true
		}
	}
	return false
}

// buildHandledItemsSummary builds a summary of previously handled feedback items
func (p *PRReviewProcessorImpl) buildHandledItemsSummary(reviews []models.GitHubReview, comments []models.GitHubPRComment, lastProcessedTime time.Time) string {
	var summary strings.Builder
	var handledItems []string

	for _, review := range reviews {
		if review.User.Login == p.config.GitHub.BotUsername {
			continue
		}
		if !review.SubmittedAt.After(lastProcessedTime) {
			// This is a HANDLED review
			if review.Body != "" {
				handledItems = append(handledItems, fmt.Sprintf("%s (review)", truncateString(review.Body, 80)))
			}
		}
	}

	for _, comment := range comments {
		if comment.User.Login == p.config.GitHub.BotUsername {
			continue
		}
		if !comment.CreatedAt.After(lastProcessedTime) {
			// This is a HANDLED comment
			if comment.Body != "" {
				handledItems = append(handledItems, truncateString(comment.Body, 80))
			}
		}
	}

	if len(handledItems) > 0 {
		summary.WriteString("Previously addressed (for context only - do not re-fix):\n")
		for _, item := range handledItems {
			summary.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}

	return summary.String()
}

// buildGroupFeedbackString builds the NewFeedback string for a single group
// The commentByID map is used to look up parent comments for threading context.
func (p *PRReviewProcessorImpl) buildGroupFeedbackString(path string, group *FeedbackData, commentByID map[int64]*models.GitHubPRComment) string {
	var newFeedback strings.Builder
	newFeedback.WriteString("## NEW Review Feedback (Action Required)\n\n")

	if path != "" {
		newFeedback.WriteString(fmt.Sprintf("**File: %s**\n\n", path))
	}

	// Add reviews (only in general group)
	for reviewID, review := range group.ReviewCommentMap {
		newFeedback.WriteString(fmt.Sprintf("### %s\n", reviewID))
		newFeedback.WriteString(fmt.Sprintf("**Review by %s (%s):**\n", review.User.Login, review.State))
		newFeedback.WriteString(review.Body)
		newFeedback.WriteString("\n\n")
	}

	// Add comments
	for commentID, comment := range group.CommentMap {
		// Format comment location
		var location string
		if comment.Path == "" || comment.Line == 0 {
			location = "General comment"
			p.logger.Debug("New general conversation comment",
				zap.String("user", comment.User.Login),
				zap.String("id", commentID))
		} else if comment.StartLine > 0 && comment.StartLine != comment.Line {
			location = fmt.Sprintf("on %s:%d-%d", comment.Path, comment.StartLine, comment.Line)
			p.logger.Debug("New multi-line review comment",
				zap.String("user", comment.User.Login),
				zap.String("path", comment.Path),
				zap.Int("start_line", comment.StartLine),
				zap.Int("end_line", comment.Line),
				zap.String("id", commentID),
				zap.String("group", path))
		} else {
			location = fmt.Sprintf("on %s:%d", comment.Path, comment.Line)
			p.logger.Debug("New single-line review comment",
				zap.String("user", comment.User.Login),
				zap.String("path", comment.Path),
				zap.Int("line", comment.Line),
				zap.String("id", commentID),
				zap.String("group", path))
		}

		newFeedback.WriteString(fmt.Sprintf("### %s\n", commentID))
		newFeedback.WriteString(fmt.Sprintf("**Comment by %s %s:**\n", comment.User.Login, location))

		// Check if this is a threaded reply (follow-up to a previous comment)
		if comment.InReplyToID != 0 {
			if parentComment, found := commentByID[comment.InReplyToID]; found {
				newFeedback.WriteString("*(Follow-up to previous discussion)*\n")
				newFeedback.WriteString(fmt.Sprintf("Previous comment by %s: \"%s\"\n\n",
					parentComment.User.Login,
					truncateString(parentComment.Body, 150)))
			}
		}

		newFeedback.WriteString(comment.Body)
		newFeedback.WriteString("\n\n")
	}

	if len(group.ReviewCommentMap) == 0 && len(group.CommentMap) == 0 {
		newFeedback.WriteString("No new feedback.\n")
	}

	return newFeedback.String()
}

// collectFeedback collects feedback, separating NEW items from HANDLED items, grouped by file path
// Returns a GroupedFeedbackData with feedback organized by file path ("" for general/reviews)
//
// IMPORTANT: This function does not modify the input slices. Internal copies are made before sorting
// to ensure deterministic behavior without affecting the caller's data.
func (p *PRReviewProcessorImpl) collectFeedback(reviews []models.GitHubReview, comments []models.GitHubPRComment, lastProcessedTime time.Time) *GroupedFeedbackData {
	// Create local copies to avoid mutating caller's data
	reviewsCopy := make([]models.GitHubReview, len(reviews))
	copy(reviewsCopy, reviews)
	commentsCopy := make([]models.GitHubPRComment, len(comments))
	copy(commentsCopy, comments)

	// Sort copies by ID for deterministic ordering
	sort.Slice(reviewsCopy, func(i, j int) bool {
		return reviewsCopy[i].ID < reviewsCopy[j].ID
	})
	sort.Slice(commentsCopy, func(i, j int) bool {
		return commentsCopy[i].ID < commentsCopy[j].ID
	})

	// Build a lookup map of all comments by ID for threaded reply context
	// NOTE: This map contains ALL comments (old and new) intentionally, so that buildGroupFeedbackString
	// can show parent comment context even if the parent is old/handled. This is critical for
	// understanding threaded conversations.
	commentByID := make(map[int64]*models.GitHubPRComment)
	for i := range commentsCopy {
		commentByID[commentsCopy[i].ID] = &commentsCopy[i]
	}

	// Build summary of HANDLED items (shared across all groups)
	summary := p.buildHandledItemsSummary(reviewsCopy, commentsCopy, lastProcessedTime)

	// Create groups map - key is file path, "" for general/reviews
	groups := make(map[string]*FeedbackData)

	// Helper function to get or create a group
	getGroup := func(path string) *FeedbackData {
		if groups[path] == nil {
			groups[path] = &FeedbackData{
				CommentMap:       make(map[string]*models.GitHubPRComment),
				ReviewCommentMap: make(map[string]*models.GitHubReview),
			}
		}
		return groups[path]
	}

	// Global counters for unique IDs across all groups
	commentIDCounter := 1
	reviewIDCounter := 1

	// Process NEW reviews - add to general ("") group
	for i := range reviewsCopy {
		review := &reviewsCopy[i]
		if review.User.Login == p.config.GitHub.BotUsername {
			continue
		}
		// Skip reviews with empty bodies
		if review.Body == "" {
			continue
		}
		if review.SubmittedAt.After(lastProcessedTime) {
			reviewID := fmt.Sprintf("REVIEW_%d", reviewIDCounter)
			reviewIDCounter++

			generalGroup := getGroup("")
			generalGroup.ReviewCommentMap[reviewID] = review
		}
	}

	// Process NEW comments - group by file path
	for i := range commentsCopy {
		comment := &commentsCopy[i]
		if comment.User.Login == p.config.GitHub.BotUsername {
			continue
		}
		// Skip comments with empty bodies
		if comment.Body == "" {
			continue
		}
		if comment.CreatedAt.After(lastProcessedTime) {
			commentID := fmt.Sprintf("COMMENT_%d", commentIDCounter)
			commentIDCounter++

			// Determine which group this comment belongs to
			groupKey := ""
			if comment.Path != "" && comment.Line > 0 {
				// File-specific comment
				groupKey = comment.Path
			}
			// else: general comment, stays in "" group

			group := getGroup(groupKey)
			group.CommentMap[commentID] = comment
		}
	}

	// Build NewFeedback string for each group using helper function
	for path, group := range groups {
		group.NewFeedback = p.buildGroupFeedbackString(path, group, commentByID)
	}

	// Count total new items and log per-group details for debugging
	totalReviews := 0
	totalComments := 0
	for path, group := range groups {
		totalReviews += len(group.ReviewCommentMap)
		totalComments += len(group.CommentMap)
		groupLabel := path
		if groupLabel == "" {
			groupLabel = "general/reviews"
		}
		p.logger.Debug("Group feedback collected",
			zap.String("group", groupLabel),
			zap.Int("reviews", len(group.ReviewCommentMap)),
			zap.Int("comments", len(group.CommentMap)))
	}

	p.logger.Info("Collected feedback",
		zap.Int("groups", len(groups)),
		zap.Int("new_reviews", totalReviews),
		zap.Int("new_comments", totalComments))

	return &GroupedFeedbackData{
		Groups:  groups,
		Summary: summary,
	}
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated
func truncateString(s string, maxLen int) string {
	// Remove newlines for summary
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)

	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// getRepositoryURLFromPR gets the repository URL from the PR details (our fork)
func (p *PRReviewProcessorImpl) getRepositoryURLFromPR(pr *models.GitHubPRDetails) (string, error) {
	// The PR head repo should be our fork
	// If CloneURL is empty, this is likely a legacy PR where the fork was deleted
	if pr.Head.Repo.CloneURL == "" {
		return "", fmt.Errorf("no clone URL found in PR head repository (likely legacy PR with deleted fork)")
	}

	// Return the clone URL as-is, let the GitHub service handle authentication
	// The GitHub service should use the Personal Access Token for authentication
	return pr.Head.Repo.CloneURL, nil
}

// applyFeedbackFixes applies the feedback fixes to the code, processing each file group separately
// TODO: Add test coverage for error paths: AI service errors, type assertion failures, clone/checkout/pull failures
func (p *PRReviewProcessorImpl) applyFeedbackFixes(ticketKey, forkURL string, pr *models.GitHubPRDetails, groupedFeedback *GroupedFeedbackData, owner, repo string, prNumber int) error {
	p.logger.Info("Applying feedback fixes for ticket",
		zap.String("ticket", ticketKey),
		zap.Int("groups", len(groupedFeedback.Groups)))

	// Clone the repository
	repoDir := fmt.Sprintf("%s/%s-feedback", p.config.TempDir, ticketKey)
	err := p.githubService.CloneRepository(forkURL, repoDir)
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	// Clean up repository directory on function exit (success or failure)
	defer func() {
		if err := os.RemoveAll(repoDir); err != nil {
			p.logger.Warn("Failed to clean up repository directory",
				zap.String("repoDir", repoDir),
				zap.Error(err))
		}
	}()

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

	// Get ticket details to get assignee info for co-author
	ticket, err := p.jiraService.GetTicket(ticketKey)
	var coAuthorName, coAuthorEmail string
	if err == nil && ticket.Fields.Assignee != nil {
		coAuthorName = ticket.Fields.Assignee.DisplayName
		coAuthorEmail = ticket.Fields.Assignee.EmailAddress
	}

	// Accumulate all responses from all groups
	allResponses := make(map[string]string)

	// Track skipped groups for logging
	var skippedGroups []string

	// Process each file group separately
	groupCount := 0
	for path, feedbackData := range groupedFeedback.Groups {
		groupCount++
		groupLabel := path
		if groupLabel == "" {
			groupLabel = "general/reviews"
		}

		p.logger.Info("Processing feedback group",
			zap.Int("group", groupCount),
			zap.Int("total_groups", len(groupedFeedback.Groups)),
			zap.String("file_path", groupLabel),
			zap.Int("comments", len(feedbackData.CommentMap)),
			zap.Int("reviews", len(feedbackData.ReviewCommentMap)))

		// Generate a prompt for the AI service to fix the code based on this group's feedback
		prompt := p.generateFeedbackPrompt(pr, feedbackData, groupedFeedback.Summary)

		// Run AI service to generate code fixes and get the AI output
		aiOutput, err := p.aiService.GenerateCode(prompt, repoDir)
		if err != nil {
			return fmt.Errorf("failed to generate code fixes for group '%s': %w", groupLabel, err)
		}

		// Parse individual responses from AI output
		aiOutputStr, ok := aiOutput.(string)
		if !ok {
			return fmt.Errorf("AI output has unexpected type %T for group '%s', expected string", aiOutput, groupLabel)
		}
		if aiOutputStr == "" {
			// Log the prompt (truncated) to help debug why AI didn't respond
			truncatedPrompt := prompt
			if len(prompt) > 500 {
				truncatedPrompt = prompt[:500] + "... (truncated)"
			}
			p.logger.Warn("AI output is empty for group",
				zap.String("group", groupLabel),
				zap.String("prompt_preview", truncatedPrompt))
			skippedGroups = append(skippedGroups, groupLabel)
			continue
		}

		// Build list of expected IDs for this group
		var expectedIDs []string
		for id := range feedbackData.CommentMap {
			expectedIDs = append(expectedIDs, id)
		}
		for id := range feedbackData.ReviewCommentMap {
			expectedIDs = append(expectedIDs, id)
		}
		// Sort for deterministic output in logs and tests
		sort.Strings(expectedIDs)

		// Parse responses for this group
		groupResponses := p.parseCommentResponses(aiOutputStr, expectedIDs)

		// Add to accumulated responses
		for id, response := range groupResponses {
			allResponses[id] = response
		}

		p.logger.Info("Completed processing group",
			zap.String("file_path", groupLabel),
			zap.Int("responses_parsed", len(groupResponses)))
	}

	// Commit all changes from all groups in one commit
	commitMessage := fmt.Sprintf("%s: Apply PR feedback fixes", ticketKey)
	if len(skippedGroups) > 0 {
		// Sort for deterministic commit messages
		sort.Strings(skippedGroups)
		commitMessage = fmt.Sprintf("%s: Apply PR feedback fixes (skipped: %s)", ticketKey, strings.Join(skippedGroups, ", "))
	}
	err = p.githubService.CommitChanges(repoDir, commitMessage, coAuthorName, coAuthorEmail)
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Push the changes to update the original PR
	// Get fork owner from PR head (the branch that was created for the PR)
	forkOwner := pr.Head.Repo.Owner.Login
	err = p.githubService.PushChanges(repoDir, branchName, forkOwner, repo)
	if err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	// Post individual replies to comments/reviews using accumulated responses
	// We need to flatten the grouped feedback back into a single FeedbackData for posting replies
	flattenedFeedback := &FeedbackData{
		CommentMap:       make(map[string]*models.GitHubPRComment),
		ReviewCommentMap: make(map[string]*models.GitHubReview),
	}
	for _, group := range groupedFeedback.Groups {
		for id, comment := range group.CommentMap {
			flattenedFeedback.CommentMap[id] = comment
		}
		for id, review := range group.ReviewCommentMap {
			flattenedFeedback.ReviewCommentMap[id] = review
		}
	}

	successCount, failCount, skippedCount := p.postIndividualReplies(owner, repo, prNumber, pr.Comments, flattenedFeedback, allResponses)

	p.logger.Info("Successfully updated PR with feedback fixes and posted replies",
		zap.Int("pr_number", pr.Number),
		zap.String("ticket", ticketKey),
		zap.Int("groups_processed", len(groupedFeedback.Groups)),
		zap.Int("replies_attempted", len(allResponses)),
		zap.Int("replies_posted", successCount),
		zap.Int("replies_failed", failCount),
		zap.Int("replies_skipped", skippedCount))

	if failCount > 0 {
		p.logger.Warn("Some replies failed to post",
			zap.Int("failed_count", failCount),
			zap.Int("total_attempted", len(allResponses)))
	}

	return nil
}

// isKnownBot checks if a username matches a known bot pattern
func (p *PRReviewProcessorImpl) isKnownBot(username string) bool {
	usernameLower := strings.ToLower(username)
	for _, botUsername := range p.config.GitHub.KnownBotUsernames {
		if strings.ToLower(botUsername) == usernameLower {
			return true
		}
	}
	return false
}

// calculateThreadDepth walks the comment chain backwards and counts how many times our bot appears
func (p *PRReviewProcessorImpl) calculateThreadDepth(
	commentID int64,
	commentByID map[int64]*models.GitHubPRComment,
) int {
	depth := 0
	currentID := commentID
	visited := make(map[int64]bool) // Prevent infinite loops in case of malformed data

	for currentID != 0 {
		// Check for cycles
		if visited[currentID] {
			p.logger.Warn("Detected cycle in comment thread",
				zap.Int64("comment_id", commentID))
			break
		}
		visited[currentID] = true

		comment, found := commentByID[currentID]
		if !found {
			break
		}

		// Count if this comment is from our bot
		if comment.User.Login == p.config.GitHub.BotUsername {
			depth++
		}

		// Move to parent
		currentID = comment.InReplyToID
	}

	return depth
}

// shouldSkipReply determines if we should skip replying to a comment to prevent loops
// Returns (shouldSkip, reason)
func (p *PRReviewProcessorImpl) shouldSkipReply(
	comment *models.GitHubPRComment,
	commentByID map[int64]*models.GitHubPRComment,
) (bool, string) {
	// Check 1: Bot detection - Don't respond to a bot's follow-up to our own comment
	// This prevents bot-to-bot ping-pong while allowing ONE initial exchange for visibility
	if p.isKnownBot(comment.User.Login) && comment.InReplyToID != 0 {
		if parentComment, found := commentByID[comment.InReplyToID]; found {
			if parentComment.User.Login == p.config.GitHub.BotUsername {
				return true, fmt.Sprintf("bot '%s' is replying to our own comment (loop prevention)", comment.User.Login)
			}
		} else {
			// Parent comment not found - be conservative and skip when bot is replying to unknown parent
			// This handles data inconsistencies defensively (missing parent could have been our bot)
			p.logger.Warn("Bot replying to missing parent - skipping as defensive measure",
				zap.Int64("comment_id", comment.ID),
				zap.Int64("parent_id", comment.InReplyToID),
				zap.String("user", comment.User.Login))
			return true, fmt.Sprintf("bot '%s' replying to missing parent %d (defensive skip)", comment.User.Login, comment.InReplyToID)
		}
	}

	// Check 2: Thread depth limit - Don't exceed configured max depth
	// MaxThreadDepth represents the maximum number of bot replies allowed in a thread
	// When threadDepth equals MaxThreadDepth, we've already made the maximum number of replies
	threadDepth := p.calculateThreadDepth(comment.ID, commentByID)
	if threadDepth >= p.config.GitHub.MaxThreadDepth {
		return true, fmt.Sprintf("thread depth %d reaches or exceeds max %d (loop prevention)", threadDepth, p.config.GitHub.MaxThreadDepth)
	}

	return false, ""
}

// postIndividualReplies posts individual replies to comments and reviews
// Returns (successCount, failCount, skippedCount)
func (p *PRReviewProcessorImpl) postIndividualReplies(owner, repo string, prNumber int, allComments []models.GitHubPRComment, feedbackData *FeedbackData, responses map[string]string) (int, int, int) {
	successCount := 0
	failCount := 0
	skippedCount := 0

	// Build lookup map of all comments for thread depth calculation
	commentByID := make(map[int64]*models.GitHubPRComment)
	for i := range allComments {
		commentByID[allComments[i].ID] = &allComments[i]
	}

	// Reply to comments
	for commentID, comment := range feedbackData.CommentMap {
		response, ok := responses[commentID]
		if !ok {
			p.logger.Warn("No AI response found for comment",
				zap.String("comment_id", commentID),
				zap.String("user", comment.User.Login))
			continue
		}

		// Check if we should skip this reply to prevent loops
		shouldSkip, reason := p.shouldSkipReply(comment, commentByID)
		if shouldSkip {
			skippedCount++
			p.logger.Info("Skipping reply to prevent loop",
				zap.String("comment_id", commentID),
				zap.String("user", comment.User.Login),
				zap.String("reason", reason))
			continue
		}

		// Format response with bot marker
		replyBody := fmt.Sprintf("🤖 %s", response)

		// For line-based review comments (have Path), use threaded reply
		if comment.Path != "" && comment.Line > 0 {
			err := p.githubService.ReplyToPRComment(owner, repo, prNumber, comment.ID, replyBody)
			if err != nil {
				failCount++
				p.logger.Error("Failed to reply to line-based comment",
					zap.String("comment_id", commentID),
					zap.Int64("github_comment_id", comment.ID),
					zap.Error(err))
			} else {
				successCount++
				p.logger.Info("Posted reply to line-based comment",
					zap.String("comment_id", commentID),
					zap.String("path", comment.Path),
					zap.Int("line", comment.Line))
			}
		} else {
			// For general conversation comments, post a regular comment with mention
			mentionBody := fmt.Sprintf("@%s %s", comment.User.Login, replyBody)
			err := p.githubService.AddPRComment(owner, repo, prNumber, mentionBody)
			if err != nil {
				failCount++
				p.logger.Error("Failed to post reply to general comment",
					zap.String("comment_id", commentID),
					zap.Error(err))
			} else {
				successCount++
				p.logger.Info("Posted reply to general comment",
					zap.String("comment_id", commentID),
					zap.String("user", comment.User.Login))
			}
		}
	}

	// Reply to reviews (always posted as general comments with mention)
	for reviewID, review := range feedbackData.ReviewCommentMap {
		response, ok := responses[reviewID]
		if !ok {
			p.logger.Warn("No AI response found for review",
				zap.String("review_id", reviewID),
				zap.String("user", review.User.Login))
			continue
		}

		// Format response with bot marker and mention
		replyBody := fmt.Sprintf("@%s 🤖 %s", review.User.Login, response)

		err := p.githubService.AddPRComment(owner, repo, prNumber, replyBody)
		if err != nil {
			failCount++
			p.logger.Error("Failed to post reply to review",
				zap.String("review_id", reviewID),
				zap.Error(err))
		} else {
			successCount++
			p.logger.Info("Posted reply to review",
				zap.String("review_id", reviewID),
				zap.String("user", review.User.Login))
		}
	}

	// Log summary if any replies were skipped
	if skippedCount > 0 {
		p.logger.Info("Skipped replies to prevent loops",
			zap.Int("skipped_count", skippedCount),
			zap.Int("total_attempted", len(responses)))
	}

	return successCount, failCount, skippedCount
}

// generateFeedbackPrompt generates a prompt for the AI service to fix code based on feedback
func (p *PRReviewProcessorImpl) generateFeedbackPrompt(pr *models.GitHubPRDetails, feedbackData *FeedbackData, summary string) string {
	var prompt strings.Builder

	prompt.WriteString("You are a code reviewer and developer. You need to fix the code based on NEW PR review feedback and provide individual responses.\n\n")

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

	// Include summary of previously addressed items if available
	if summary != "" {
		prompt.WriteString("## ")
		prompt.WriteString(summary)
		prompt.WriteString("\n")
	}

	// Include NEW feedback with IDs
	prompt.WriteString(feedbackData.NewFeedback)
	prompt.WriteString("\n")

	prompt.WriteString("## Instructions\n")
	prompt.WriteString("1. Analyze the NEW feedback carefully (marked with COMMENT_X or REVIEW_X IDs)\n")
	prompt.WriteString("2. Apply the necessary fixes to address each piece of feedback\n")
	prompt.WriteString("3. After fixing, provide a brief response (1-3 sentences) for EACH comment/review explaining what you changed\n\n")

	prompt.WriteString("## Response Format\n")
	prompt.WriteString("IMPORTANT: After making your code changes, provide individual responses in this exact format:\n\n")
	prompt.WriteString("For each COMMENT_X or REVIEW_X, include a section like:\n")
	prompt.WriteString("```\n")
	prompt.WriteString("COMMENT_1_RESPONSE:\n")
	prompt.WriteString("Brief 1-3 sentence explanation of what you changed to address this comment.\n\n")
	prompt.WriteString("COMMENT_2_RESPONSE:\n")
	prompt.WriteString("Brief 1-3 sentence explanation of what you changed.\n")
	prompt.WriteString("```\n\n")
	prompt.WriteString("NOTE: Each response should end with a double newline (\\n\\n) to separate it from the next response.\n")
	prompt.WriteString("The parser stops at the first double newline, so keep responses concise (1-3 sentences).\n\n")

	prompt.WriteString("Now please:\n")
	prompt.WriteString("1. Apply all the fixes to the code\n")
	prompt.WriteString("2. Provide individual responses in the format shown above\n")

	return prompt.String()
}

// parseCommentResponses extracts individual comment responses from AI output
// Looks for patterns like "COMMENT_1_RESPONSE:" or "REVIEW_1_RESPONSE:" followed by the response text
// Returns map of comment/review IDs to responses, and logs warnings if parsing issues occur
func (p *PRReviewProcessorImpl) parseCommentResponses(aiOutput string, expectedIDs []string) map[string]string {
	responses := make(map[string]string)

	// Pattern to match COMMENT_1_RESPONSE: or REVIEW_2_RESPONSE:
	// We'll extract the text between each marker using FindAllStringSubmatchIndex
	pattern := regexp.MustCompile(`((?:COMMENT|REVIEW)_\d+)_RESPONSE:\s*`)

	// Find all matches and their positions
	matches := pattern.FindAllStringSubmatchIndex(aiOutput, -1)

	for i, match := range matches {
		// match[2], match[3] are the start and end of the ID capture group
		id := aiOutput[match[2]:match[3]]

		// Start of response text is at match[1] (end of full match)
		responseStart := match[1]

		// End of response text is either:
		// 1. The first double newline (\n\n) indicating end of response paragraph
		// 2. The start of the next _RESPONSE: marker
		// 3. The end of the string
		var responseEnd int
		if i+1 < len(matches) {
			// Find the end: either double newline or next marker (whichever comes first)
			nextMarkerStart := matches[i+1][0]
			doubleNewlineIdx := strings.Index(aiOutput[responseStart:nextMarkerStart], "\n\n")
			if doubleNewlineIdx != -1 {
				responseEnd = responseStart + doubleNewlineIdx
			} else {
				responseEnd = nextMarkerStart
			}
		} else {
			// Last response: find double newline or use end of string
			doubleNewlineIdx := strings.Index(aiOutput[responseStart:], "\n\n")
			if doubleNewlineIdx != -1 {
				responseEnd = responseStart + doubleNewlineIdx
			} else {
				responseEnd = len(aiOutput)
			}
		}

		responseText := strings.TrimSpace(aiOutput[responseStart:responseEnd])

		if responseText != "" {
			responses[id] = responseText
			p.logger.Debug("Parsed AI response",
				zap.String("id", id),
				zap.String("response_preview", truncateString(responseText, 100)))
		} else {
			p.logger.Warn("Parsed empty response for ID",
				zap.String("id", id))
		}
	}

	// Validate that all expected IDs have responses
	missing := []string{}
	for _, expectedID := range expectedIDs {
		if _, found := responses[expectedID]; !found {
			missing = append(missing, expectedID)
		}
	}

	if len(missing) > 0 {
		p.logger.Warn("AI did not provide responses for some comments/reviews",
			zap.Strings("missing_ids", missing),
			zap.Int("total_expected", len(expectedIDs)),
			zap.Int("total_found", len(responses)),
			zap.String("ai_output_preview", truncateString(aiOutput, 500)))
	}

	p.logger.Info("Parsed AI responses",
		zap.Int("count", len(responses)),
		zap.Int("expected", len(expectedIDs)))

	return responses
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
