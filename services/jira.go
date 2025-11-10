package services

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

const (
	// Retry configuration constants
	maxRetries              = 2
	defaultRetryWaitSeconds = 5
	maxRetryWaitSeconds     = 60 // Cap at 1 minute to prevent excessive waits

	// Exponential backoff configuration (used when Retry-After is 0 or missing)
	initialBackoffSeconds = 1   // Initial backoff duration
	maxBackoffSeconds     = 16  // Maximum backoff before jitter
	maxJitterSeconds      = 1.0 // Maximum jitter to add (in seconds)

	// Response body truncation for logging and errors
	maxBodyLogLength   = 500 // Max chars to log in debug
	maxBodyErrorLength = 200 // Max chars to include in error messages
)

// JiraService defines the interface for interacting with Jira
type JiraService interface {
	// GetTicket fetches a ticket from Jira
	GetTicket(key string) (*models.JiraTicketResponse, error)

	// GetTicketWithExpandedFields fetches a ticket from Jira with expanded fields for custom field access
	GetTicketWithExpandedFields(key string) (map[string]interface{}, map[string]string, error)

	// UpdateTicketLabels updates the labels of a ticket
	UpdateTicketLabels(key string, addLabels, removeLabels []string) error

	// UpdateTicketStatus updates the status of a ticket
	UpdateTicketStatus(key string, status string) error

	// UpdateTicketField updates a specific field of a ticket
	UpdateTicketField(key string, fieldID string, value interface{}) error

	// UpdateTicketFieldByName updates a specific field of a ticket by field name
	UpdateTicketFieldByName(key string, fieldName string, value interface{}) error

	// GetFieldIDByName resolves a field name to its ID
	GetFieldIDByName(fieldName string) (string, error)

	// AddComment adds a comment to a ticket
	AddComment(key string, comment string) error

	// GetTicketWithComments fetches a ticket from Jira with comments expanded
	GetTicketWithComments(key string) (*models.JiraTicketResponse, error)

	// SearchTickets searches for tickets using JQL
	SearchTickets(jql string) (*models.JiraSearchResponse, error)

	// HasSecurityLevel checks if a ticket has a security level set (other than "None")
	HasSecurityLevel(key string) (bool, error)

	// GetTicketSecurityLevel gets the security level of a ticket
	GetTicketSecurityLevel(key string) (*models.JiraSecurity, error)
}

// truncate truncates a string to a maximum length
func truncate(body []byte, maxLen int) string {
	bodyStr := string(body)
	if len(bodyStr) > maxLen {
		return bodyStr[:maxLen] + fmt.Sprintf("... (truncated, total: %d chars)", len(bodyStr))
	}
	return bodyStr
}

// truncateForLogging truncates response body for debug logging
func truncateForLogging(body []byte) string {
	return truncate(body, maxBodyLogLength)
}

// truncateForError truncates response body for error messages
func truncateForError(body []byte) string {
	return truncate(body, maxBodyErrorLength)
}

// randomJitter generates a random jitter value between 0 and maxSeconds
func randomJitter(maxSeconds float64) (float64, error) {
	var randomBytes [8]byte

	_, err := rand.Read(randomBytes[:])
	if err != nil {
		return 0, fmt.Errorf("failed to generate random jitter: %w", err)
	}
	// Convert random bytes to uint64, then normalize to [0, 1) range
	randomUint64 := binary.BigEndian.Uint64(randomBytes[:])
	normalized := float64(randomUint64) / float64(^uint64(0))

	return normalized * maxSeconds, nil
}

// JiraServiceImpl implements the JiraService interface
type JiraServiceImpl struct {
	config   *models.Config
	client   *http.Client
	executor models.CommandExecutor
	logger   *zap.Logger
	sleepFn  func(time.Duration) <-chan time.Time // Returns a channel for select-based waiting
}

// NewJiraService creates a new JiraService with production defaults
func NewJiraService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) JiraService {
	return NewJiraServiceForTest(config, &http.Client{}, logger, time.After, executor...)
}

// NewJiraServiceForTest creates a new JiraService with a custom sleep function for testing
func NewJiraServiceForTest(config *models.Config, client *http.Client, logger *zap.Logger, sleepFn func(time.Duration) <-chan time.Time, executor ...models.CommandExecutor) *JiraServiceImpl {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}
	return &JiraServiceImpl{
		config:   config,
		client:   client,
		executor: commandExecutor,
		logger:   logger,
		sleepFn:  sleepFn,
	}
}

