package services

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"
)

func TestTicketProcessor_ProcessTicket(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create mock services
	mockJiraService := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Test ticket",
					Description: "Test description",
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
					},
				},
			}, nil
		},
		GetFieldIDByNameFunc: func(fieldName string) (string, error) {
			return "customfield_10001", nil
		},
	}
	mockGitHubService := &mocks.MockGitHubService{
		CreatePullRequestFunc: func(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
			return &models.GitHubCreatePRResponse{
				ID:      1,
				Number:  1,
				State:   "open",
				Title:   title,
				Body:    body,
				HTMLURL: "https://github.com/example/repo/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/mockuser/frontend.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/mockuser/frontend.git", nil
		},
		HasChangesFunc: func(directory string) (bool, error) {
			return true, nil // Mock AI generates changes
		},
	}
	mockClaudeService := &mocks.MockClaudeService{}

	// Create config
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"default": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
			},
		},
	}
	config.TempDir = "/tmp/test"
	config.AI.MaxRetries = 5
	config.AI.RetryDelaySeconds = 2

	// Create ticket processor
	processor := NewTicketProcessor(mockJiraService, mockGitHubService, mockClaudeService, config, logger)

	// Test processing a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

func TestTicketProcessor_CreatePullRequestHeadFormat(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Test that the pull request creation uses the correct head format
	config := &models.Config{}
	config.GitHub.BotUsername = "test-bot"
	config.GitHub.BotEmail = "test@example.com"
	config.GitHub.AppID = 123456
	config.GitHub.PRLabel = "ai-pr"
	config.TempDir = "/tmp"
	config.Jira.BaseURL = "https://your-domain.atlassian.net"
	config.Jira.AssigneeToGitHubUsername = map[string]string{
		"test@example.com": "test-user",
	}
	config.AI.GenerateDocumentation = false
	config.AI.MaxRetries = 5
	config.AI.RetryDelaySeconds = 2
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"TEST"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"default": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
			},
			DisableErrorComments: true,
		},
	}

	// Create mock services with captured values
	var capturedHead, capturedCommitMessage, capturedPRTitle, capturedPRBody string

	mockGitHub := &mocks.MockGitHubService{
		CommitChangesFunc: func(directory, message string, coAuthorName, coAuthorEmail string) error {
			capturedCommitMessage = message
			return nil
		},
		CommitChangesViaAPIFunc: func(owner, repo, branchName, commitMessage, repoDir, coAuthorName, coAuthorEmail string) (string, error) {
			capturedCommitMessage = commitMessage
			return "abc123", nil
		},
		CreatePullRequestFunc: func(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
			capturedHead = head
			capturedPRTitle = title
			capturedPRBody = body
			return &models.GitHubCreatePRResponse{
				ID:      1,
				Number:  1,
				State:   "open",
				Title:   title,
				Body:    body,
				HTMLURL: "https://github.com/example/repo/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/test-user/frontend.git", nil
		},
		CheckForkExistsFunc: func(forkOwner, repo string) (exists bool, cloneURL string, err error) {
			// Always return true with the appropriate fork URL
			return true, "https://github.com/" + forkOwner + "/" + repo + ".git", nil
		},
		CheckForkExistsForUserFunc: func(owner, repo, forkOwner string) (bool, error) {
			// Always return true to indicate the fork exists
			return true, nil
		},
		GetInstallationIDForRepoFunc: func(owner, repo string) (int64, error) {
			return 12345, nil
		},
		CloneRepositoryFunc: func(cloneURL, directory string) error {
			return nil
		},
		CreateBranchFunc: func(directory, branchName string) error {
			return nil
		},
		SwitchToBranchFunc: func(directory, branchName string) error {
			return nil
		},
		PushChangesFunc: func(directory, branchName, forkOwner, repo string) error {
			return nil
		},
		HasChangesFunc: func(directory string) (bool, error) {
			return true, nil // Mock AI generates changes
		},
	}
	mockJira := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Test ticket",
					Description: "Test description",
					IssueType: models.JiraIssueType{
						Name: "Bug",
					},
					Assignee: &models.JiraUser{
						DisplayName:  "Test User",
						EmailAddress: "test@example.com",
					},
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
					},
				},
			}, nil
		},
		GetFieldIDByNameFunc: func(fieldName string) (string, error) {
			return "customfield_10001", nil
		},
		UpdateTicketStatusFunc: func(key, status string) error {
			return nil
		},
		UpdateTicketFieldFunc: func(key, fieldID string, value interface{}) error {
			return nil
		},
		HasSecurityLevelFunc: func(key string) (bool, error) {
			return false, nil
		},
	}
	mockAI := &mocks.MockClaudeService{
		GenerateCodeFunc: func(prompt, repoDir string) (*models.ClaudeResponse, error) {
			return &models.ClaudeResponse{Result: "Code generated successfully"}, nil
		},
	}

	processor := NewTicketProcessor(mockJira, mockGitHub, mockAI, config, logger)

	// Process a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that the head parameter was formatted correctly
	// Format is: forkOwner:branchName where branchName is botUsername/ticketKey
	expectedHead := "test-user:test-bot/TEST-123"
	if capturedHead != expectedHead {
		t.Errorf("Expected head to be '%s', got '%s'", expectedHead, capturedHead)
	}

	// Verify that the commit message was formatted correctly
	expectedCommitMessage := "TEST-123: Test ticket"
	if capturedCommitMessage != expectedCommitMessage {
		t.Errorf("Expected commit message to be '%s', got '%s'", expectedCommitMessage, capturedCommitMessage)
	}

	// Verify that the PR title was formatted correctly
	expectedPRTitle := "TEST-123: Test ticket"
	if capturedPRTitle != expectedPRTitle {
		t.Errorf("Expected PR title to be '%s', got '%s'", expectedPRTitle, capturedPRTitle)
	}

	// Verify that the PR body contains the structured format
	if !strings.Contains(capturedPRBody, "This PR addresses TEST-123") {
		t.Errorf("Expected PR body to contain ticket key reference, got '%s'", capturedPRBody)
	}
	if !strings.Contains(capturedPRBody, "## Problem") {
		t.Errorf("Expected PR body to contain '## Problem' section, got '%s'", capturedPRBody)
	}
	if !strings.Contains(capturedPRBody, "## Root Cause") {
		t.Errorf("Expected PR body to contain '## Root Cause' section, got '%s'", capturedPRBody)
	}
	if !strings.Contains(capturedPRBody, "## Solution") {
		t.Errorf("Expected PR body to contain '## Solution' section, got '%s'", capturedPRBody)
	}

	// Verify that the PR body does NOT contain internal Jira URLs
	if strings.Contains(capturedPRBody, "atlassian.net") {
		t.Errorf("Expected PR body NOT to contain internal Jira URL, got '%s'", capturedPRBody)
	}
}

