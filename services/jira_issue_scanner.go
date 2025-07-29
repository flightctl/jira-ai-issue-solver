package services

import (
	"fmt"
	"strings"
	"time"

	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

// JiraIssueScannerService defines the interface for the Jira issue scanner
type JiraIssueScannerService interface {
	// Start starts the periodic scanning
	Start()
	// Stop stops the periodic scanning
	Stop()
}

// JiraIssueScannerServiceImpl implements the JiraIssueScannerService interface
type JiraIssueScannerServiceImpl struct {
	jiraService     JiraService
	githubService   GitHubService
	aiService       AIService
	ticketProcessor TicketProcessor
	config          *models.Config
	logger          *zap.Logger
	stopChan        chan struct{}
	isRunning       bool
}

// NewJiraIssueScannerService creates a new JiraIssueScannerService
func NewJiraIssueScannerService(
	jiraService JiraService,
	githubService GitHubService,
	aiService AIService,
	config *models.Config,
	logger *zap.Logger,
) JiraIssueScannerService {
	ticketProcessor := NewTicketProcessor(jiraService, githubService, aiService, config, logger)

	return &JiraIssueScannerServiceImpl{
		jiraService:     jiraService,
		githubService:   githubService,
		aiService:       aiService,
		ticketProcessor: ticketProcessor,
		config:          config,
		logger:          logger,
		stopChan:        make(chan struct{}),
		isRunning:       false,
	}
}

// Start starts the periodic scanning
func (s *JiraIssueScannerServiceImpl) Start() {
	if s.isRunning {
		s.logger.Info("Jira issue scanner is already running")
		return
	}

	s.isRunning = true
	s.logger.Info("Starting Jira issue scanner...")

	go func() {
		ticker := time.NewTicker(time.Duration(s.config.Jira.IntervalSeconds) * time.Second)
		defer ticker.Stop()

		// Run initial scan immediately
		s.scanForTickets()

		for {
			select {
			case <-ticker.C:
				s.scanForTickets()
			case <-s.stopChan:
				s.logger.Info("Stopping Jira issue scanner...")
				return
			}
		}
	}()
}

// Stop stops the periodic scanning
func (s *JiraIssueScannerServiceImpl) Stop() {
	if !s.isRunning {
		return
	}

	s.isRunning = false
	close(s.stopChan)
}

// buildTodoStatusJQL builds a JQL query that searches for tickets across all configured ticket types
// and their respective todo statuses
func (s *JiraIssueScannerServiceImpl) buildTodoStatusJQL() string {
	var conditions []string

	// Iterate through all configured ticket types and their status transitions
	for ticketType, transitions := range s.config.Jira.StatusTransitions {
		// Create condition for this ticket type and its todo status
		condition := fmt.Sprintf(`(issuetype = "%s" AND status = "%s")`, ticketType, transitions.Todo)
		conditions = append(conditions, condition)
	}

	// Build the final JQL query
	var jql string
	if len(conditions) == 1 {
		jql = fmt.Sprintf(`Contributors = currentUser() AND %s ORDER BY updated DESC`, conditions[0])
	} else {
		// Join multiple conditions with OR
		orConditions := strings.Join(conditions, " OR ")
		jql = fmt.Sprintf(`Contributors = currentUser() AND (%s) ORDER BY updated DESC`, orConditions)
	}

	s.logger.Debug("Generated JQL query", zap.String("jql", jql))
	return jql
}

// scanForTickets searches for tickets that need AI processing
func (s *JiraIssueScannerServiceImpl) scanForTickets() {
	s.logger.Info("Scanning for tickets that need AI processing...")

	// Log current configuration for debugging
	s.logger.Debug("Current status transitions configuration",
		zap.Any("status_transitions", s.config.Jira.StatusTransitions))

	// Build dynamic JQL query based on all configured ticket types and their todo statuses
	jql := s.buildTodoStatusJQL()

	searchResponse, err := s.jiraService.SearchTickets(jql)
	if err != nil {
		s.logger.Error("Failed to search for tickets", zap.Error(err))
		return
	}

	if searchResponse.Total == 0 {
		s.logger.Info("No tickets found that need AI processing")
		return
	}

	s.logger.Info("Found tickets that need AI processing", zap.Int("count", searchResponse.Total))

	// Process each ticket
	for _, issue := range searchResponse.Issues {
		s.logger.Info("Found ticket", zap.String("ticket", issue.Key))

		// Process all tickets returned by the search

		// Process the ticket asynchronously
		go s.ticketProcessor.ProcessTicket(issue.Key)
	}
}