// doOperation makes a request to Jira and handles rate limiting errors.
// It returns the response body or an error on failure.
func (s *JiraServiceImpl) doOperation(
	operation string,
	url string,
	bodyReader io.Reader,
	okStatusCodes ...int,
) ([]byte, error) {
	s.logger.Debug("Doing operation", zap.String("operation", operation), zap.String("url", url))

	// Buffer the request body once so it can be retried
	var requestBody []byte
	if bodyReader != nil {
		var err error
		requestBody, err = io.ReadAll(bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Create fresh reader for each attempt
		var bodyForRequest io.Reader
		if requestBody != nil {
			bodyForRequest = bytes.NewReader(requestBody)
		}

		req, err := http.NewRequest(operation, url, bodyForRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s request: %w", operation, err)
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.config.Jira.APIToken))
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send %s request: %w", operation, err)
		}

		// Read the body and close immediately so we can retry if needed
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(closeErr), zap.String("operation", operation), zap.String("url", url))
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response body: %w", readErr)
		}

		for _, okStatusCode := range okStatusCodes {
			// Success case
			if resp.StatusCode == okStatusCode {
				s.logger.Debug("Operation successful", zap.String("operation", operation), zap.String("url", url), zap.Int("status_code", resp.StatusCode))
				s.logger.Debug("Response body", zap.String("body", truncateForLogging(body)))
				return body, nil
			}
		}

		// Handle rate limiting with retry
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			s.logger.Debug("Response headers:")
			for key, values := range resp.Header {
				s.logger.Debug("Header", zap.String("key", key), zap.Strings("values", values))
			}

			var waitDuration time.Duration
			useExponentialBackoff := false

			if retryAfterHeader := resp.Header.Get("Retry-After"); retryAfterHeader != "" {
				// Parse Retry-After header (Jira returns it as seconds)
				if parsed, err := strconv.Atoi(retryAfterHeader); err == nil && parsed > 0 {
					// Cap the retry wait time to prevent excessive delays
					retrySeconds := parsed
					if parsed > maxRetryWaitSeconds {
						s.logger.Warn("Retry-After exceeds maximum, capping to max",
							zap.Int("requested_seconds", parsed),
							zap.Int("capped_to_seconds", maxRetryWaitSeconds))
						retrySeconds = maxRetryWaitSeconds
					}
					waitDuration = time.Duration(retrySeconds) * time.Second
					s.logger.Info("Rate limited by Jira, using Retry-After header",
						zap.Int("retry_after_seconds", retrySeconds))
				} else {
					// Retry-After is 0, unparseable, or invalid - use exponential backoff
					useExponentialBackoff = true
					if err != nil {
						s.logger.Warn("Failed to parse Retry-After header, using exponential backoff",
							zap.String("retry_after", retryAfterHeader),
							zap.Error(err))
					} else {
						s.logger.Info("Retry-After is zero, using exponential backoff instead",
							zap.String("retry_after", retryAfterHeader))
					}
				}
			} else {
				// No Retry-After header - use exponential backoff
				useExponentialBackoff = true
				s.logger.Info("Rate limited without Retry-After header, using exponential backoff")
			}

			if useExponentialBackoff {
				// Calculate exponential backoff: initialBackoff * 2^(attempt-1)
				// attempt starts at 1, so for first retry (attempt=1): 1 * 2^0 = 1 second
				backoffSeconds := initialBackoffSeconds * (1 << (attempt - 1))
				if backoffSeconds > maxBackoffSeconds {
					backoffSeconds = maxBackoffSeconds
				}

				// Add jitter: random value between 0 and maxJitterSeconds
				jitter, err := randomJitter(maxJitterSeconds)
				if err != nil {
					s.logger.Warn("Failed to generate secure jitter, using 0",
						zap.Error(err))
					jitter = 0
				}
				totalSeconds := float64(backoffSeconds) + jitter

				waitDuration = time.Duration(totalSeconds * float64(time.Second))
				s.logger.Info("Calculated exponential backoff with jitter",
					zap.Int("attempt", attempt),
					zap.Int("backoff_seconds", backoffSeconds),
					zap.Float64("jitter_seconds", jitter),
					zap.Duration("total_wait", waitDuration))
			}

			s.logger.Info("Rate limited by Jira, retrying after delay",
				zap.String("operation", operation),
				zap.String("url", url),
				zap.Int("attempt", attempt),
				zap.Duration("wait_duration", waitDuration))

			// Wait using channel-based approach (compatible with future context support)
			// When adding context: wrap in select with case <-ctx.Done()
			<-s.sleepFn(waitDuration)

			continue // Retry the request
		}

		// All other error cases - truncate body to avoid huge error messages
		return nil, fmt.Errorf("failed to %s %s: status_code=%d, body=%s",
			operation, url, resp.StatusCode, truncateForError(body))
	}

	return nil, fmt.Errorf("failed to %s %s after %d retries", operation, url, maxRetries)
}

