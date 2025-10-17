package services

import (
	"fmt"
	"strings"
	"sync"
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
	jiraService       JiraService
	aiService         AIService
	ticketProcessor   TicketProcessor
	config            *models.Config
	logger            *zap.Logger
	stopChan          chan struct{}
	isRunning         bool
	processingTickets sync.Map // map[string]bool to track tickets currently being processed
}

// NewJiraIssueScannerService creates a new JiraIssueScannerService
func NewJiraIssueScannerService(
	jiraService JiraService,
	aiService AIService,
	ticketProcessor TicketProcessor,
	config *models.Config,
	logger *zap.Logger,
) JiraIssueScannerService {
	return &JiraIssueScannerServiceImpl{
		jiraService:     jiraService,
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

	// Clear any remaining processing tickets to prevent memory leaks
	s.processingTickets.Range(func(key, value interface{}) bool {
		s.processingTickets.Delete(key)
		return true
	})
}

// buildTodoStatusJQL builds a JQL query that searches for tickets across all configured ticket types
// and their respective todo statuses across all projects
func (s *JiraIssueScannerServiceImpl) buildTodoStatusJQL() string {
	var conditions []string
	var allProjectKeys []string

	// Iterate through all configured projects
	for _, project := range s.config.Jira.Projects {
		// Add project keys to the list
		allProjectKeys = append(allProjectKeys, project.ProjectKeys...)

		// Iterate through all configured ticket types and their status transitions for this project
		for ticketType, transitions := range project.StatusTransitions {
			// Create condition for this ticket type and its todo status
			condition := fmt.Sprintf(`(issuetype = "%s" AND status = "%s")`, ticketType, transitions.Todo)

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
	jql := fmt.Sprintf(`Contributors = currentUser() AND (%s)`, orConditions)

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

	s.logger.Debug("Generated JQL query", zap.String("jql", jql))
	return jql
}

// scanForTickets searches for tickets that need AI processing
func (s *JiraIssueScannerServiceImpl) scanForTickets() {
	s.logger.Info("Scanning for tickets that need AI processing...")

	// Log current configuration for debugging
	s.logger.Debug("Current projects configuration",
		zap.Any("projects", s.config.Jira.Projects))

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

		// Check if this ticket is already being processed
		if _, isProcessing := s.processingTickets.Load(issue.Key); isProcessing {
			s.logger.Info("Ticket is already being processed, skipping", zap.String("ticket", issue.Key))
			continue
		}

		// Mark this ticket as being processed
		s.processingTickets.Store(issue.Key, true)

		// Process the ticket asynchronously
		go func(ticketKey string) {
			defer func() {
				// Remove from processing map when done
				s.processingTickets.Delete(ticketKey)
			}()

			err := s.ticketProcessor.ProcessTicket(ticketKey)
			if err != nil {
				s.logger.Error("Failed to process ticket", zap.String("ticket", ticketKey), zap.Error(err))
			}
		}(issue.Key)
	}
}
