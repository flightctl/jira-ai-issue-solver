package services

import (
	"strings"
	"testing"
	"time"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

func TestPRFeedbackScannerService_StartStop(t *testing.T) {
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
					Status: models.JiraStatus{
						Name: "In Review",
					},
					Assignee: &models.JiraUser{
						Name: "testuser",
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
		GetTicketWithExpandedFieldsFunc: func(key string) (map[string]interface{}, map[string]string, error) {
			return map[string]interface{}{
					"customfield_10001": "https://github.com/testuser/frontend/pull/1",
				}, map[string]string{
					"customfield_10001": "Git Pull Request",
				}, nil
		},
		SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
			return &models.JiraSearchResponse{
				Total: 1,
				Issues: []models.JiraIssue{
					{
						Key: "TEST-123",
						Fields: models.JiraFields{
							Summary: "Test ticket",
							Status: models.JiraStatus{
								Name: "In Review",
							},
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
			return "https://github.com/testuser/frontend.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/testuser/frontend.git", nil
		},
		GetPRDetailsFunc: func(owner, repo string, prNumber int) (*models.GitHubPRDetails, error) {
			return &models.GitHubPRDetails{
				Number:  1,
				Title:   "Test PR",
				Body:    "Test description",
				HTMLURL: "https://github.com/testuser/frontend/pull/1",
				Head: models.GitHubRef{
					Ref: "feature-branch",
					Repo: models.GitHubRepository{
						CloneURL: "https://github.com/testuser/frontend.git",
					},
				},
				Reviews:  []models.GitHubReview{},
				Comments: []models.GitHubPRComment{},
				Files:    []models.GitHubPRFile{},
			}, nil
		},
		ListPRCommentsFunc: func(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
			return []models.GitHubPRComment{}, nil
		},
		ListPRReviewsFunc: func(owner, repo string, prNumber int) ([]models.GitHubReview, error) {
			return []models.GitHubReview{}, nil
		},
	}
	mockAIService := &mocks.MockClaudeService{}

	// Create config with short interval for testing
	config := &models.Config{}
	config.Jira.IntervalSeconds = 1 // 1 second for testing
	config.Jira.Username = "testuser"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "In Review",
		},
	}
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.TempDir = "/tmp/test"

	// Create scanner service
	scanner := NewPRFeedbackScannerService(mockJiraService, mockGitHubService, mockAIService, config, logger)

	// Start the scanner
	scanner.Start()

	// Wait a bit to ensure it starts
	time.Sleep(100 * time.Millisecond)

	// Stop the scanner
	scanner.Stop()

	// Wait a bit to ensure it stops
	time.Sleep(100 * time.Millisecond)
}

func TestPRFeedbackScannerService_ScanForPRFeedback(t *testing.T) {
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
					Status: models.JiraStatus{
						Name: "In Review",
					},
					Assignee: &models.JiraUser{
						Name: "testuser",
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
		GetTicketWithExpandedFieldsFunc: func(key string) (map[string]interface{}, map[string]string, error) {
			return map[string]interface{}{
					"customfield_10001": "https://github.com/testuser/frontend/pull/1",
				}, map[string]string{
					"customfield_10001": "Git Pull Request",
				}, nil
		},
		SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
			return &models.JiraSearchResponse{
				Total: 1,
				Issues: []models.JiraIssue{
					{
						Key: "TEST-123",
						Fields: models.JiraFields{
							Summary: "Test ticket",
							Status: models.JiraStatus{
								Name: "In Review",
							},
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
			return "https://github.com/testuser/frontend.git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (exists bool, cloneURL string, err error) {
			return true, "https://github.com/testuser/frontend.git", nil
		},
		GetPRDetailsFunc: func(owner, repo string, prNumber int) (*models.GitHubPRDetails, error) {
			return &models.GitHubPRDetails{
				Number:  1,
				Title:   "Test PR",
				Body:    "Test description",
				HTMLURL: "https://github.com/testuser/frontend/pull/1",
				Head: models.GitHubRef{
					Ref: "feature-branch",
					Repo: models.GitHubRepository{
						CloneURL: "https://github.com/testuser/frontend.git",
					},
				},
				Reviews:  []models.GitHubReview{},
				Comments: []models.GitHubPRComment{},
				Files:    []models.GitHubPRFile{},
			}, nil
		},
		ListPRCommentsFunc: func(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
			return []models.GitHubPRComment{}, nil
		},
		ListPRReviewsFunc: func(owner, repo string, prNumber int) ([]models.GitHubReview, error) {
			return []models.GitHubReview{}, nil
		},
	}
	mockAIService := &mocks.MockClaudeService{}

	// Create config
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.Username = "testuser"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "In Review",
		},
	}
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.TempDir = "/tmp/test"

	// Create scanner service with actual PR review processor
	scanner := &PRFeedbackScannerServiceImpl{
		jiraService:       mockJiraService,
		githubService:     mockGitHubService,
		aiService:         mockAIService,
		prReviewProcessor: NewPRReviewProcessor(mockJiraService, mockGitHubService, mockAIService, config, logger),
		config:            config,
		logger:            logger,
	}

	// Test scanning for PR feedback
	scanner.scanForPRFeedback()
}

func TestPRFeedbackScannerService_BuildInReviewStatusJQL(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with multiple ticket types
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "Code Review",
		},
		"Story": models.StatusTransitions{
			InReview: "Testing",
		},
		"Task": models.StatusTransitions{
			InReview: "Review",
		},
	}
	config.Jira.ProjectKeys = models.ProjectKeys{"PROJ1", "PROJ2"}

	// Create scanner service
	scanner := &PRFeedbackScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildInReviewStatusJQL()

	// Verify the JQL contains all expected conditions (order may vary due to map iteration)
	expectedConditions := []string{
		`(issuetype = "Bug" AND status = "Code Review")`,
		`(issuetype = "Story" AND status = "Testing")`,
		`(issuetype = "Task" AND status = "Review")`,
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
	if !strings.Contains(jql, `"Git Pull Request" IS NOT EMPTY`) {
		t.Errorf("Expected JQL to contain 'Git Pull Request' IS NOT EMPTY")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}

func TestPRFeedbackScannerService_BuildInReviewStatusJQL_SingleType(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with single ticket type
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "Code Review",
		},
	}
	config.Jira.ProjectKeys = models.ProjectKeys{"PROJ1"}

	// Create scanner service
	scanner := &PRFeedbackScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildInReviewStatusJQL()
	expectedJQL := `Contributors = currentUser() AND ((issuetype = "Bug" AND status = "Code Review")) AND "Git Pull Request" IS NOT EMPTY AND (project = "PROJ1") ORDER BY updated DESC`

	if jql != expectedJQL {
		t.Errorf("Expected JQL:\n%s\n\nGot JQL:\n%s", expectedJQL, jql)
	}
}

func TestPRFeedbackScannerService_BuildInReviewStatusJQL_WithProjectKeys(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with single ticket type and project keys
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "Code Review",
		},
	}
	config.Jira.ProjectKeys = models.ProjectKeys{"PROJ1", "PROJ2"}

	// Create scanner service
	scanner := &PRFeedbackScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildInReviewStatusJQL()

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
	if !strings.Contains(jql, `"Git Pull Request" IS NOT EMPTY`) {
		t.Errorf("Expected JQL to contain 'Git Pull Request' IS NOT EMPTY")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}