func TestTicketProcessor_PRDescriptionWithAssignee(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Test that the PR description includes assignee information when available
	config := &models.Config{}
	config.GitHub.BotUsername = "test-bot"
	config.GitHub.BotEmail = "test@example.com"
	config.GitHub.AppID = 123456
	config.GitHub.PRLabel = "ai-pr"
	config.TempDir = "/tmp"
	config.Jira.BaseURL = "https://your-domain.atlassian.net"
	config.Jira.AssigneeToGitHubUsername = map[string]string{
		"john.doe@example.com": "john-doe-github",
	}
	config.AI.MaxRetries = 5
	config.AI.RetryDelaySeconds = 2
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"TEST"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"default": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
			},
			DisableErrorComments: true,
		},
	}

	// Create mock services with captured values
	var capturedPRBody string

	mockGitHub := &mocks.MockGitHubService{
		CommitChangesFunc: func(directory, message string, coAuthorName, coAuthorEmail string) error {
			return nil
		},
		CommitChangesViaAPIFunc: func(owner, repo, branchName, commitMessage, repoDir, coAuthorName, coAuthorEmail string) (string, error) {
			return "abc123", nil
		},
		CreatePullRequestFunc: func(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
			capturedPRBody = body
			return &models.GitHubCreatePRResponse{
				ID:      1,
				Number:  1,
				State:   "open",
				Title:   title,
				Body:    body,
				HTMLURL: "https://github.com/example/repo/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/john-doe-github/frontend.git", nil
		},
		CheckForkExistsFunc: func(forkOwner, repo string) (exists bool, cloneURL string, err error) {
			// Always return true with the appropriate fork URL
			return true, "https://github.com/" + forkOwner + "/" + repo + ".git", nil
		},
		CheckForkExistsForUserFunc: func(owner, repo, forkOwner string) (bool, error) {
			// Always return true to indicate the fork exists
			return true, nil
		},
		GetInstallationIDForRepoFunc: func(owner, repo string) (int64, error) {
			return 12345, nil
		},
		CloneRepositoryFunc: func(cloneURL, directory string) error {
			return nil
		},
		SwitchToBranchFunc: func(directory, branchName string) error {
			return nil
		},
		PushChangesFunc: func(directory, branchName, forkOwner, repo string) error {
			return nil
		},
		HasChangesFunc: func(directory string) (bool, error) {
			return true, nil // Mock AI generates changes
		},
	}
	mockJira := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Test ticket with assignee",
					Description: "Test description",
					Assignee: &models.JiraUser{
						DisplayName:  "John Doe",
						EmailAddress: "john.doe@example.com",
					},
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
					},
				},
			}, nil
		},
		GetFieldIDByNameFunc: func(fieldName string) (string, error) {
			return "customfield_10001", nil
		},
	}
	mockAI := &mocks.MockClaudeService{
		GenerateCodeFunc: func(prompt, repoDir string) (*models.ClaudeResponse, error) {
			return &models.ClaudeResponse{Result: "Code generated successfully"}, nil
		},
	}

	processor := NewTicketProcessor(mockJira, mockGitHub, mockAI, config, logger)

	// Process a ticket
	err := processor.ProcessTicket("TEST-456")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that the PR body uses the structured format
	if !strings.Contains(capturedPRBody, "## Problem") {
		t.Errorf("Expected PR body to contain '## Problem' section, got '%s'", capturedPRBody)
	}

	// Verify that the PR body does NOT leak assignee email
	if strings.Contains(capturedPRBody, "john.doe@example.com") {
		t.Errorf("Expected PR body NOT to contain assignee email, got '%s'", capturedPRBody)
	}

	// Verify that the PR body does NOT contain internal Jira URLs
	if strings.Contains(capturedPRBody, "atlassian.net") {
		t.Errorf("Expected PR body NOT to contain internal Jira URL, got '%s'", capturedPRBody)
	}
}

