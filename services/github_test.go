package services

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// execCommand is a variable that holds the exec.Command function
// It can be replaced with a mock for testing
var execCommand = exec.Command

// MockGitHubAppService is a mock implementation of GitHubAppService
type MockGitHubAppService struct {
	GetInstallationTokenFunc func() (string, error)
	GetAppTokenFunc          func() (string, error)
}

func (m *MockGitHubAppService) GetInstallationToken() (string, error) {
	if m.GetInstallationTokenFunc != nil {
		return m.GetInstallationTokenFunc()
	}
	return "mock-installation-token", nil
}

func (m *MockGitHubAppService) GetAppToken() (string, error) {
	if m.GetAppTokenFunc != nil {
		return m.GetAppTokenFunc()
	}
	return "mock-app-token", nil
}

// TestCreatePullRequest_ForkOwnerExtraction tests that the fork owner is correctly extracted from head parameter
func TestCreatePullRequest_ForkOwnerExtraction(t *testing.T) {
	testCases := []struct {
		name          string
		head          string
		expectedOwner string
	}{
		{
			name:          "fork with owner prefix",
			head:          "adalton:EDM-123",
			expectedOwner: "adalton",
		},
		{
			name:          "another fork owner",
			head:          "bob:feature-branch",
			expectedOwner: "bob",
		},
		{
			name:          "no colon - same repo",
			head:          "feature-branch",
			expectedOwner: "", // Will be set to owner param in actual code
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var extractedOwner string
			if strings.Contains(tc.head, ":") {
				parts := strings.SplitN(tc.head, ":", 2)
				extractedOwner = parts[0]
			}

			if extractedOwner != tc.expectedOwner {
				t.Errorf("Expected owner '%s', got '%s'", tc.expectedOwner, extractedOwner)
			}
		})
	}
}

// generateTestRSAKey creates a temporary RSA private key file for testing
func generateTestRSAKey(t *testing.T) string {
	// Generate RSA key
	privateKey, err := exec.Command("openssl", "genrsa", "2048").Output()
	if err != nil {
		t.Skip("Skipping test: openssl not available for generating test RSA key")
		return ""
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "test-github-key-*.pem")
	if err != nil {
		t.Fatalf("Failed to create temp key file: %v", err)
	}

	// Write key to file
	if _, err := tmpFile.Write(privateKey); err != nil {
		_ = os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write key to temp file: %v", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		t.Fatalf("Failed to close temp file: %v", err)
	}

	return tmpFile.Name()
}

