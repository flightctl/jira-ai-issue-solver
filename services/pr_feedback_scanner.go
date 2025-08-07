package services

import (
	"fmt"
	"strings"
	"time"

	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

// PRFeedbackScannerService defines the interface for scanning tickets in "In Review" status
type PRFeedbackScannerService interface {
	// Start starts the periodic scanning for PR feedback
	Start()
	// Stop stops the periodic scanning
	Stop()
}

// PRFeedbackScannerServiceImpl implements the PRFeedbackScannerService interface
type PRFeedbackScannerServiceImpl struct {
	jiraService       JiraService
	githubService     GitHubService
	aiService         AIService
	prReviewProcessor PRReviewProcessor
	config            *models.Config
	logger            *zap.Logger
	stopChan          chan struct{}
	isRunning         bool
}

// NewPRFeedbackScannerService creates a new PRFeedbackScannerService
func NewPRFeedbackScannerService(
	jiraService JiraService,
	githubService GitHubService,
	aiService AIService,
	config *models.Config,
	logger *zap.Logger,
) PRFeedbackScannerService {
	prReviewProcessor := NewPRReviewProcessor(jiraService, githubService, aiService, config, logger)

	return &PRFeedbackScannerServiceImpl{
		jiraService:       jiraService,
		githubService:     githubService,
		aiService:         aiService,
		prReviewProcessor: prReviewProcessor,
		config:            config,
		logger:            logger,
		stopChan:          make(chan struct{}),
		isRunning:         false,
	}
}

// Start starts the periodic scanning for PR feedback
func (s *PRFeedbackScannerServiceImpl) Start() {
	if s.isRunning {
		s.logger.Info("PR feedback scanner is already running")
		return
	}

	s.isRunning = true
	s.logger.Info("Starting PR feedback scanner...")

	go func() {
		ticker := time.NewTicker(time.Duration(s.config.Jira.IntervalSeconds) * time.Second)
		defer ticker.Stop()

		// Run initial scan immediately
		s.scanForPRFeedback()

		for {
			select {
			case <-ticker.C:
				s.scanForPRFeedback()
			case <-s.stopChan:
				s.logger.Info("Stopping PR feedback scanner...")
				return
			}
		}
	}()
}

// Stop stops the periodic scanning
func (s *PRFeedbackScannerServiceImpl) Stop() {
	if !s.isRunning {
		return
	}

	s.isRunning = false
	close(s.stopChan)
}

// buildInReviewStatusJQL builds a JQL query that searches for tickets across all configured ticket types
// and their respective "In Review" statuses across all projects
func (s *PRFeedbackScannerServiceImpl) buildInReviewStatusJQL() string {
	var conditions []string
	var allProjectKeys []string

	// Iterate through all configured projects
	for _, project := range s.config.Jira.Projects {
		// Add project keys to the list
		allProjectKeys = append(allProjectKeys, project.ProjectKeys...)

		// Iterate through all configured ticket types and their status transitions for this project
		for ticketType, transitions := range project.StatusTransitions {
			// Create condition for this ticket type and its in_review status
			condition := fmt.Sprintf(`(issuetype = "%s" AND status = "%s")`, ticketType, transitions.InReview)

			// Only add if not already present (avoid duplicates across projects)
			found := false
			for _, existing := range conditions {
				if existing == condition {
					found = true
					break
				}
			}
			if !found {
				conditions = append(conditions, condition)
			}
		}
	}

	// Build the base JQL query
	orConditions := strings.Join(conditions, " OR ")

	// Include all tickets in review status - we'll filter by PR URLs later
	jql := fmt.Sprintf(`Contributors = currentUser() AND (%s)`, orConditions)

	// Add Git Pull Request field check - we need to have a PR field populated to process feedback
	gitPRFieldNames := make(map[string]bool)
	for _, project := range s.config.Jira.Projects {
		if project.GitPullRequestFieldName != "" {
			gitPRFieldNames[project.GitPullRequestFieldName] = true
		}
	}

	// Add Git Pull Request field filter if configured
	if len(gitPRFieldNames) > 0 {
		var gitPRConditions []string
		for fieldName := range gitPRFieldNames {
			gitPRConditions = append(gitPRConditions, fmt.Sprintf(`"%s" IS NOT EMPTY`, fieldName))
		}
		gitPRFilter := strings.Join(gitPRConditions, " OR ")
		// Only add parentheses if we have multiple conditions
		if len(gitPRConditions) > 1 {
			jql = fmt.Sprintf(`%s AND (%s)`, jql, gitPRFilter)
		} else {
			jql = fmt.Sprintf(`%s AND %s`, jql, gitPRFilter)
		}
	}

	// Add project key filtering (mandatory) - include all project keys from all projects
	if len(allProjectKeys) > 0 {
		projectConditions := make([]string, len(allProjectKeys))
		for i, projectKey := range allProjectKeys {
			projectConditions[i] = fmt.Sprintf(`project = "%s"`, strings.TrimSpace(projectKey))
		}
		projectFilter := strings.Join(projectConditions, " OR ")
		jql = fmt.Sprintf(`%s AND (%s)`, jql, projectFilter)
	}

	// Add ordering
	jql = fmt.Sprintf(`%s ORDER BY updated DESC`, jql)

	s.logger.Debug("Generated JQL query for PR feedback", zap.String("jql", jql))
	return jql
}

// scanForPRFeedback searches for tickets in "In Review" status that need PR feedback processing
func (s *PRFeedbackScannerServiceImpl) scanForPRFeedback() {
	s.logger.Info("Scanning for tickets in 'In Review' status that need PR feedback processing...")

	// Log current configuration for debugging
	s.logger.Debug("Current projects configuration",
		zap.Any("projects", s.config.Jira.Projects))

	// Build dynamic JQL query based on all configured ticket types and their in_review statuses
	jql := s.buildInReviewStatusJQL()

	searchResponse, err := s.jiraService.SearchTickets(jql)
	if err != nil {
		s.logger.Error("Failed to search for tickets in 'In Review' status", zap.Error(err))
		return
	}

	if searchResponse.Total == 0 {
		s.logger.Info("No tickets found in 'In Review' status that need PR feedback processing")
		return
	}

	s.logger.Info("Found tickets in 'In Review' status that need PR feedback processing", zap.Int("count", searchResponse.Total))

	// Process each ticket
	for _, issue := range searchResponse.Issues {
		s.logger.Info("Found ticket in 'In Review' status", zap.String("ticket", issue.Key))

		// Process the ticket asynchronously
		go func(ticketKey string) {
			if err := s.prReviewProcessor.ProcessPRReviewFeedback(ticketKey); err != nil {
				s.logger.Error("Failed to process PR feedback for ticket", zap.String("ticket", ticketKey), zap.Error(err))
			}
		}(issue.Key)
	}
}
