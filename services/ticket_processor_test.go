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
					IssueType: models.JiraIssueType{
						Name: "default",
					},
				},
			}, nil
		},
		GetFieldIDByNameFunc: func(fieldName string) (string, error) {
			return "customfield_10001", nil
		},
		HasSecurityLevelFunc: func(key string) (bool, error) {
			return false, nil
		},
	}
	mockAI := &mocks.MockClaudeService{}

	processor := NewTicketProcessor(mockJira, mockGitHub, mockAI, config, logger)

	// Process a ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify that the head parameter was formatted correctly (now always includes owner/repo format)
	expectedHead := "test-bot:TEST-123-example-frontend"
	if capturedHead != expectedHead {
		t.Errorf("Expected head to be '%s', got '%s'", expectedHead, capturedHead)
	}

	// Verify that the commit message was formatted correctly (clean, no repo info)
	expectedCommitMessage := "TEST-123: Test ticket"
	if capturedCommitMessage != expectedCommitMessage {
		t.Errorf("Expected commit message to be '%s', got '%s'", expectedCommitMessage, capturedCommitMessage)
	}

	// Verify that the PR title was formatted correctly (clean, no repo info)
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
					IssueType: models.JiraIssueType{
						Name: "default",
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

func TestTicketProcessor_ProcessTicket_MultipleRepositories(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Track created PRs
	var createdPRs []string

	// Create mock services for multi-repo scenario
	mockJiraService := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Multi-repo test ticket",
					Description: "Test description spanning multiple repositories",
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
						{
							ID:   "2",
							Name: "backend",
						},
						{
							ID:   "3",
							Name: "api",
						},
					},
					IssueType: models.JiraIssueType{
						Name: "Bug",
					},
				},
			}, nil
		},
		UpdateTicketStatusFunc: func(key, status string) error {
			return nil
		},
		AddCommentFunc: func(key, comment string) error {
			// Track created PR comments
			if strings.Contains(comment, "[AI-BOT-PR") {
				createdPRs = append(createdPRs, comment)
			}
			return nil
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
				HTMLURL: "https://github.com/" + owner + "/" + repo + "/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/mockuser/" + repo + ".git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (bool, string, error) {
			return true, "https://github.com/mockuser/" + repo + ".git", nil
		},
		CloneRepositoryFunc:      func(repoURL, targetDir string) error { return nil },
		SwitchToTargetBranchFunc: func(repoDir string) error { return nil },
		CreateBranchFunc:         func(repoDir, branchName string) error { return nil },
		CommitChangesFunc:        func(repoDir, message, coAuthorName, coAuthorEmail string) error { return nil },
		PushChangesFunc:          func(repoDir, branchName string) error { return nil },
	}

	mockClaudeService := &mocks.MockClaudeService{
		GenerateCodeFunc: func(prompt, repoDir string) (*models.ClaudeResponse, error) {
			return &models.ClaudeResponse{
				Type:    "completion",
				IsError: false,
				Result:  "Mock generated code for " + repoDir,
			}, nil
		},
	}

	// Create configuration with multiple component mappings
	config := &models.Config{}
	config.TempDir = "/tmp/test"
	config.Jira.BaseURL = "https://test.atlassian.net"
	config.Jira.Username = "testuser"
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"TEST"},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
				"backend":  "https://github.com/example/backend.git",
				"api":      "https://github.com/example/api.git",
			},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
			},
		},
	}
	config.GitHub.BotUsername = "testbot"
	config.GitHub.TargetBranch = "main"
	config.AI.GenerateDocumentation = false

	// Create processor
	processor := NewTicketProcessor(mockJiraService, mockGitHubService, mockClaudeService, config, logger)

	// Process ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify that 3 PR comments were created (one for each repository)
	if len(createdPRs) != 3 {
		t.Errorf("Expected 3 PR comments, got %d: %v", len(createdPRs), createdPRs)
	}

	// Verify that each PR comment has the expected numbered format with owner/repo
	expectedPrefixes := []string{
		"[AI-BOT-PR-1-example/frontend]",
		"[AI-BOT-PR-2-example/backend]",
		"[AI-BOT-PR-3-example/api]",
	}
	for i, comment := range createdPRs {
		if i < len(expectedPrefixes) {
			if !strings.Contains(comment, expectedPrefixes[i]) {
				t.Errorf("Expected PR comment to contain '%s', got '%s'", expectedPrefixes[i], comment)
			}
		}
	}
}

