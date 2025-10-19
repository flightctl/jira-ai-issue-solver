package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

// GitHubService defines the interface for interacting with GitHub
type GitHubService interface {
	// CloneRepository clones a repository to a local directory
	CloneRepository(repoURL, directory string) error

	// CreateBranch creates a new branch in a local repository
	CreateBranch(directory, branchName string) error

	// CommitChanges commits changes to a local repository
	CommitChanges(directory, message string, coAuthorName, coAuthorEmail string) error

	// PushChanges pushes changes to a remote repository
	PushChanges(directory, branchName string) error

	// CreatePullRequest creates a pull request
	CreatePullRequest(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error)

	// ForkRepository forks a repository and returns the clone URL of the fork
	ForkRepository(owner, repo string) (string, error)

	// CheckForkExists checks if a fork already exists for the given repository
	CheckForkExists(owner, repo string) (exists bool, cloneURL string, err error)

	// ResetFork resets a fork to match the original repository
	ResetFork(forkCloneURL, directory string) error

	// SyncForkWithUpstream syncs a fork with its upstream repository
	SyncForkWithUpstream(owner, repo string) error

	// SwitchToTargetBranch switches to the configured target branch after cloning
	SwitchToTargetBranch(directory string) error

	// SwitchToBranch switches to a specific branch
	SwitchToBranch(directory, branchName string) error

	// PullChanges pulls the latest changes from the remote branch
	PullChanges(directory, branchName string) error

	AddPRComment(owner, repo string, prNumber int, body string) error
	ListPRComments(owner, repo string, prNumber int) ([]models.GitHubPRComment, error)

	// GetPRDetails gets detailed PR information including reviews, comments, and files
	GetPRDetails(owner, repo string, prNumber int) (*models.GitHubPRDetails, error)

	// ListPRReviews lists all reviews on a PR
	ListPRReviews(owner, repo string, prNumber int) ([]models.GitHubReview, error)
}

// GitHubServiceImpl implements the GitHubService interface
type GitHubServiceImpl struct {
	config   *models.Config
	client   *http.Client
	executor models.CommandExecutor
	logger   *zap.Logger
}

// NewGitHubService creates a new GitHubService
func NewGitHubService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) GitHubService {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}

	return &GitHubServiceImpl{
		config:   config,
		client:   &http.Client{},
		executor: commandExecutor,
		logger:   logger,
	}
}

// CloneRepository clones a repository to a local directory
func (s *GitHubServiceImpl) CloneRepository(repoURL, directory string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if the directory is already a git repository
	if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
		// Directory is already a git repository, fetch the latest changes
		cmd := s.executor("git", "fetch", "origin")
		cmd.Dir = directory

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch repository: %w, stderr: %s", err, stderr.String())
		}

		// Reset to origin/main or origin/master to ensure we're up to date
		cmd = s.executor("git", "reset", "--hard", "origin/main")
		cmd.Dir = directory

		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// Try with master branch
			cmd = s.executor("git", "reset", "--hard", "origin/master")
			cmd.Dir = directory

			stderr.Reset()
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to reset to origin/main or origin/master: %w, stderr: %s", err, stderr.String())
			}
		}

		// Clean the repository
		cmd = s.executor("git", "clean", "-fdx")
		cmd.Dir = directory

		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to clean repository: %w, stderr: %s", err, stderr.String())
		}
	} else {
		// Clone the repository
		cmd := s.executor("git", "clone", repoURL, directory)

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to clone repository: %w, stderr: %s", err, stderr.String())
		}
	}

	// Configure git user for GitHub App
	cmd := s.executor("git", "config", "user.name", s.config.GitHub.BotUsername)
	cmd.Dir = directory

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git user name: %w", err)
	}

	cmd = s.executor("git", "config", "user.email", s.config.GitHub.BotEmail)
	cmd.Dir = directory

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git user email: %w", err)
	}

	// Configure SSH signing if a key is specified
	if s.config.GitHub.SSHKeyPath != "" {
		cmd = s.executor("git", "config", "gpg.format", "ssh")
		cmd.Dir = directory
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to configure git gpg format: %w", err)
		}

		cmd = s.executor("git", "config", "user.signingkey", s.config.GitHub.SSHKeyPath)
		cmd.Dir = directory
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to configure git ssh signing key: %w", err)
		}

		cmd = s.executor("git", "config", "commit.gpgsign", "true")
		cmd.Dir = directory
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to enable git commit signing: %w", err)
		}

		s.logger.Info("Configured SSH signing for repository", zap.String("sshKeyPath", s.config.GitHub.SSHKeyPath))
	}

	// Configure git to use the GitHub token for authentication
	// This prevents credential prompts during push operations
	cmd = s.executor("git", "config", "credential.helper", "store")
	cmd.Dir = directory

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git credential helper: %w", err)
	}

	// Set up the credential URL with token
	token, err := s.getAuthToken()
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	// Configure the remote URL to include the token
	// Extract owner and repo from the URL
	owner, repo, err := ExtractRepoInfo(repoURL)
	if err != nil {
		return fmt.Errorf("failed to extract repo info: %w", err)
	}

	// Set the remote URL with embedded token
	authURL := fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo)
	cmd = s.executor("git", "remote", "set-url", "origin", authURL)
	cmd.Dir = directory

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set remote URL with token: %w", err)
	}

	return nil
}