func TestTicketProcessor_ConfigurableStatusTransitions(t *testing.T) {
	// Create mock services with captured statuses
	var capturedStatuses []string

	mockJiraService := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Test ticket",
					Description: "Test description",
					IssueType: models.JiraIssueType{
						Name: "default",
					},
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
					},
				},
			}, nil
		},
		UpdateTicketStatusFunc: func(key string, status string) error {
			capturedStatuses = append(capturedStatuses, status)
			return nil
		},
		GetFieldIDByNameFunc: func(fieldName string) (string, error) {
			return "customfield_10001", nil
		},
		HasSecurityLevelFunc: func(key string) (bool, error) {
			return false, nil
		},
	}
	mockGitHubService := &mocks.MockGitHubService{
		CreatePullRequestFunc: func(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
			return &models.GitHubCreatePRResponse{
				ID:      1,
				Number:  1,
				State:   "open",
				Title:   title,
				Body:    body,
				HTMLURL: "https://github.com/example/repo/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/mockuser/frontend.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/mockuser/frontend.git", nil
		},
		HasChangesFunc: func(directory string) (bool, error) {
			return true, nil // Mock AI generates changes
		},
	}
	mockClaudeService := &mocks.MockClaudeService{}

	// Create config with custom status transitions
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"default": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "Development",
					InReview:   "Code Review",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
			},
		},
	}
	config.TempDir = "/tmp/test"
	config.AI.MaxRetries = 5
	config.AI.RetryDelaySeconds = 2

	// Create ticket processor
	processor := NewTicketProcessor(mockJiraService, mockGitHubService, mockClaudeService, config, zap.NewNop())

	// Test processing a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Verify that the correct status transition was used
	// Note: We only update to "In Review" status, not "In Progress"
	// This prevents tickets from getting stuck if something fails
	expectedStatuses := []string{"Code Review"}
	if len(capturedStatuses) != len(expectedStatuses) {
		t.Errorf("Expected %d status updates, got %d", len(expectedStatuses), len(capturedStatuses))
	}

	for i, expectedStatus := range expectedStatuses {
		if i >= len(capturedStatuses) {
			t.Errorf("Missing status update at index %d", i)
			continue
		}
		if capturedStatuses[i] != expectedStatus {
			t.Errorf("Expected status at index %d to be '%s', got '%s'", i, expectedStatus, capturedStatuses[i])
		}
	}
}