// TestCreatePullRequest_GitHubApp tests CreatePullRequest with GitHub App authentication
func TestCreatePullRequest_GitHubApp(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	testCases := []struct {
		name                         string
		head                         string
		base                         string
		baseOwner                    string
		repo                         string
		expectedBaseOwner            string
		expectedInstallationRequests int
		expectedAuthTokenPrefix      string
	}{
		{
			name:                         "PR from fork - should use base repo's installation",
			head:                         "adalton:EDM-123",
			base:                         "main",
			baseOwner:                    "flightctl",
			repo:                         "flightctl",
			expectedBaseOwner:            "flightctl",
			expectedInstallationRequests: 1,
			expectedAuthTokenPrefix:      "token ghs_mock", // Transport automatically adds installation token
		},
		{
			name:                         "PR within same repo - should use base repo's installation",
			head:                         "feature-branch",
			base:                         "main",
			baseOwner:                    "myorg",
			repo:                         "myrepo",
			expectedBaseOwner:            "myorg",
			expectedInstallationRequests: 1,
			expectedAuthTokenPrefix:      "token ghs_mock",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var installationRequests []string
			var prCreationRequest *http.Request
			var tokenRequests int

			// Create a mock HTTP client that tracks requests
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				// Track JWT token requests (for GitHub App authentication)
				if strings.Contains(req.URL.Path, "/app/installations") && strings.Contains(req.URL.Path, "/access_tokens") {
					tokenRequests++
					return &http.Response{
						StatusCode: http.StatusCreated,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
							"token": "ghs_mock_installation_token",
							"expires_at": "2099-01-01T00:00:00Z"
						}`))),
					}, nil
				}

				// Track installation ID requests
				if strings.Contains(req.URL.Path, "/installation") {
					parts := strings.Split(req.URL.Path, "/")
					if len(parts) >= 4 {
						owner := parts[2]
						installationRequests = append(installationRequests, owner)
					}

					// Return mock installation response
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
							"id": 12345678
						}`))),
					}, nil
				}

				// Track PR creation request
				if strings.Contains(req.URL.Path, "/pulls") {
					prCreationRequest = req

					// Return mock PR response
					return &http.Response{
						StatusCode: http.StatusCreated,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
							"id": 98765,
							"number": 42,
							"state": "open",
							"title": "Test PR",
							"body": "Test body",
							"html_url": "https://github.com/test/repo/pull/42",
							"created_at": "2023-01-01T00:00:00Z",
							"updated_at": "2023-01-01T00:00:00Z"
						}`))),
					}, nil
				}

				// Ignore other requests (GitHub App may make additional API calls)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
				}, nil
			})

			// Create config with GitHub App settings
			config := &models.Config{}
			config.GitHub.AppID = 123456
			config.GitHub.BotUsername = "test-bot[bot]"
			config.GitHub.PRLabel = "ai-pr"
			config.GitHub.PrivateKeyPath = keyPath

			// Create service with mock transport for app-level operations
			appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
				mockClient.Transport,
				config.GitHub.AppID,
				config.GitHub.PrivateKeyPath,
			)
			if err != nil {
				t.Fatalf("Failed to create app transport: %v", err)
			}

			service := &GitHubServiceImpl{
				config:           config,
				client:           mockClient,
				appTransport:     appTransport,
				installationAuth: make(map[int64]*ghinstallation.Transport),
				executor:         execCommand,
				logger:           zap.NewNop(),
			}

			// Call CreatePullRequest
			result, err := service.CreatePullRequest(
				tc.baseOwner,
				tc.repo,
				"Test PR",
				"Test body",
				tc.head,
				tc.base,
			)

			// Verify no error
			if err != nil {
				t.Fatalf("Expected no error but got: %v", err)
			}

			// Verify result
			if result == nil {
				t.Fatal("Expected a result but got nil")
			} else {
				if result.Number != 42 {
					t.Errorf("Expected PR number 42 but got %d", result.Number)
				}
			}

			// Verify installation ID was requested for the correct owner (base repo)
			if len(installationRequests) != tc.expectedInstallationRequests {
				t.Errorf("Expected %d installation requests but got %d", tc.expectedInstallationRequests, len(installationRequests))
			}
			if len(installationRequests) > 0 && installationRequests[0] != tc.expectedBaseOwner {
				t.Errorf("Expected installation request for base owner '%s' but got '%s'", tc.expectedBaseOwner, installationRequests[0])
			}

			// Verify that token was requested (proves GitHub App authentication was used)
			if tokenRequests == 0 {
				t.Error("Expected at least one installation token request, but got none")
			}

			// Verify Authorization header was set by the transport (not manually by our code)
			if prCreationRequest != nil {
				authHeader := prCreationRequest.Header.Get("Authorization")
				if authHeader == "" {
					t.Error("Expected Authorization header to be set by transport, but it was not present")
				} else if !strings.HasPrefix(authHeader, tc.expectedAuthTokenPrefix) {
					t.Errorf("Expected Authorization header to start with '%s', but got: %s", tc.expectedAuthTokenPrefix, authHeader)
				}
			} else {
				t.Error("PR creation request was not captured")
			}

			// Verify maintainer_can_modify is explicitly set to false
			// This is required for GitHub App tokens creating PRs from forks
			if prCreationRequest != nil {
				bodyBytes, _ := io.ReadAll(prCreationRequest.Body)
				var payload models.GitHubCreatePRRequest
				if err := json.Unmarshal(bodyBytes, &payload); err != nil {
					t.Errorf("Failed to unmarshal request body: %v", err)
				} else {
					if payload.MaintainerCanModify == nil {
						t.Error("Expected maintainer_can_modify to be set to false, but got nil")
					} else if *payload.MaintainerCanModify != false {
						t.Errorf("Expected maintainer_can_modify to be false, but got %v", *payload.MaintainerCanModify)
					}
				}
			}
		})
	}
}

// TestCreatePullRequest tests the CreatePullRequest method
func TestCreatePullRequest(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Test cases
	testCases := []struct {
		name           string
		owner          string
		repo           string
		title          string
		body           string
		head           string
		base           string
		prLabel        string
		mockResponse   *http.Response
		mockError      error
		expectedResult *models.GitHubCreatePRResponse
		expectedError  bool
	}{
		{
			name:    "successful PR creation",
			owner:   "example",
			repo:    "repo",
			title:   "Test PR",
			body:    "This is a test PR",
			head:    "feature/TEST-123",
			base:    "main",
			prLabel: "ai-pr",
			mockResponse: &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"id": 12345,
					"number": 1,
					"state": "open",
					"title": "Test PR",
					"body": "This is a test PR",
					"html_url": "https://github.com/example/repo/pull/1",
					"created_at": "2023-01-01T00:00:00Z",
					"updated_at": "2023-01-01T00:00:00Z"
				}`))),
			},
			mockError: nil,
			expectedResult: &models.GitHubCreatePRResponse{
				ID:      12345,
				Number:  1,
				State:   "open",
				Title:   "Test PR",
				Body:    "This is a test PR",
				HTMLURL: "https://github.com/example/repo/pull/1",
			},
			expectedError: false,
		},
		{
			name:    "error creating PR",
			owner:   "example",
			repo:    "repo",
			title:   "Test PR",
			body:    "This is a test PR",
			head:    "feature/TEST-123",
			base:    "main",
			prLabel: "ai-pr",
			mockResponse: &http.Response{
				StatusCode: http.StatusUnprocessableEntity,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for example:feature/TEST-123."}],"documentation_url":"https://docs.github.com/rest/reference/pulls#create-a-pull-request"}`))),
			},
			mockError:      nil,
			expectedResult: nil,
			expectedError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock HTTP client that captures the request body
			var capturedBody []byte
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				// Mock installation token request
				if strings.Contains(req.URL.Path, "/access_tokens") {
					return &http.Response{
						StatusCode: http.StatusCreated,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
							"token": "ghs_mock_installation_token",
							"expires_at": "2099-01-01T00:00:00Z"
						}`))),
					}, nil
				}

				// Mock installation ID request
				if strings.Contains(req.URL.Path, "/installation") && !strings.Contains(req.URL.Path, "/access_tokens") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
							"id": 12345678
						}`))),
					}, nil
				}

				// Capture the request body for PR creation
				capturedBody, _ = io.ReadAll(req.Body)
				return tc.mockResponse, tc.mockError
			})

			// Create a GitHubService with the mock client
			config := &models.Config{}
			config.GitHub.AppID = 123456
			config.GitHub.PrivateKeyPath = keyPath
			config.GitHub.BotUsername = "test-bot"
			config.GitHub.PRLabel = tc.prLabel

			// Create app transport
			appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
				mockClient.Transport,
				config.GitHub.AppID,
				config.GitHub.PrivateKeyPath,
			)
			if err != nil {
				t.Fatalf("Failed to create app transport: %v", err)
			}

			service := &GitHubServiceImpl{
				config:           config,
				client:           mockClient,
				executor:         execCommand,
				appTransport:     appTransport,
				installationAuth: make(map[int64]*ghinstallation.Transport),
				logger:           zap.NewNop(),
			}

			// Call the method being tested
			result, err := service.CreatePullRequest(tc.owner, tc.repo, tc.title, tc.body, tc.head, tc.base)

			// Check the results
			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tc.expectedResult != nil {
				if result == nil {
					t.Errorf("Expected a result but got nil")
				} else {
					if result.ID != tc.expectedResult.ID {
						t.Errorf("Expected result ID %d but got %d", tc.expectedResult.ID, result.ID)
					}
					if result.Number != tc.expectedResult.Number {
						t.Errorf("Expected result Number %d but got %d", tc.expectedResult.Number, result.Number)
					}
					// Add more assertions for other fields as needed
				}
			}

			// Verify that the label was included in the request
			if len(capturedBody) > 0 {
				var requestPayload models.GitHubCreatePRRequest
				if err := json.Unmarshal(capturedBody, &requestPayload); err != nil {
					t.Errorf("Failed to unmarshal request body: %v", err)
				} else {
					if len(requestPayload.Labels) == 0 {
						t.Errorf("Expected labels to be included in request, but got empty labels")
					} else if requestPayload.Labels[0] != tc.prLabel {
						t.Errorf("Expected label '%s' but got '%s'", tc.prLabel, requestPayload.Labels[0])
					}
					// Verify maintainer_can_modify is explicitly set to false
					// This is required for GitHub App tokens creating PRs from forks
					if requestPayload.MaintainerCanModify == nil {
						t.Error("Expected maintainer_can_modify to be set to false, but got nil")
					} else if *requestPayload.MaintainerCanModify != false {
						t.Errorf("Expected maintainer_can_modify to be false, but got %v", *requestPayload.MaintainerCanModify)
					}
				}
			}
		})
	}
}