// doGet is a helper function to make a GET request to Jira and process any rate limiting errors
func (s *JiraServiceImpl) doGet(url string) ([]byte, error) {
	return s.doOperation("GET", url, nil, http.StatusOK)
}

func (s *JiraServiceImpl) doPut(url string, bodyReader io.Reader) ([]byte, error) {
	return s.doOperation("PUT", url, bodyReader, http.StatusNoContent, http.StatusOK)
}

func (s *JiraServiceImpl) doPost(url string, bodyReader io.Reader) ([]byte, error) {
	return s.doOperation("POST", url, bodyReader, http.StatusNoContent, http.StatusCreated, http.StatusOK)
}

// GetTicket fetches a ticket from Jira
func (s *JiraServiceImpl) GetTicket(key string) (*models.JiraTicketResponse, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s", s.config.Jira.BaseURL, key)

	body, err := s.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket, err: %w", err)
	}

	var ticket models.JiraTicketResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&ticket); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &ticket, nil
}

// GetTicketWithExpandedFields fetches a ticket from Jira with expanded fields for custom field access
func (s *JiraServiceImpl) GetTicketWithExpandedFields(key string) (map[string]interface{}, map[string]string, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s?expand=names", s.config.Jira.BaseURL, key)

	body, err := s.doGet(url)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get ticket with expanded fields, err: %w", err)
	}

	var ticketWithFields struct {
		Fields map[string]interface{} `json:"fields"`
		Names  map[string]string      `json:"names"`
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&ticketWithFields); err != nil {
		return nil, nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return ticketWithFields.Fields, ticketWithFields.Names, nil
}

// GetTicketWithComments fetches a ticket from Jira with comments expanded
func (s *JiraServiceImpl) GetTicketWithComments(key string) (*models.JiraTicketResponse, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s?expand=comment", s.config.Jira.BaseURL, key)

	body, err := s.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket with comments, err: %w", err)
	}

	var ticket models.JiraTicketResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&ticket); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &ticket, nil
}