// getAuthToken returns the GitHub Personal Access Token for API calls
func (s *GitHubServiceImpl) getAuthToken() (string, error) {
	if s.config.GitHub.PersonalAccessToken == "" {
		return "", fmt.Errorf("the Personal Access Token is not configured")
	}
	return s.config.GitHub.PersonalAccessToken, nil
}

// CreateBranch creates a new branch in a local repository based on the latest target branch
func (s *GitHubServiceImpl) CreateBranch(directory, branchName string) error {
	// Fetch the latest changes from origin
	cmd := s.executor("git", "fetch", "origin")
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, stderr.String())
	}

	// Checkout the target branch
	cmd = s.executor("git", "checkout", s.config.GitHub.TargetBranch)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to checkout target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, stderr.String())
	}

	// Reset to the latest commit on the target branch to ensure we're up to date
	cmd = s.executor("git", "reset", "--hard", "origin/"+s.config.GitHub.TargetBranch)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reset to latest commit on target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, stderr.String())
	}

	// Check if the branch already exists locally
	cmd = s.executor("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	cmd.Dir = directory

	if err := cmd.Run(); err == nil {
		// Branch exists locally, delete it first
		s.logger.Info("Branch %s already exists locally, deleting it", zap.String("branchName", branchName))
		cmd = s.executor("git", "branch", "-D", branchName)
		cmd.Dir = directory

		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete existing branch %s: %w, stderr: %s", branchName, err, stderr.String())
		}
	}

	// Create a new branch from the current state
	cmd = s.executor("git", "checkout", "-b", branchName)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create branch: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

// CommitChanges commits changes to a local repository
func (s *GitHubServiceImpl) CommitChanges(directory, message string, coAuthorName, coAuthorEmail string) error {
	// Add all changes
	cmd := s.executor("git", "add", ".")
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add changes: %w, stderr: %s", err, stderr.String())
	}

	// Check if there are changes to commit
	cmd = s.executor("git", "status", "--porcelain")
	cmd.Dir = directory

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to check status: %w", err)
	}

	if stdout.Len() == 0 {
		// No changes to commit
		return nil
	}

	// Build commit message with optional co-author
	commitMessage := message
	if coAuthorName != "" && coAuthorEmail != "" {
		commitMessage = fmt.Sprintf("%s\n\nCo-authored-by: %s <%s>", message, coAuthorName, coAuthorEmail)
	}

	// Commit changes (SSH signing is handled by git config)
	cmd = s.executor("git", "commit", "-m", commitMessage)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit changes: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

// PushChanges pushes changes to a remote repository
func (s *GitHubServiceImpl) PushChanges(directory, branchName string) error {
	// Ensure git is configured to not prompt for credentials
	cmd := s.executor("git", "config", "credential.helper", "store")
	cmd.Dir = directory

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git credential helper: %w", err)
	}

	// Push the changes
	cmd = s.executor("git", "push", "-u", "origin", branchName)
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to push changes: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

// CreatePullRequest creates a pull request
func (s *GitHubServiceImpl) CreatePullRequest(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)

	payload := models.GitHubCreatePRRequest{
		Title:  title,
		Body:   body,
		Head:   head,
		Base:   base,
		Labels: []string{s.config.GitHub.PRLabel},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Get authentication token
	token, err := s.getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create pull request: %s, status code: %d", string(body), resp.StatusCode)
	}

	var prResponse models.GitHubCreatePRResponse
	if err := json.NewDecoder(resp.Body).Decode(&prResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &prResponse, nil
}