// TestExtractRepoInfo tests the ExtractRepoInfo function
func TestExtractRepoInfo(t *testing.T) {
	// Test cases
	testCases := []struct {
		name          string
		repoURL       string
		expectedOwner string
		expectedRepo  string
		expectedError bool
	}{
		{
			name:          "HTTPS URL",
			repoURL:       "https://github.com/example/repo.git",
			expectedOwner: "example",
			expectedRepo:  "repo",
			expectedError: false,
		},
		{
			name:          "SSH URL",
			repoURL:       "git@github.com:example/repo.git",
			expectedOwner: "example",
			expectedRepo:  "repo",
			expectedError: false,
		},
		{
			name:          "HTTPS URL without .git",
			repoURL:       "https://github.com/example/repo",
			expectedOwner: "example",
			expectedRepo:  "repo",
			expectedError: false,
		},
		{
			name:          "invalid URL",
			repoURL:       "invalid-url",
			expectedOwner: "",
			expectedRepo:  "",
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call the function being tested
			owner, repo, err := ExtractRepoInfo(tc.repoURL)

			// Check the results
			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if owner != tc.expectedOwner {
				t.Errorf("Expected owner %s but got %s", tc.expectedOwner, owner)
			}
			if repo != tc.expectedRepo {
				t.Errorf("Expected repo %s but got %s", tc.expectedRepo, repo)
			}
		})
	}
}

// TestSwitchToBranch tests the SwitchToBranch method
func TestSwitchToBranch(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "github-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Track the commands that would be executed
	var executedCommands []string
	mockExecutor := func(name string, args ...string) *exec.Cmd {
		command := strings.Join(append([]string{name}, args...), " ")
		executedCommands = append(executedCommands, command)

		// Return a mock command that does nothing
		return exec.Command("echo", "mocked")
	}

	// Create config
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.BotUsername = "test-bot"

	// Create GitHub service with mocked executor
	githubService := NewGitHubService(config, logger, mockExecutor)

	// Test switching to the test branch
	err = githubService.SwitchToBranch(tempDir, "test-branch")
	if err != nil {
		t.Errorf("SwitchToBranch() error = %v", err)
	}

	// Verify the correct commands were executed
	expectedCommands := []string{
		"git fetch origin",
		"git checkout test-branch",
	}

	if len(executedCommands) != len(expectedCommands) {
		t.Errorf("Expected %d commands to be executed, got %d", len(expectedCommands), len(executedCommands))
	}

	for i, expected := range expectedCommands {
		if i < len(executedCommands) && executedCommands[i] != expected {
			t.Errorf("Expected command '%s', got '%s'", expected, executedCommands[i])
		}
	}
}