func TestTicketProcessor_DocumentationGenerationConfig(t *testing.T) {
	// Test case 1: Documentation generation enabled
	config1 := &models.Config{
		AIProvider: "claude",
		AI: struct {
			GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
			MaxRetries            int  `yaml:"max_retries" mapstructure:"max_retries" default:"5"`
			RetryDelaySeconds     int  `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds" default:"2"`
		}{
			GenerateDocumentation: true,
			MaxRetries:            5,
			RetryDelaySeconds:     2,
		},
	}

	processor1 := &TicketProcessorImpl{
		config: config1,
	}

	// Test case 2: Documentation generation disabled
	config2 := &models.Config{
		AIProvider: "gemini",
		AI: struct {
			GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
			MaxRetries            int  `yaml:"max_retries" mapstructure:"max_retries" default:"5"`
			RetryDelaySeconds     int  `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds" default:"2"`
		}{
			GenerateDocumentation: false,
			MaxRetries:            5,
			RetryDelaySeconds:     2,
		},
	}

	processor2 := &TicketProcessorImpl{
		config: config2,
	}

	// Verify configurations are set correctly
	if !processor1.config.AI.GenerateDocumentation {
		t.Error("Documentation generation should be enabled for processor1")
	}

	if processor2.config.AI.GenerateDocumentation {
		t.Error("Documentation generation should be disabled for processor2")
	}

	// Verify AI provider is set correctly
	if processor1.config.AIProvider != "claude" {
		t.Error("AI provider should be claude for processor1")
	}

	if processor2.config.AIProvider != "gemini" {
		t.Error("AI provider should be gemini for processor2")
	}
}

// Note: AI retry logic is tested through integration tests
// The retry logic in ticket_processor.go:311-384 handles:
// - Retrying AI code generation up to config.AI.MaxRetries times
// - Checking for changes after each attempt using githubService.HasChanges()
// - Failing gracefully with clear error messages if no changes after all retries
// - Waiting config.AI.RetryDelaySeconds between retries

func TestParsePRDescription(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		shouldMatch    bool
		mustContain    []string
		mustNotContain []string
	}{
		{
			name: "extracts full PR description section",
			input: `I made some changes to fix the bug.

## Changes Made
- Modified foo.go

## PR Description

### Problem
Users see an authentication error when pulling a valid container image.

### Root Cause
The error mapping in device/errors was returning an auth error for registry 404 responses.

### Solution
Updated the error classification to distinguish between auth failures and missing images.

## Testing
All tests pass.`,
			shouldMatch: true,
			mustContain: []string{
				"### Problem",
				"### Root Cause",
				"### Solution",
				"authentication error",
				"error classification",
			},
			mustNotContain: []string{
				"## Changes Made",
				"## Testing",
			},
		},
		{
			name:        "returns empty when no PR Description section",
			input:       "I fixed the bug by changing foo.go.\n\n## Summary\nDone.",
			shouldMatch: false,
		},
		{
			name: "stops at next H2 heading",
			input: `## PR Description

### Problem
The widget broke.

### Root Cause
Off-by-one error.

### Solution
Fixed the loop bound.

## Some Other Section
This should not be included.`,
			shouldMatch:    true,
			mustContain:    []string{"Fixed the loop bound"},
			mustNotContain: []string{"This should not be included"},
		},
		{
			name:        "returns empty for empty input",
			input:       "",
			shouldMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parsePRDescription(tc.input)

			if tc.shouldMatch && result == "" {
				t.Fatal("Expected parsePRDescription to extract content, got empty string")
			}
			if !tc.shouldMatch && result != "" {
				t.Fatalf("Expected empty result, got: %s", result)
			}

			for _, s := range tc.mustContain {
				if !strings.Contains(result, s) {
					t.Errorf("Expected result to contain %q, got:\n%s", s, result)
				}
			}
			for _, s := range tc.mustNotContain {
				if strings.Contains(result, s) {
					t.Errorf("Expected result NOT to contain %q, got:\n%s", s, result)
				}
			}
		})
	}
}

func TestScrubPII(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "scrubs redhat email",
			input:  "Assigned to john.doe@redhat.com for review",
			expect: "Assigned to [email redacted] for review",
		},
		{
			name:   "scrubs any email",
			input:  "Contact alice@example.com or bob@corp.internal.net",
			expect: "Contact [email redacted] or [email redacted]",
		},
		{
			name:   "scrubs internal Jira URL",
			input:  "See https://issues.redhat.com/browse/EDM-3115 for details",
			expect: "See [internal link removed] for details",
		},
		{
			name:   "leaves public URLs alone",
			input:  "See https://github.com/org/repo/pull/1 for the PR",
			expect: "See https://github.com/org/repo/pull/1 for the PR",
		},
		{
			name:   "handles text with no PII",
			input:  "Fixed a bug in the error handler",
			expect: "Fixed a bug in the error handler",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := scrubPII(tc.input)
			if result != tc.expect {
				t.Errorf("scrubPII(%q)\n  got:    %q\n  expect: %q", tc.input, result, tc.expect)
			}
		})
	}
}