func TestTicketProcessor_ProcessTicket_SingleRepository_UsesSimplifiedFormat(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Track created PRs
	var createdPRs []string

	// Create mock services for single-repo scenario
	mockJiraService := &mocks.MockJiraService{
		GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
			return &models.JiraTicketResponse{
				Key: key,
				Fields: models.JiraFields{
					Summary:     "Single-repo test ticket",
					Description: "Test description for single repository",
					Components: []models.JiraComponent{
						{
							ID:   "1",
							Name: "frontend",
						},
					},
					IssueType: models.JiraIssueType{
						Name: "Bug",
					},
				},
			}, nil
		},
		UpdateTicketStatusFunc: func(key, status string) error {
			return nil
		},
		AddCommentFunc: func(key, comment string) error {
			// Track created PR comments
			if strings.Contains(comment, "[AI-BOT-PR") {
				createdPRs = append(createdPRs, comment)
			}
			return nil
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
				HTMLURL: "https://github.com/" + owner + "/" + repo + "/pull/1",
			}, nil
		},
		ForkRepositoryFunc: func(owner, repo string) (string, error) {
			return "https://github.com/mockuser/" + repo + ".git", nil
		},
		CheckForkExistsFunc: func(owner, repo string) (bool, string, error) {
			return true, "https://github.com/mockuser/" + repo + ".git", nil
		},
		CloneRepositoryFunc:      func(repoURL, targetDir string) error { return nil },
		SwitchToTargetBranchFunc: func(repoDir string) error { return nil },
		CreateBranchFunc:         func(repoDir, branchName string) error { return nil },
		CommitChangesFunc:        func(repoDir, message, coAuthorName, coAuthorEmail string) error { return nil },
		PushChangesFunc:          func(repoDir, branchName string) error { return nil },
	}

	mockClaudeService := &mocks.MockClaudeService{
		GenerateCodeFunc: func(prompt, repoDir string) (*models.ClaudeResponse, error) {
			return &models.ClaudeResponse{
				Type:    "completion",
				IsError: false,
				Result:  "Mock generated code for " + repoDir,
			}, nil
		},
	}

	// Create configuration with single component mapping
	config := &models.Config{}
	config.TempDir = "/tmp/test"
	config.Jira.BaseURL = "https://test.atlassian.net"
	config.Jira.Username = "testuser"
	config.Jira.Projects = []models.ProjectConfig{
		{
			ProjectKeys: models.ProjectKeys{"TEST"},
			ComponentToRepo: models.ComponentToRepoMap{
				"frontend": "https://github.com/example/frontend.git",
			},
			StatusTransitions: models.TicketTypeStatusTransitions{
				"Bug": models.StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
			},
		},
	}
	config.GitHub.BotUsername = "testbot"
	config.GitHub.TargetBranch = "main"
	config.AI.GenerateDocumentation = false

	// Create processor
	processor := NewTicketProcessor(mockJiraService, mockGitHubService, mockClaudeService, config, logger)

	// Process ticket
	err := processor.ProcessTicket("TEST-123")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify that 1 PR comment was created with numbered format
	if len(createdPRs) != 1 {
		t.Errorf("Expected 1 PR comment, got %d: %v", len(createdPRs), createdPRs)
	}

	// Verify that even single repository uses numbered format with owner/repo
	if len(createdPRs) > 0 {
		expectedPrefix := "[AI-BOT-PR-1-example/frontend]"
		if !strings.Contains(createdPRs[0], expectedPrefix) {
			t.Errorf("Expected PR comment to contain '%s', got '%s'", expectedPrefix, createdPRs[0])
		}
	}
}

func TestTicketProcessor_GetRepositoryURLs_MultipleComponents(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create configuration
	config := &models.Config{
		TempDir: "/tmp/test",
	}

	// Create processor
	processor := &TicketProcessorImpl{
		config: config,
		logger: logger,
	}

	// Test case: Multiple components mapping to different repositories
	ticket := &models.JiraTicketResponse{
		Key: "TEST-123",
		Fields: models.JiraFields{
			Components: []models.JiraComponent{
				{ID: "1", Name: "frontend"},
				{ID: "2", Name: "backend"},
				{ID: "3", Name: "api"},
			},
		},
	}

	projectConfig := &models.ProjectConfig{
		ComponentToRepo: models.ComponentToRepoMap{
			"frontend": "https://github.com/example/frontend.git",
			"backend":  "https://github.com/example/backend.git",
			"api":      "https://github.com/example/api.git",
		},
	}

	repos, err := processor.getRepositoryURLs(ticket, projectConfig)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedRepos := []string{
		"https://github.com/example/frontend.git",
		"https://github.com/example/backend.git",
		"https://github.com/example/api.git",
	}

	if len(repos) != len(expectedRepos) {
		t.Errorf("Expected %d repositories, got %d", len(expectedRepos), len(repos))
	}

	// Check that all expected repos are present (order may vary due to map iteration)
	repoSet := make(map[string]bool)
	for _, repo := range repos {
		repoSet[repo] = true
	}

	for _, expectedRepo := range expectedRepos {
		if !repoSet[expectedRepo] {
			t.Errorf("Expected repository %s not found in results", expectedRepo)
		}
	}
}

func TestTicketProcessor_GetRepositoryURLs_DeduplicateRepos(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create configuration
	config := &models.Config{
		TempDir: "/tmp/test",
	}

	// Create processor
	processor := &TicketProcessorImpl{
		config: config,
		logger: logger,
	}

	// Test case: Multiple components mapping to the same repository (should be deduplicated)
	ticket := &models.JiraTicketResponse{
		Key: "TEST-123",
		Fields: models.JiraFields{
			Components: []models.JiraComponent{
				{ID: "1", Name: "frontend"},
				{ID: "2", Name: "ui-components"},
				{ID: "3", Name: "backend"},
			},
		},
	}

	projectConfig := &models.ProjectConfig{
		ComponentToRepo: models.ComponentToRepoMap{
			"frontend":      "https://github.com/example/frontend.git",
			"ui-components": "https://github.com/example/frontend.git", // Same repo as frontend
			"backend":       "https://github.com/example/backend.git",
		},
	}

	repos, err := processor.getRepositoryURLs(ticket, projectConfig)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should only have 2 unique repositories
	if len(repos) != 2 {
		t.Errorf("Expected 2 unique repositories after deduplication, got %d: %v", len(repos), repos)
	}

	// Check that both unique repos are present
	repoSet := make(map[string]bool)
	for _, repo := range repos {
		repoSet[repo] = true
	}

	expectedRepos := []string{
		"https://github.com/example/frontend.git",
		"https://github.com/example/backend.git",
	}

	for _, expectedRepo := range expectedRepos {
		if !repoSet[expectedRepo] {
			t.Errorf("Expected repository %s not found in results", expectedRepo)
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