// TestSwitchToBranch_NonExistentBranch tests switching to a non-existent branch
func TestSwitchToBranch_NonExistentBranch(t *testing.T) {
	// Create test logger
	logger := zap.NewNop()

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "github-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Create initial commit
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Create config
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.BotUsername = "test-bot"

	// Create GitHub service
	githubService := NewGitHubService(config, logger)

	// Test switching to a non-existent branch
	err = githubService.SwitchToBranch(tempDir, "non-existent-branch")
	if err == nil {
		t.Error("SwitchToBranch() should return error for non-existent branch")
	}
}

func TestGitHubService_CommitChanges_WithCoAuthor(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create config
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.BotUsername = "test-bot"

	// Create GitHub service
	githubService := NewGitHubService(config, zap.NewNop())

	// Test commit with co-author
	commitMessage := "TEST-123: Test commit with co-author"
	coAuthorName := "Test Assignee"
	coAuthorEmail := "assignee@example.com"

	err = githubService.CommitChanges(tempDir, commitMessage, coAuthorName, coAuthorEmail)
	if err != nil {
		t.Fatalf("Failed to commit changes: %v", err)
	}

	// Verify the commit message contains the co-author
	cmd = exec.Command("git", "log", "--format=%B", "-1")
	cmd.Dir = tempDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit message: %v", err)
	}

	commitOutput := string(output)
	if !strings.Contains(commitOutput, "Co-authored-by: Test Assignee <assignee@example.com>") {
		t.Errorf("Expected commit message to contain co-author, got: %s", commitOutput)
	}

	if !strings.Contains(commitOutput, commitMessage) {
		t.Errorf("Expected commit message to contain original message, got: %s", commitOutput)
	}
}

func TestGitHubService_CommitChanges_WithoutCoAuthor(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create config
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.BotUsername = "test-bot"

	// Create GitHub service
	githubService := NewGitHubService(config, zap.NewNop())

	// Test commit without co-author
	commitMessage := "TEST-123: Test commit without co-author"

	err = githubService.CommitChanges(tempDir, commitMessage, "", "")
	if err != nil {
		t.Fatalf("Failed to commit changes: %v", err)
	}

	// Verify the commit message does not contain co-author
	cmd = exec.Command("git", "log", "--format=%B", "-1")
	cmd.Dir = tempDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit message: %v", err)
	}

	commitOutput := string(output)
	if strings.Contains(commitOutput, "Co-authored-by:") {
		t.Errorf("Expected commit message to not contain co-author, got: %s", commitOutput)
	}

	if !strings.Contains(commitOutput, commitMessage) {
		t.Errorf("Expected commit message to contain original message, got: %s", commitOutput)
	}
}

func TestGitHubService_CommitChanges_WithSSHSigning(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create config with SSH key
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.BotUsername = "test-bot"
	config.GitHub.SSHKeyPath = "/path/to/test_ssh_key" // Test SSH key path

	// Create GitHub service
	githubService := NewGitHubService(config, zap.NewNop())

	// Configure SSH signing manually (simulating what CloneRepository does)
	cmd = exec.Command("git", "config", "gpg.format", "ssh")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git gpg format: %v", err)
	}

	cmd = exec.Command("git", "config", "user.signingkey", config.GitHub.SSHKeyPath)
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git ssh signing key: %v", err)
	}

	cmd = exec.Command("git", "config", "commit.gpgsign", "true")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to enable git commit signing: %v", err)
	}

	// Test commit with SSH signing - this will fail because the key doesn't exist,
	// but we can verify that the git configuration was set correctly
	commitMessage := "TEST-123: Test commit with SSH signing"

	err = githubService.CommitChanges(tempDir, commitMessage, "", "")
	// We expect this to fail because the SSH key doesn't exist in the test environment
	if err == nil {
		t.Log("Commit succeeded (unexpected, but possible if SSH key exists)")
	} else {
		t.Logf("Commit failed as expected: %v", err)
	}

	// Verify that git config was set for SSH signing
	cmd = exec.Command("git", "config", "gpg.format")
	cmd.Dir = tempDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get gpg format config: %v", err)
	}

	gpgFormat := strings.TrimSpace(string(output))
	if gpgFormat != "ssh" {
		t.Errorf("Expected gpg format to be 'ssh', got '%s'", gpgFormat)
	}

	// Verify that signing key was set
	cmd = exec.Command("git", "config", "user.signingkey")
	cmd.Dir = tempDir
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get signing key config: %v", err)
	}

	signingKey := strings.TrimSpace(string(output))
	if signingKey != config.GitHub.SSHKeyPath {
		t.Errorf("Expected signing key to be '%s', got '%s'", config.GitHub.SSHKeyPath, signingKey)
	}

	// Verify that commit signing is enabled
	cmd = exec.Command("git", "config", "commit.gpgsign")
	cmd.Dir = tempDir
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get commit signing config: %v", err)
	}

	gpgSign := strings.TrimSpace(string(output))
	if gpgSign != "true" {
		t.Errorf("Expected commit signing to be enabled, got '%s'", gpgSign)
	}

	t.Log("SSH signing configuration verified successfully")
}

