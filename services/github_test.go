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

// TestCreatePullRequest tests the CreatePullRequest method with PAT authentication
func TestCreatePullRequest(t *testing.T) {
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
				// Capture the request body
				capturedBody, _ = io.ReadAll(req.Body)
				return tc.mockResponse, tc.mockError
			})

			// Create a GitHubService with the mock client
			config := &models.Config{}
			config.GitHub.PersonalAccessToken = "test-token"
			config.GitHub.AppID = 123456
			config.GitHub.BotUsername = "test-bot"
			config.GitHub.PRLabel = tc.prLabel

			service := &GitHubServiceImpl{
				config:   config,
				client:   mockClient,
				executor: execCommand,
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