func TestPRFeedbackScannerService_BuildInReviewStatusJQL_WithProjectKeysAndMultipleTypes(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create config with multiple ticket types and project keys
	config := &models.Config{}
	config.Jira.IntervalSeconds = 300
	config.Jira.GitPullRequestFieldName = "Git Pull Request"
	config.Jira.StatusTransitions = models.TicketTypeStatusTransitions{
		"Bug": models.StatusTransitions{
			InReview: "Code Review",
		},
		"Story": models.StatusTransitions{
			InReview: "Testing",
		},
	}
	config.Jira.ProjectKeys = models.ProjectKeys{"PROJ1", "PROJ2", "PROJ3"}

	// Create scanner service
	scanner := &PRFeedbackScannerServiceImpl{
		config: config,
		logger: logger,
	}

	// Test JQL generation
	jql := scanner.buildInReviewStatusJQL()

	// Verify the JQL contains all expected conditions
	expectedConditions := []string{
		`(issuetype = "Bug" AND status = "Code Review")`,
		`(issuetype = "Story" AND status = "Testing")`,
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
	if !strings.Contains(jql, `"Git Pull Request" IS NOT EMPTY`) {
		t.Errorf("Expected JQL to contain 'Git Pull Request' IS NOT EMPTY")
	}
	if !strings.Contains(jql, "ORDER BY updated DESC") {
		t.Errorf("Expected JQL to contain 'ORDER BY updated DESC'")
	}
}