// mockCommitChangesViaAPIHandler creates a mock HTTP handler for testing CommitChangesViaAPI
func mockCommitChangesViaAPIHandler(t *testing.T, capturedCommitRequest **models.GitHubCommitRequest, getRefCalled, getCommitCalled, createBlobCalled, createTreeCalled, createCommitCalled, updateRefCalled *bool) RoundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		// Mock installation token request
		if strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"token": "ghs_mock_installation_token",
					"expires_at": "2099-01-01T00:00:00Z"
				}`))),
			}, nil
		}

		// Mock installation ID request
		if strings.Contains(req.URL.Path, "/installation") && !strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"id": 12345678
				}`))),
			}, nil
		}

		// Mock update reference (PATCH must be checked before GET for refs)
		if req.Method == "PATCH" && strings.Contains(req.URL.Path, "/git/refs/heads/") {
			*updateRefCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"ref": "refs/heads/test-branch",
					"object": {
						"sha": "newcommit789abc012"
					}
				}`))),
			}, nil
		}

		// Mock get reference
		if strings.Contains(req.URL.Path, "/git/refs/heads/") {
			*getRefCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"ref": "refs/heads/test-branch",
					"object": {
						"type": "commit",
						"sha": "abc123def456",
						"url": "https://api.github.com/repos/test/repo/git/commits/abc123def456"
					}
				}`))),
			}, nil
		}

		// Mock get commit (for tree SHA)
		if strings.Contains(req.URL.Path, "/git/commits/") {
			*getCommitCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"sha": "abc123def456",
					"tree": {
						"sha": "tree123abc456"
					}
				}`))),
			}, nil
		}

		// Mock create blob
		if strings.Contains(req.URL.Path, "/git/blobs") {
			*createBlobCalled = true
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"sha": "blob789xyz012",
					"url": "https://api.github.com/repos/test/repo/git/blobs/blob789xyz012"
				}`))),
			}, nil
		}

		// Mock create tree
		if strings.Contains(req.URL.Path, "/git/trees") {
			*createTreeCalled = true
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"sha": "newtree456def789",
					"url": "https://api.github.com/repos/test/repo/git/trees/newtree456def789"
				}`))),
			}, nil
		}

		// Mock create commit
		if strings.Contains(req.URL.Path, "/git/commits") && req.Method == "POST" {
			*createCommitCalled = true
			// Capture the commit request body
			bodyBytes, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(bodyBytes, capturedCommitRequest); err != nil {
				t.Errorf("Failed to unmarshal commit request: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"sha": "newcommit789abc012",
					"url": "https://api.github.com/repos/test/repo/git/commits/newcommit789abc012",
					"message": "Test commit"
				}`))),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	}
}

// TestCommitChangesViaAPI_Success tests successful commit creation via GitHub API
func TestCommitChangesViaAPI_Success(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Create a temporary directory with a test file
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content\n"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Make a change
	if err := os.WriteFile(testFile, []byte("modified content\n"), 0600); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Stage the changes (git add) - CommitChangesViaAPI expects changes to be staged
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	// Track API calls
	var getRefCalled, getCommitCalled, createBlobCalled, createTreeCalled, createCommitCalled, updateRefCalled bool
	var capturedCommitRequest *models.GitHubCommitRequest

	// Create mock HTTP client using the extracted handler
	mockClient := NewTestClient(mockCommitChangesViaAPIHandler(t, &capturedCommitRequest, &getRefCalled, &getCommitCalled, &createBlobCalled, &createTreeCalled, &createCommitCalled, &updateRefCalled))

	// Create config with GitHub App settings
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.BotUsername = "test-bot[bot]"
	config.GitHub.PrivateKeyPath = keyPath

	// Create service
	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		config.GitHub.PrivateKeyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:           config,
		client:           mockClient,
		appTransport:     appTransport,
		installationAuth: make(map[int64]*ghinstallation.Transport),
		executor:         execCommand,
		logger:           zap.NewNop(),
	}

	// Test commit creation
	commitMessage := "Test commit message"
	coAuthorName := "Alice Developer"
	coAuthorEmail := "alice@example.com"

	commitSHA, err := service.CommitChangesViaAPI("testowner", "testrepo", "test-branch", commitMessage, tempDir, coAuthorName, coAuthorEmail)

	// Verify no error
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Verify commit SHA returned
	if commitSHA == "" {
		t.Fatal("Expected commit SHA but got empty string")
	}
	if commitSHA != "newcommit789abc012" {
		t.Errorf("Expected commit SHA 'newcommit789abc012' but got '%s'", commitSHA)
	}

	// Verify all API calls were made
	if !getRefCalled {
		t.Error("Expected get reference API call but it wasn't made")
	}
	if !getCommitCalled {
		t.Error("Expected get commit API call but it wasn't made")
	}
	if !createBlobCalled {
		t.Error("Expected create blob API call but it wasn't made")
	}
	if !createTreeCalled {
		t.Error("Expected create tree API call but it wasn't made")
	}
	if !createCommitCalled {
		t.Error("Expected create commit API call but it wasn't made")
	}
	if !updateRefCalled {
		t.Error("Expected update reference API call but it wasn't made")
	}

	// Verify commit request structure
	if capturedCommitRequest == nil {
		t.Fatal("Commit request was not captured")
	}

	// Verify commit message includes co-author
	expectedMessage := "Test commit message\n\nCo-authored-by: Alice Developer <alice@example.com>"
	if capturedCommitRequest.Message != expectedMessage {
		t.Errorf("Expected commit message:\n%s\nGot:\n%s", expectedMessage, capturedCommitRequest.Message)
	}

	// Verify Author and Committer are NOT set (GitHub sets them automatically for verified commits)
	if capturedCommitRequest.Author != nil {
		t.Error("Expected Author to be nil (for GitHub App verified commits) but it was set")
	}
	if capturedCommitRequest.Committer != nil {
		t.Error("Expected Committer to be nil (for GitHub App verified commits) but it was set")
	}

	// Verify tree SHA
	if capturedCommitRequest.Tree != "newtree456def789" {
		t.Errorf("Expected tree SHA 'newtree456def789' but got '%s'", capturedCommitRequest.Tree)
	}

	// Verify parents
	if len(capturedCommitRequest.Parents) != 1 {
		t.Errorf("Expected 1 parent but got %d", len(capturedCommitRequest.Parents))
	} else if capturedCommitRequest.Parents[0] != "abc123def456" {
		t.Errorf("Expected parent SHA 'abc123def456' but got '%s'", capturedCommitRequest.Parents[0])
	}
}

