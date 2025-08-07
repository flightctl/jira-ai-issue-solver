package services

import (
	"strings"
	"testing"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
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
	config.GitHub.PersonalAccessToken = "test-token"
	config.GitHub.PRLabel = "ai-pr"
	config.TempDir = "/tmp"
	config.Jira.BaseURL = "https://your-domain.atlassian.net"
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
			return "https://github.com/test-bot/repo.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/test-bot/repo.git", nil
		},
	}
	mockJira := &mocks.MockJiraService{
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
	mockAI := &mocks.MockClaudeService{}

	processor := NewTicketProcessor(mockJira, mockGitHub, mockAI, config, logger)

	// Process a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that the head parameter was formatted correctly
	expectedHead := "test-bot:TEST-123"
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

	// Verify that the PR body contains the expected format
	expectedPRBodyContains := "This PR addresses the issue described in [TEST-123]"
	if !strings.Contains(capturedPRBody, expectedPRBodyContains) {
		t.Errorf("Expected PR body to contain '%s', got '%s'", expectedPRBodyContains, capturedPRBody)
	}

	// Verify that the PR body contains the Jira URL
	expectedJiraURL := "https://your-domain.atlassian.net/browse/TEST-123"
	if !strings.Contains(capturedPRBody, expectedJiraURL) {
		t.Errorf("Expected PR body to contain Jira URL '%s', got '%s'", expectedJiraURL, capturedPRBody)
	}
}

func TestTicketProcessor_PRDescriptionWithAssignee(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Test that the PR description includes assignee information when available
	config := &models.Config{}
	config.GitHub.BotUsername = "test-bot"
	config.GitHub.BotEmail = "test@example.com"
	config.GitHub.PersonalAccessToken = "test-token"
	config.GitHub.PRLabel = "ai-pr"
	config.TempDir = "/tmp"
	config.Jira.BaseURL = "https://your-domain.atlassian.net"
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
			return "https://github.com/test-bot/repo.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/test-bot/repo.git", nil
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
	mockAI := &mocks.MockClaudeService{}

	processor := NewTicketProcessor(mockJira, mockGitHub, mockAI, config, logger)

	// Process a ticket
	err := processor.ProcessTicket("TEST-456")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that the PR body contains assignee information
	expectedAssigneeInfo := "**Assignee:** John Doe (john.doe@example.com)"
	if !strings.Contains(capturedPRBody, expectedAssigneeInfo) {
		t.Errorf("Expected PR body to contain assignee info '%s', got '%s'", expectedAssigneeInfo, capturedPRBody)
	}

	// Verify that the PR body contains the Jira URL
	expectedJiraURL := "https://your-domain.atlassian.net/browse/TEST-456"
	if !strings.Contains(capturedPRBody, expectedJiraURL) {
		t.Errorf("Expected PR body to contain Jira URL '%s', got '%s'", expectedJiraURL, capturedPRBody)
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

	// Create ticket processor
	processor := NewTicketProcessor(mockJiraService, mockGitHubService, mockClaudeService, config, zap.NewNop())

	// Test processing a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Verify that the correct status transitions were used
	expectedStatuses := []string{"Development", "Code Review"}
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
	logger := zap.NewNop()

	// Test case 1: Documentation generation enabled
	config1 := &models.Config{
		AIProvider: "claude",
		AI: struct {
			GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
		}{
			GenerateDocumentation: true,
		},
	}

	processor1 := &TicketProcessorImpl{
		config: config1,
		logger: logger,
	}

	// Test case 2: Documentation generation disabled
	config2 := &models.Config{
		AIProvider: "gemini",
		AI: struct {
			GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
		}{
			GenerateDocumentation: false,
		},
	}

	processor2 := &TicketProcessorImpl{
		config: config2,
		logger: logger,
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