// UpdateTicketLabels updates the labels of a ticket
func (s *JiraServiceImpl) UpdateTicketLabels(key string, addLabels, removeLabels []string) error {
	// First, get the current labels
	ticket, err := s.GetTicket(key)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	// Create a map of current labels for easy lookup
	currentLabels := make(map[string]bool)
	for _, label := range ticket.Fields.Labels {
		currentLabels[label] = true
	}

	// Remove labels
	for _, label := range removeLabels {
		delete(currentLabels, label)
	}

	// Add labels
	for _, label := range addLabels {
		currentLabels[label] = true
	}

	// Convert map back to slice
	labels := make([]string, 0, len(currentLabels))
	for label := range currentLabels {
		labels = append(labels, label)
	}

	// Update the ticket
	url := fmt.Sprintf("%s/rest/api/2/issue/%s", s.config.Jira.BaseURL, key)

	payload := map[string]interface{}{
		"fields": map[string]interface{}{
			"labels": labels,
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	if _, err := s.doPut(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update ticket labels: %w", err)
	}

	return nil
}

// UpdateTicketStatus updates the status of a ticket
func (s *JiraServiceImpl) UpdateTicketStatus(key string, status string) error {
	// Get available transitions
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/transitions", s.config.Jira.BaseURL, key)

	body, err := s.doGet(url)
	if err != nil {
		return fmt.Errorf("failed to get transitions, err: %w", err)
	}

	var transitions struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&transitions); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Find the transition ID for the target status
	var transitionID string
	for _, transition := range transitions.Transitions {
		if strings.EqualFold(transition.To.Name, status) {
			transitionID = transition.ID
			break
		}
	}

	if transitionID == "" {
		return fmt.Errorf("no transition found for status: %s", status)
	}

	// Perform the transition
	payload := map[string]interface{}{
		"transition": map[string]string{
			"id": transitionID,
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	if _, err := s.doPost(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update ticket status: %w", err)
	}

	return nil
}

// AddComment adds a comment to a ticket
func (s *JiraServiceImpl) AddComment(key string, comment string) error {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", s.config.Jira.BaseURL, key)

	payload := map[string]string{
		"body": comment,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal add comment payload: %w", err)
	}

	if _, err := s.doPost(url, bytes.NewReader(jsonPayload)); err != nil {
		return fmt.Errorf("failed to add comment: %w", err)
	}

	return nil
}

// UpdateTicketField updates a specific field of a ticket
func (s *JiraServiceImpl) UpdateTicketField(key string, fieldID string, value interface{}) error {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s", s.config.Jira.BaseURL, key)

	payload := map[string]interface{}{
		"fields": map[string]interface{}{
			fieldID: value,
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	if _, err := s.doPut(url, bytes.NewBuffer(jsonPayload)); err != nil {
		return fmt.Errorf("failed to update ticket field: %w", err)
	}

	return nil
}

// UpdateTicketFieldByName updates a specific field of a ticket by field name
func (s *JiraServiceImpl) UpdateTicketFieldByName(key string, fieldName string, value interface{}) error {
	fieldID, err := s.GetFieldIDByName(fieldName)
	if err != nil {
		return fmt.Errorf("failed to resolve field name '%s' to ID: %w", fieldName, err)
	}
	return s.UpdateTicketField(key, fieldID, value)
}

// GetFieldIDByName resolves a field name to its ID
func (s *JiraServiceImpl) GetFieldIDByName(fieldName string) (string, error) {
	url := fmt.Sprintf("%s/rest/api/2/field", s.config.Jira.BaseURL)

	body, err := s.doGet(url)
	if err != nil {
		return "", fmt.Errorf("failed to get fields, err: %w", err)
	}

	var fields []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&fields); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Search for the field by name
	for _, field := range fields {
		if field.Name == fieldName {
			return field.ID, nil
		}
	}

	return "", fmt.Errorf("field with name '%s' not found", fieldName)
}

// SearchTickets searches for tickets using JQL
func (s *JiraServiceImpl) SearchTickets(jql string) (*models.JiraSearchResponse, error) {
	url := fmt.Sprintf("%s/rest/api/2/search", s.config.Jira.BaseURL)

	payload := map[string]interface{}{
		"jql":        jql,
		"startAt":    0,
		"maxResults": 100,
		"fields":     []string{"summary", "description", "status", "project", "components", "labels", "created", "updated", "creator", "reporter"},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	body, err := s.doPost(url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to search tickets: %w", err)
	}

	var searchResponse models.JiraSearchResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&searchResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &searchResponse, nil
}

// HasSecurityLevel checks if a ticket has a security level set (other than "None")
func (s *JiraServiceImpl) HasSecurityLevel(key string) (bool, error) {
	security, err := s.GetTicketSecurityLevel(key)
	if err != nil {
		return false, err
	}

	// Consider ticket secure if security level exists and is not "None" or empty
	return security != nil && security.Name != "" && strings.ToLower(security.Name) != "none", nil
}

// GetTicketSecurityLevel gets the security level of a ticket
func (s *JiraServiceImpl) GetTicketSecurityLevel(key string) (*models.JiraSecurity, error) {
	// First try the standard fields API
	ticket, err := s.GetTicket(key)
	if err != nil {
		return nil, err
	}

	if ticket.Fields.Security != nil {
		return ticket.Fields.Security, nil
	}

	// If not found in standard fields, try expanded fields API
	fields, names, err := s.GetTicketWithExpandedFields(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket with expanded fields: %w", err)
	}

	// Look for security field by name mapping
	var securityFieldID string
	for fieldID, fieldName := range names {
		if strings.ToLower(fieldName) == "security level" || strings.ToLower(fieldName) == "security" {
			securityFieldID = fieldID
			break
		}
	}

	if securityFieldID == "" {
		// Security field not found - assume no security level
		return nil, nil
	}

	// Extract security level from expanded fields
	if securityValue, ok := fields[securityFieldID]; ok && securityValue != nil {
		// Handle different possible formats of security field
		switch v := securityValue.(type) {
		case map[string]interface{}:
			security := &models.JiraSecurity{}
			if id, ok := v["id"].(string); ok {
				security.ID = id
			}
			if name, ok := v["name"].(string); ok {
				security.Name = name
			}
			if desc, ok := v["description"].(string); ok {
				security.Description = desc
			}
			return security, nil
		case string:
			// Sometimes just the name is returned
			return &models.JiraSecurity{Name: v}, nil
		}
	}

	return nil, nil
}