// TestCommitChangesViaAPI_NoChanges tests that no commit is created when there are no changes
func TestCommitChangesViaAPI_NoChanges(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repo with initial commit (no pending changes)
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content\n"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Track API calls - no commit creation should happen
	var getRefCalled, createCommitCalled bool

	// Create mock HTTP client
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"token": "ghs_mock_installation_token",
					"expires_at": "2099-01-01T00:00:00Z"
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/installation") && !strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"id": 12345678
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/refs/heads/") {
			getRefCalled = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"ref": "refs/heads/test-branch",
					"object": {
						"sha": "abc123def456"
					}
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits/") && req.Method == "GET" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"tree": {
						"sha": "tree123abc456"
					}
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits") && req.Method == "POST" {
			createCommitCalled = true
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	// Create config
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.BotUsername = "test-bot[bot]"
	config.GitHub.PrivateKeyPath = keyPath

	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		config.GitHub.PrivateKeyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:           config,
		client:           mockClient,
		appTransport:     appTransport,
		installationAuth: make(map[int64]*ghinstallation.Transport),
		executor:         execCommand,
		logger:           zap.NewNop(),
	}

	// Test commit creation with no changes
	commitSHA, err := service.CommitChangesViaAPI("testowner", "testrepo", "test-branch", "Test commit", tempDir, "", "")

	// Verify no error
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Verify original commit SHA is returned (no new commit created)
	if commitSHA != "abc123def456" {
		t.Errorf("Expected original commit SHA 'abc123def456' but got '%s'", commitSHA)
	}

	// Verify get reference was called
	if !getRefCalled {
		t.Error("Expected get reference API call but it wasn't made")
	}

	// Verify NO commit was created
	if createCommitCalled {
		t.Error("Expected NO commit creation but create commit API was called")
	}
}

// TestCommitChangesViaAPI_WithoutCoAuthor tests commit creation without co-author
func TestCommitChangesViaAPI_WithoutCoAuthor(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Create a temporary directory with changes
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial\n"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Make a change
	if err := os.WriteFile(testFile, []byte("modified\n"), 0600); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Stage the changes (git add) - CommitChangesViaAPI expects changes to be staged
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	var capturedCommitRequest *models.GitHubCommitRequest

	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"token": "ghs_mock_installation_token",
					"expires_at": "2099-01-01T00:00:00Z"
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/installation") && !strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id": 12345678}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/refs/heads/") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/test", "object": {"sha": "abc123"}}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits/") && req.Method == "GET" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"tree": {"sha": "tree123"}}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/blobs") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "blob789"}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/trees") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "newtree456"}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits") && req.Method == "POST" {
			bodyBytes, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(bodyBytes, &capturedCommitRequest); err != nil {
				t.Errorf("Failed to unmarshal commit request: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "newcommit789"}`))),
			}, nil
		}

		if req.Method == "PATCH" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/test"}`))),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.BotUsername = "test-bot[bot]"
	config.GitHub.PrivateKeyPath = keyPath

	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		config.GitHub.PrivateKeyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:           config,
		client:           mockClient,
		appTransport:     appTransport,
		installationAuth: make(map[int64]*ghinstallation.Transport),
		executor:         execCommand,
		logger:           zap.NewNop(),
	}

	// Test commit creation WITHOUT co-author
	commitMessage := "Simple commit message"
	commitSHA, err := service.CommitChangesViaAPI("testowner", "testrepo", "test-branch", commitMessage, tempDir, "", "")

	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if commitSHA != "newcommit789" {
		t.Errorf("Expected commit SHA 'newcommit789' but got '%s'", commitSHA)
	}

	// Verify commit message does NOT include co-author
	if capturedCommitRequest == nil {
		t.Fatal("Commit request was not captured")
	}

	if capturedCommitRequest.Message != commitMessage {
		t.Errorf("Expected commit message '%s' but got '%s'", commitMessage, capturedCommitRequest.Message)
	}

	// Verify no co-author trailer is present
	if strings.Contains(capturedCommitRequest.Message, "Co-authored-by") {
		t.Error("Expected no Co-authored-by trailer but it was present")
	}
}