// CheckForkExists checks if a fork already exists for the given repository
func (s *GitHubServiceImpl) CheckForkExists(owner, repo string) (exists bool, cloneURL string, err error) {
	// Get authentication token
	token, err := s.getAuthToken()
	if err != nil {
		return false, "", fmt.Errorf("failed to get auth token: %w", err)
	}

	// Check if the fork already exists by listing the bot's repositories
	url := fmt.Sprintf("https://api.github.com/users/%s/repos", s.config.GitHub.BotUsername)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, "", fmt.Errorf("failed to create request: %w", err)
	}

	// Use the authentication token
	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("failed to list repositories: %s, status code: %d", string(body), resp.StatusCode)
	}

	var repos []struct {
		Name     string `json:"name"`
		CloneURL string `json:"clone_url"`
		Fork     bool   `json:"fork"`
		Source   struct {
			FullName string `json:"full_name"`
		} `json:"source"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return false, "", fmt.Errorf("failed to decode response: %w", err)
	}

	s.logger.Info("repos", zap.Any("repos", repos))

	// Check if any of the repositories is a fork of the target repository
	targetFullName := fmt.Sprintf("%s/%s", owner, repo)
	s.logger.Info("Looking for fork of", zap.String("targetFullName", targetFullName))

	for _, r := range repos {
		s.logger.Info("Checking repo", zap.String("repoName", r.Name), zap.Bool("isFork", r.Fork), zap.Any("source", r.Source))
		if r.Fork && r.Source.FullName == targetFullName {
			s.logger.Info("Found fork", zap.String("cloneURL", r.CloneURL))
			return true, r.CloneURL, nil
		}
		// Fallback: check if the repo name matches the target repo name
		if r.Fork && r.Name == repo {
			s.logger.Info("Found fork by name match", zap.String("cloneURL", r.CloneURL))
			return true, r.CloneURL, nil
		}
	}

	s.logger.Info("No fork found for", zap.String("targetFullName", targetFullName))
	return false, "", nil
}

// ResetFork resets a fork to match the original repository and sets up upstream
func (s *GitHubServiceImpl) ResetFork(forkCloneURL, directory string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if the directory is already a git repository
	if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
		// Directory is already a git repository, fetch and reset
		// Fetch the upstream repository
		cmd := s.executor("git", "fetch", "origin")
		cmd.Dir = directory

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, stderr.String())
		}

		// Reset to origin/main or origin/master
		cmd = s.executor("git", "reset", "--hard", "origin/main")
		cmd.Dir = directory

		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// Try with master branch
			cmd = s.executor("git", "reset", "--hard", "origin/master")
			cmd.Dir = directory

			stderr.Reset()
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to reset to origin/main or origin/master: %w, stderr: %s", err, stderr.String())
			}
		}

		// Clean the repository
		cmd = s.executor("git", "clean", "-fdx")
		cmd.Dir = directory

		stderr.Reset()
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to clean repository: %w, stderr: %s", err, stderr.String())
		}

		return nil
	}

	// Clone the repository
	return s.CloneRepository(forkCloneURL, directory)
}

// ForkRepository forks a repository and returns the clone URL of the fork
func (s *GitHubServiceImpl) ForkRepository(owner, repo string) (string, error) {
	// Get authentication token
	token, err := s.getAuthToken()
	if err != nil {
		return "", fmt.Errorf("failed to get auth token: %w", err)
	}

	// Create a new fork
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/forks", owner, repo)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Use the authentication token
	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to fork repository %s/%s: %s, status code: %d", owner, repo, string(body), resp.StatusCode)
	}

	var forkResponse struct {
		HTMLURL  string `json:"html_url"`
		CloneURL string `json:"clone_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&forkResponse); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return forkResponse.CloneURL, nil
}

// SyncForkWithUpstream syncs a fork with its upstream repository
func (s *GitHubServiceImpl) SyncForkWithUpstream(owner, repo string) error {
	// Get authentication token
	token, err := s.getAuthToken()
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	// Get the fork details to sync with upstream
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", s.config.GitHub.BotUsername, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get fork details: %s, status code: %d", string(body), resp.StatusCode)
	}

	var forkDetails struct {
		Source struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"source"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&forkDetails); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Sync the fork with upstream
	syncURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/merge-upstream", s.config.GitHub.BotUsername, repo)
	syncBody := map[string]string{
		"branch": "main",
	}

	jsonBody, err := json.Marshal(syncBody)
	if err != nil {
		return fmt.Errorf("failed to marshal sync request: %w", err)
	}

	req, err = http.NewRequest("POST", syncURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create sync request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err = s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send sync request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to sync fork: %s, status code: %d", string(body), resp.StatusCode)
	}

	return nil
}

