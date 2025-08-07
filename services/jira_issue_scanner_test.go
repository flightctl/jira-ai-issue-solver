package services

import (
	"strings"
	"testing"
	"time"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

func TestJiraIssueScannerService_StartStop(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create mock services with stubbed methods
	mockJiraService := &mocks.MockJiraService{
		SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
			return &models.JiraSearchResponse{
				Total:  0,
				Issues: []models.JiraIssue{},
			}, nil
		},
	}
	mockGitHubService := &mocks.MockGitHubService{}
	mockClaudeService := &mocks.MockClaudeService{}

	// Create config with short interval for testing
	config := &models.Config{}
	config.Jira.IntervalSeconds = 1 // 1 second for testing
	config.TempDir = "/tmp/test"

	// Create scanner service
	scanner := NewJiraIssueScannerService(mockJiraService, mockGitHubService, mockClaudeService, config, logger)

	// Start the scanner
	scanner.Start()

	// Wait a bit to ensure it starts
	time.Sleep(100 * time.Millisecond)

	// Stop the scanner
	scanner.Stop()

	// Wait a bit to ensure it stops
	time.Sleep(100 * time.Millisecond)
}

func TestJiraIssueScannerService_ScanForTickets(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create mock services with stubbed methods
	mockJiraService := &mocks.MockJiraService{
		SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
			return &models.JiraSearchResponse{
				Total:  1,
				Issues: []models.JiraIssue{{Key: "TEST-1"}},
			}, nil
		},
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Test ticket",
					Description: "Test description",
					Components:  []models.JiraComponent{{ID: "1", Name: "frontend"}},
				},
			}, nil
		},
		UpdateTicketLabelsFunc: func(key string, addLabels, removeLabels []string) error {
			return nil
		},
		UpdateTicketStatusFunc: func(key string, status string) error {
			return nil
		},
		AddCommentFunc: func(key string, comment string) error {
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
	mockClaudeService := &mocks.MockClaudeService{
		GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
			return nil, nil
		},
	}

	// Create config
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"TEST"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo: "Open",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
		},
	}
	config.TempDir = "/tmp/test"

	// Create a mock ticket processor with a no-op ProcessTicket
	mockTicketProcessor := &mocks.MockTicketProcessor{
		ProcessTicketFunc: func(key string) error {
			return nil
		},
	}

	// Create scanner service with injected mock ticket processor
	scanner := &JiraIssueScannerServiceImpl{
		jiraService:     mockJiraService,
		githubService:   mockGitHubService,
		aiService:       mockClaudeService,
		ticketProcessor: mockTicketProcessor,
		config:          config,
		logger:          logger,
	}

	// Test scanning for tickets
	scanner.scanForTickets()
}

func TestJiraIssueScannerService_BuildTodoStatusJQL(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with multiple ticket types
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1", "PROJ2"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo: "Open",
				},
				"Story": models.StatusTransitions{
					Todo: "Backlog",
				},
				"Task": models.StatusTransitions{
					Todo: "To Do",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
		},
	}

	// Create scanner service
	scanner := &JiraIssueScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildTodoStatusJQL()

	// Verify the JQL contains all expected conditions (order may vary due to map iteration)
	expectedConditions := []string{
		`(issuetype = "Bug" AND status = "Open")`,
		`(issuetype = "Story" AND status = "Backlog")`,
		`(issuetype = "Task" AND status = "To Do")`,
	}

	// Check that all expected conditions are present
	for _, condition := range expectedConditions {
		if !strings.Contains(jql, condition) {
			t.Errorf("Expected JQL to contain condition: %s", condition)
		}
	}

	// Check basic structure
	if !strings.Contains(jql, "Contributors = currentUser()") {
		t.Errorf("Expected JQL to contain 'Contributors = currentUser()'")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}

func TestJiraIssueScannerService_BuildTodoStatusJQL_SingleType(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with single ticket type
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo: "Open",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
		},
	}

	// Create scanner service
	scanner := &JiraIssueScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildTodoStatusJQL()
	expectedJQL := `Contributors = currentUser() AND ((issuetype = "Bug" AND status = "Open")) AND (project = "PROJ1") ORDER BY updated DESC`

	if jql != expectedJQL {
		t.Errorf("Expected JQL:\n%s\n\nGot JQL:\n%s", expectedJQL, jql)
	}
}

func TestJiraIssueScannerService_BuildTodoStatusJQL_WithProjectKeys(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with single ticket type and project keys
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1", "PROJ2"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo: "Open",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
		},
	}

	// Create scanner service
	scanner := &JiraIssueScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildTodoStatusJQL()

	// Verify the JQL contains project filtering
	expectedProjectConditions := []string{
		`project = "PROJ1"`,
		`project = "PROJ2"`,
	}

	for _, condition := range expectedProjectConditions {
		if !strings.Contains(jql, condition) {
			t.Errorf("Expected JQL to contain project condition: %s", condition)
		}
	}

	// Check that the project conditions are properly combined with OR
	if !strings.Contains(jql, `(project = "PROJ1" OR project = "PROJ2")`) {
		t.Errorf("Expected JQL to contain properly formatted project OR conditions")
	}

	// Check basic structure
	if !strings.Contains(jql, "Contributors = currentUser()") {
		t.Errorf("Expected JQL to contain 'Contributors = currentUser()'")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}

func TestJiraIssueScannerService_BuildTodoStatusJQL_WithProjectKeysAndMultipleTypes(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with multiple ticket types and project keys
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"PROJ1", "PROJ2", "PROJ3"},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo: "Open",
				},
				"Story": models.StatusTransitions{
					Todo: "Backlog",
				},
			},
			ComponentToRepo: models.ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
		},
	}

	// Create scanner service
	scanner := &JiraIssueScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildTodoStatusJQL()

	// Verify the JQL contains all expected conditions
	expectedConditions := []string{
		`(issuetype = "Bug" AND status = "Open")`,
		`(issuetype = "Story" AND status = "Backlog")`,
		`project = "PROJ1"`,
		`project = "PROJ2"`,
		`project = "PROJ3"`,
	}

	for _, condition := range expectedConditions {
		if !strings.Contains(jql, condition) {
			t.Errorf("Expected JQL to contain condition: %s", condition)
		}
	}

	// Check that the project conditions are properly combined with OR
	if !strings.Contains(jql, `(project = "PROJ1" OR project = "PROJ2" OR project = "PROJ3")`) {
		t.Errorf("Expected JQL to contain properly formatted project OR conditions")
	}

	// Check basic structure
	if !strings.Contains(jql, "Contributors = currentUser()") {
		t.Errorf("Expected JQL to contain 'Contributors = currentUser()'")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}