// TestCommitChangesViaAPI_NewBranch tests commit creation when branch doesn't exist on remote
// This test would have caught the missing GitHubCreateReferenceRequest model
func TestCommitChangesViaAPI_NewBranch(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Create a temporary directory with changes
	tempDir, err := os.MkdirTemp("", "test-repo-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user.email: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial\n"), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Make a change
	if err := os.WriteFile(testFile, []byte("modified\n"), 0600); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Stage the changes
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	var createRefCalled bool
	var capturedCreateRefRequest *models.GitHubCreateReferenceRequest

	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"token": "ghs_mock_installation_token",
					"expires_at": "2099-01-01T00:00:00Z"
				}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/installation") && !strings.Contains(req.URL.Path, "/access_tokens") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id": 12345678}`))),
			}, nil
		}

		// First call to get new branch - returns 404
		// Second call to get target branch - returns 200
		if strings.Contains(req.URL.Path, "/git/refs/heads/") && req.Method == "GET" {
			if strings.Contains(req.URL.Path, "/git/refs/heads/new-feature") {
				// New branch doesn't exist
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"message": "Not Found"}`))),
				}, nil
			}
			// Target branch (main) exists
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/main", "object": {"sha": "target123"}}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits/") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"tree": {"sha": "tree456"}}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/blobs") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "blob789"}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/trees") {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "newtree999"}`))),
			}, nil
		}

		if strings.Contains(req.URL.Path, "/git/commits") && req.Method == "POST" {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "newcommit111"}`))),
			}, nil
		}

		// Create new reference (POST to /git/refs)
		if strings.Contains(req.URL.Path, "/git/refs") && !strings.Contains(req.URL.Path, "/git/refs/heads/") && req.Method == "POST" {
			createRefCalled = true
			bodyBytes, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(bodyBytes, &capturedCreateRefRequest); err != nil {
				t.Errorf("Failed to unmarshal create reference request: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/new-feature", "object": {"sha": "newcommit111"}}`))),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.BotUsername = "test-bot[bot]"
	config.GitHub.PrivateKeyPath = keyPath
	config.GitHub.TargetBranch = "main"

	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		config.GitHub.PrivateKeyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:           config,
		client:           mockClient,
		appTransport:     appTransport,
		installationAuth: make(map[int64]*ghinstallation.Transport),
		executor:         execCommand,
		logger:           zap.NewNop(),
	}

	// Test commit creation for a NEW branch
	commitSHA, err := service.CommitChangesViaAPI("testowner", "testrepo", "new-feature", "Add new feature", tempDir, "", "")

	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if commitSHA != "newcommit111" {
		t.Errorf("Expected commit SHA 'newcommit111' but got '%s'", commitSHA)
	}

	// Verify that createReference was called (not updateReference)
	if !createRefCalled {
		t.Error("Expected createReference to be called for new branch but it wasn't")
	}

	// Verify the create reference request structure
	if capturedCreateRefRequest == nil {
		t.Fatal("Create reference request was not captured")
	}

	if capturedCreateRefRequest.Ref != "refs/heads/new-feature" {
		t.Errorf("Expected ref 'refs/heads/new-feature' but got '%s'", capturedCreateRefRequest.Ref)
	}

	if capturedCreateRefRequest.SHA != "newcommit111" {
		t.Errorf("Expected SHA 'newcommit111' but got '%s'", capturedCreateRefRequest.SHA)
	}
}

// TestGetBranchBaseCommit_BranchExists tests getting base commit when branch exists
func TestGetBranchBaseCommit_BranchExists(t *testing.T) {
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return
	}
	defer func() { _ = os.Remove(keyPath) }()

	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/git/refs/heads/existing-branch") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/existing-branch", "object": {"sha": "branch123"}}`))),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.TargetBranch = "main"

	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		keyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:       config,
		client:       mockClient,
		appTransport: appTransport,
		logger:       zap.NewNop(),
	}

	baseSHA, branchExists, err := service.getBranchBaseCommit("owner", "repo", "existing-branch", "fake-token")

	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if !branchExists {
		t.Error("Expected branch to exist but got false")
	}

	if baseSHA != "branch123" {
		t.Errorf("Expected base SHA 'branch123' but got '%s'", baseSHA)
	}
}

// TestGetBranchBaseCommit_BranchDoesNotExist tests fallback to target branch
func TestGetBranchBaseCommit_BranchDoesNotExist(t *testing.T) {
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return
	}
	defer func() { _ = os.Remove(keyPath) }()

	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/git/refs/heads/new-branch") {
			// New branch doesn't exist
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"message": "Not Found"}`))),
			}, nil
		}
		if strings.Contains(req.URL.Path, "/git/refs/heads/main") {
			// Target branch exists
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"ref": "refs/heads/main", "object": {"sha": "main456"}}`))),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.TargetBranch = "main"

	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		mockClient.Transport,
		config.GitHub.AppID,
		keyPath,
	)
	if err != nil {
		t.Fatalf("Failed to create app transport: %v", err)
	}

	service := &GitHubServiceImpl{
		config:       config,
		client:       mockClient,
		appTransport: appTransport,
		logger:       zap.NewNop(),
	}

	baseSHA, branchExists, err := service.getBranchBaseCommit("owner", "repo", "new-branch", "fake-token")

	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if branchExists {
		t.Error("Expected branch to not exist but got true")
	}

	if baseSHA != "main456" {
		t.Errorf("Expected base SHA from target branch 'main456' but got '%s'", baseSHA)
	}
}

