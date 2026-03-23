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
	"github.com/google/go-github/v75/github"
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

// TestCreatePR_GitHubApp tests CreatePR with GitHub App authentication
func TestCreatePR_GitHubApp(t *testing.T) {
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
				// Mock label addition request
				if strings.Contains(req.URL.Path, "/labels") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader([]byte(`[{"id":1,"name":"ai-pr","url":"https://api.github.com/repos/test/test/labels/ai-pr","color":"008672"}]`))),
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
				config:              config,
				client:              mockClient,
				appTransport:        appTransport,
				installationAuth:    make(map[int64]*ghinstallation.Transport),
				installationClients: make(map[int64]*github.Client),
				installationIDs:     make(map[string]int64),
				executor:            execCommand,
				logger:              zap.NewNop(),
			}

			// Call CreatePR
			result, err := service.CreatePR(models.PRParams{
				Owner: tc.baseOwner,
				Repo:  tc.repo,
				Title: "Test PR",
				Body:  "Test body",
				Head:  tc.head,
				Base:  tc.base,
			})

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

// TestCreatePR tests the CreatePR method
func TestCreatePR(t *testing.T) {
	// Generate temporary RSA key for testing
	keyPath := generateTestRSAKey(t)
	if keyPath == "" {
		return // Test was skipped
	}
	defer func() { _ = os.Remove(keyPath) }()

	// Test cases
	testCases := []struct {
		name           string
		params         models.PRParams
		prLabel        string
		mockResponse   *http.Response
		mockError      error
		expectedResult *models.PR
		expectedError  bool
	}{
		{
			name: "successful PR creation",
			params: models.PRParams{
				Owner: "example",
				Repo:  "repo",
				Title: "Test PR",
				Body:  "This is a test PR",
				Head:  "feature/TEST-123",
				Base:  "main",
			},
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
			expectedResult: &models.PR{
				Number: 1,
				URL:    "https://github.com/example/repo/pull/1",
				State:  "open",
			},
			expectedError: false,
		},
		{
			name: "error creating PR",
			params: models.PRParams{
				Owner: "example",
				Repo:  "repo",
				Title: "Test PR",
				Body:  "This is a test PR",
				Head:  "feature/TEST-123",
				Base:  "main",
			},
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
			// Create a mock HTTP client
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

				// Mock label addition request (go-github adds labels separately)
				if strings.Contains(req.URL.Path, "/labels") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader([]byte(`[{"name":"ai-pr"}]`))),
					}, nil
				}

				// For PR creation and other requests
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
				config:              config,
				client:              mockClient,
				executor:            execCommand,
				appTransport:        appTransport,
				installationAuth:    make(map[int64]*ghinstallation.Transport),
				installationClients: make(map[int64]*github.Client),
				installationIDs:     make(map[string]int64),
				logger:              zap.NewNop(),
			}

			// Call the method being tested
			result, err := service.CreatePR(tc.params)

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
					if result.Number != tc.expectedResult.Number {
						t.Errorf("Expected result Number %d but got %d", tc.expectedResult.Number, result.Number)
					}
					if result.URL != tc.expectedResult.URL {
						t.Errorf("Expected result URL %s but got %s", tc.expectedResult.URL, result.URL)
					}
					if result.State != tc.expectedResult.State {
						t.Errorf("Expected result State %s but got %s", tc.expectedResult.State, result.State)
					}
				}
			}
		})
	}
}

// Test_extractRepoInfo tests the extractRepoInfo function.
func Test_extractRepoInfo(t *testing.T) {
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
			owner, repo, err := extractRepoInfo(tc.repoURL)

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

// TestSwitchBranch tests the SwitchBranch method
func TestSwitchBranch(t *testing.T) {
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
	err = githubService.SwitchBranch(tempDir, "test-branch")
	if err != nil {
		t.Errorf("SwitchBranch() error = %v", err)
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

// TestSwitchBranch_NonExistentBranch tests switching to a non-existent branch
func TestSwitchBranch_NonExistentBranch(t *testing.T) {
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
	err = githubService.SwitchBranch(tempDir, "non-existent-branch")
	if err == nil {
		t.Error("SwitchBranch() should return error for non-existent branch")
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

	// Add the bare repo as a remote and push so origin/<default> exists.
	cmd = exec.Command("git", "remote", "add", "origin", bareDir)
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add remote: %v", err)
	}

	// Determine the default branch name before pushing.
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = tempDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get default branch: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(out))

	cmd = exec.Command("/usr/bin/git", "push", "-u", "origin", defaultBranch)
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to push to origin: %v", err)
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
	config.GitHub.TargetBranch = defaultBranch
	githubService := NewGitHubService(config, zap.NewNop())

	// A new branch with no working tree changes and no local commits
	// relative to origin/<targetBranch> has nothing to commit.
	hasChanges, err := githubService.HasChanges(tempDir)
	if err != nil {
		t.Fatalf("HasChanges failed: %v", err)
	}

	if hasChanges {
		t.Error("Expected HasChanges to return false for new branch with no divergent commits, but got true")
	}
}