// SwitchToTargetBranch switches to the configured target branch after cloning
func (s *GitHubServiceImpl) SwitchToTargetBranch(directory string) error {
	// Fetch the latest changes from origin
	cmd := s.executor("git", "fetch", "origin")
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, stderr.String())
	}

	// Checkout the target branch
	cmd = s.executor("git", "checkout", s.config.GitHub.TargetBranch)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to checkout target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, stderr.String())
	}

	// Reset to the latest commit on the target branch to ensure we're up to date
	cmd = s.executor("git", "reset", "--hard", "origin/"+s.config.GitHub.TargetBranch)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reset to latest commit on target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, stderr.String())
	}

	return nil
}

// SwitchToBranch switches to a specific branch
func (s *GitHubServiceImpl) SwitchToBranch(directory, branchName string) error {
	// Fetch the latest changes from origin
	cmd := s.executor("git", "fetch", "origin")
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, stderr.String())
	}

	// Checkout the specified branch
	cmd = s.executor("git", "checkout", branchName)
	cmd.Dir = directory

	stderr.Reset()
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w, stderr: %s", branchName, err, stderr.String())
	}

	return nil
}

// PullChanges pulls the latest changes from the remote branch
func (s *GitHubServiceImpl) PullChanges(directory, branchName string) error {
	// Pull the latest changes from the remote branch
	cmd := s.executor("git", "pull", "origin", branchName)
	cmd.Dir = directory

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull changes from origin/%s: %w, stderr: %s", branchName, err, stderr.String())
	}

	return nil
}

// AddPRComment posts a comment to a PR (issue) on GitHub
func (s *GitHubServiceImpl) AddPRComment(owner, repo string, prNumber int, body string) error {
	commentRequest := struct {
		Body string `json:"body"`
	}{Body: body}

	jsonPayload, err := json.Marshal(commentRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal comment request: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, prNumber)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthToken()
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add PR comment: %s, status: %d", string(body), resp.StatusCode)
	}

	return nil
}

// ListPRComments lists all comments on a PR (issue) on GitHub
func (s *GitHubServiceImpl) ListPRComments(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, prNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PR comments: %s, status: %d", string(body), resp.StatusCode)
	}

	var comments []models.GitHubPRComment
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil, fmt.Errorf("failed to decode comments: %w", err)
	}

	return comments, nil
}

// ExtractRepoInfo extracts owner and repo from a repository URL
func ExtractRepoInfo(repoURL string) (owner, repo string, err error) {
	// Handle SSH URLs: git@github.com:owner/repo.git
	if strings.HasPrefix(repoURL, "git@github.com:") {
		parts := strings.Split(strings.TrimPrefix(repoURL, "git@github.com:"), "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("invalid GitHub SSH URL: %s", repoURL)
		}
		owner = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		return owner, repo, nil
	}

	// Handle HTTPS URLs: https://github.com/owner/repo.git
	if strings.HasPrefix(repoURL, "https://github.com/") {
		parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("invalid GitHub HTTPS URL: %s", repoURL)
		}
		owner = parts[0]
		repo = strings.TrimSuffix(parts[1], ".git")
		return owner, repo, nil
	}

	return "", "", fmt.Errorf("unsupported repository URL format: %s", repoURL)
}

// GetPRDetails gets detailed PR information including reviews, comments, and files
func (s *GitHubServiceImpl) GetPRDetails(owner, repo string, prNumber int) (*models.GitHubPRDetails, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PR details: %s, status: %d", string(body), resp.StatusCode)
	}

	var prDetails models.GitHubPRDetails
	if err := json.NewDecoder(resp.Body).Decode(&prDetails); err != nil {
		return nil, fmt.Errorf("failed to decode PR details: %w", err)
	}

	// Get reviews
	reviews, err := s.ListPRReviews(owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR reviews: %w", err)
	}
	prDetails.Reviews = reviews

	// Get comments
	comments, err := s.ListPRComments(owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR comments: %w", err)
	}
	prDetails.Comments = comments

	return &prDetails, nil
}

// ListPRReviews lists all reviews on a PR
func (s *GitHubServiceImpl) ListPRReviews(owner, repo string, prNumber int) ([]models.GitHubReview, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/reviews", owner, repo, prNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PR reviews: %s, status: %d", string(body), resp.StatusCode)
	}

	var reviews []models.GitHubReview
	if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
		return nil, fmt.Errorf("failed to decode reviews: %w", err)
	}

	return reviews, nil
}