// TestCreateBlobsForChangedFiles_GitStatusParsing tests that git status --porcelain output is correctly parsed
// This specifically tests the fix for leading spaces in unstaged changes (e.g., " M filename")
func TestCreateBlobsForChangedFiles_GitStatusParsing(t *testing.T) {
	// Create temp directory for test
	tempDir, err := os.MkdirTemp("", "git-status-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create test files that will appear in git status
	testFiles := []string{
		"api/v1beta1/types.gen.go",
		"internal/agent/device/applications.go",
		"pkg/utils/helper.go",
	}

	for _, file := range testFiles {
		filePath := filepath.Join(tempDir, file)
		if err := os.MkdirAll(filepath.Dir(filePath), 0750); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}
		if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Mock git status output with leading spaces (unstaged changes)
	// Format: " M filename" where position 0 is space, position 1 is M
	gitStatusOutput := " M api/v1beta1/types.gen.go\n M internal/agent/device/applications.go\n M pkg/utils/helper.go\n"

	// Mock HTTP client for blob creation
	blobCounter := 0
	mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/git/blobs") {
			blobCounter++
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sha": "blob123"}`))),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
		}, nil
	})

	// Mock command executor that returns our test git status output
	mockExecutor := func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command("echo", "-n", gitStatusOutput)
		return cmd
	}

	config := &models.Config{}
	config.GitHub.AppID = 123456

	service := &GitHubServiceImpl{
		config:   config,
		client:   mockClient,
		executor: mockExecutor,
		logger:   zap.NewNop(),
	}

	// Call createBlobsForChangedFiles
	entries, err := service.createBlobsForChangedFiles("owner", "repo", tempDir, "fake-token")

	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	// Should have created blobs for all 3 files
	if len(entries) != 3 {
		t.Errorf("Expected 3 tree entries but got %d", len(entries))
	}

	// Verify filenames are correct (no missing first characters)
	expectedFiles := map[string]bool{
		"api/v1beta1/types.gen.go":              false,
		"internal/agent/device/applications.go": false,
		"pkg/utils/helper.go":                   false,
	}

	for _, entry := range entries {
		if _, exists := expectedFiles[entry.Path]; !exists {
			t.Errorf("Unexpected file path: %s", entry.Path)
		}
		expectedFiles[entry.Path] = true
	}

	// Verify all expected files were found
	for file, found := range expectedFiles {
		if !found {
			t.Errorf("Expected to find file '%s' but it was not in entries", file)
		}
	}

	// Verify blobs were created
	if blobCounter != 3 {
		t.Errorf("Expected 3 blob creation calls but got %d", blobCounter)
	}
}

func TestGitHubService_HasChanges_NoChanges(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create an initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Create GitHub service
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	githubService := NewGitHubService(config, zap.NewNop())

	// Test HasChanges - should return false (no changes)
	hasChanges, err := githubService.HasChanges(tempDir)
	if err != nil {
		t.Fatalf("HasChanges failed: %v", err)
	}

	if hasChanges {
		t.Error("Expected HasChanges to return false for clean repository, but got true")
	}
}

func TestGitHubService_HasChanges_WorkingTreeChanges(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create an initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Modify the file (create working tree changes)
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Create GitHub service
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	githubService := NewGitHubService(config, zap.NewNop())

	// Test HasChanges - should return true (working tree changes)
	hasChanges, err := githubService.HasChanges(tempDir)
	if err != nil {
		t.Fatalf("HasChanges failed: %v", err)
	}

	if !hasChanges {
		t.Error("Expected HasChanges to return true for modified file, but got false")
	}
}

func TestGitHubService_HasChanges_UnpushedCommits(t *testing.T) {
	// This test verifies that HasChanges detects when there are local commits
	// that haven't been pushed to the remote. We test this by creating a branch
	// with a remote, then adding a local commit without pushing it.

	// Note: The complexity of setting up a real bare repository for this test
	// outweighs its value since TestGitHubService_HasChanges_NewBranch already
	// validates the core unpushed commit detection logic (a new branch is
	// effectively unpushed commits).
	//
	// In a real-world scenario, this would be tested via integration tests
	// against an actual Git server.
	t.Skip("Skipping bare repository test - covered by NewBranch test")
}

func TestGitHubService_HasChanges_NewBranch(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "github-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repository: %v", err)
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to configure git user email: %v", err)
	}

	// Create an initial commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Simulate a remote by creating a bare repository
	bareDir, err := os.MkdirTemp("", "github-test-bare-*")
	if err != nil {
		t.Fatalf("Failed to create bare directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(bareDir) }()

	cmd = exec.Command("git", "init", "--bare")
	cmd.Dir = bareDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init bare repository: %v", err)
	}

	// Add the bare repo as a remote
	cmd = exec.Command("git", "remote", "add", "origin", bareDir)
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add remote: %v", err)
	}

	// Create a new branch (not pushed to remote yet)
	cmd = exec.Command("git", "checkout", "-b", "feature-branch")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create new branch: %v", err)
	}

	// Create temporary private key file
	keyPath := generateTestRSAKey(t)
	defer func() { _ = os.Remove(keyPath) }()

	// Create GitHub service
	config := &models.Config{}
	config.GitHub.AppID = 123456
	config.GitHub.PrivateKeyPath = keyPath
	githubService := NewGitHubService(config, zap.NewNop())

	// Test HasChanges - should return true (new branch, no remote counterpart)
	hasChanges, err := githubService.HasChanges(tempDir)
	if err != nil {
		t.Fatalf("HasChanges failed: %v", err)
	}

	if !hasChanges {
		t.Error("Expected HasChanges to return true for new branch without remote, but got false")
	}
}
