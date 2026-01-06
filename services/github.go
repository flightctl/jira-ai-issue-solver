package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"jira-ai-issue-solver/models"
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
	// For PAT auth: forkOwner and repo are not used
	// For GitHub App auth: forkOwner and repo are required to get the installation token
	PushChanges(directory, branchName string, forkOwner, repo string) error

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

	// ReplyToPRComment replies to a specific PR comment (for line-based review comments)
	ReplyToPRComment(owner, repo string, prNumber int, commentID int64, body string) error

	// GetPRDetails gets detailed PR information including reviews, comments, and files
	GetPRDetails(owner, repo string, prNumber int) (*models.GitHubPRDetails, error)

	// ListPRReviews lists all reviews on a PR
	ListPRReviews(owner, repo string, prNumber int) ([]models.GitHubReview, error)

	// GetInstallationIDForRepo discovers the installation ID for a specific repository (GitHub App only)
	GetInstallationIDForRepo(owner, repo string) (int64, error)

	// CheckForkExistsForUser checks if a fork exists for a specific fork owner
	CheckForkExistsForUser(owner, repo, forkOwner string) (bool, error)

	// GetForkCloneURLForUser returns the clone URL for a specific user's fork
	GetForkCloneURLForUser(owner, repo, forkOwner string) (string, error)
}

// fileExists returns true if the file exists, false if it does not exist,
// and an error if the existence check failed for reasons other than the file not existing.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, err
}

const (
	// maxPaginationPages is the safety limit for API pagination
	// 100 pages * 100 items per page = 10,000 items max
	maxPaginationPages = 100
)

// GitHubServiceImpl implements the GitHubService interface
type GitHubServiceImpl struct {
	config             *models.Config
	client             *http.Client
	appTransport       *ghinstallation.AppsTransport       // For app-level operations
	installationAuth   map[int64]*ghinstallation.Transport // Per-installation auth
	installationAuthMu sync.RWMutex                        // Protects installationAuth map
	executor           models.CommandExecutor
	logger             *zap.Logger
}

// gitCommand encapsulates a git command execution with optional stdout/stderr capture.
// Buffers are allocated only when enabled, optimizing memory usage while maintaining
// clean error reporting and debug logging capabilities.
type gitCommand struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// newGitCommand creates a new gitCommand that executes the given command in the specified directory.
// Stdout and stderr are captured only when their respective flags are enabled, minimizing memory allocation.
// The returned gitCommand provides safe access to command output even when capture is disabled.
func newGitCommand(cmd *exec.Cmd, directory string, captureStdout, captureStderr bool) *gitCommand {
	f := func(used bool) *bytes.Buffer {
		if used {
			return bytes.NewBuffer(nil)
		}
		return nil
	}

	cmd.Dir = directory

	gitCmd := &gitCommand{
		cmd:    cmd,
		stdout: f(captureStdout),
		stderr: f(captureStderr),
	}

	if gitCmd.stdout != nil {
		cmd.Stdout = gitCmd.stdout
	}
	if gitCmd.stderr != nil {
		cmd.Stderr = gitCmd.stderr
	}

	return gitCmd
}

// hasStdout returns true if stdout was captured and contains data.
// Returns false if stdout capture was disabled or if no output was produced.
func (g *gitCommand) hasStdout() bool {
	return g.stdout != nil && g.stdout.Len() > 0
}

// getStdout returns the captured stdout as a string.
// Returns an empty string if stdout capture was disabled or if the buffer is nil.
// Safe to call even when stdout was not enabled.
func (g *gitCommand) getStdout() string {
	if g.stdout == nil {
		return ""
	}
	return g.stdout.String()
}

// getStderr returns the captured stderr as a string.
// Returns an empty string if stderr capture was disabled or if the buffer is nil.
// Safe to call even when stderr was not enabled.
func (g *gitCommand) getStderr() string {
	if g.stderr == nil {
		return ""
	}
	return g.stderr.String()
}

// run executes the git command and returns any error that occurred.
// Stdout and stderr are captured to their respective buffers if enabled during construction.
func (g *gitCommand) run() error {
	return g.cmd.Run()
}

// NewGitHubService creates a new GitHubService
func NewGitHubService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) GitHubService {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}

	service := &GitHubServiceImpl{
		config:           config,
		client:           &http.Client{},
		installationAuth: make(map[int64]*ghinstallation.Transport),
		executor:         commandExecutor,
		logger:           logger,
	}

	// Initialize GitHub App transport if configured
	if config.GitHub.AppID != 0 && config.GitHub.PrivateKeyPath != "" {
		// Create Apps Transport for app-level operations
		appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
			http.DefaultTransport,
			config.GitHub.AppID,
			config.GitHub.PrivateKeyPath,
		)
		if err != nil {
			logger.Fatal("Failed to create GitHub App transport", zap.Error(err))
		}
		service.appTransport = appTransport

		logger.Info("Using GitHub App authentication",
			zap.Int64("appID", config.GitHub.AppID))
	} else {
		logger.Info("Using Personal Access Token authentication")
	}

	return service
}

// CloneRepository clones a repository to a local directory
func (s *GitHubServiceImpl) CloneRepository(repoURL, directory string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "CloneRepository")

	// Check if the directory is already a git repository
	if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
		// Directory is already a git repository, fetch the latest changes
		cmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to fetch repository: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git fetch origin", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		// Reset to origin/main or origin/master to ensure we're up to date
		cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/main"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			// Try with master branch
			cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/master"), directory, debugEnabled, true)

			if err := cmd.run(); err != nil {
				return fmt.Errorf("failed to reset to origin/main or origin/master: %w, stderr: %s", err, cmd.getStderr())
			}
			s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/master"), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
		} else {
			s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/main"), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
		}

		// Clean the repository
		cmd = newGitCommand(s.executor("git", "clean", "-fdx"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to clean repository: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git clean -fdx", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	} else {
		// Clone the repository
		cmd := newGitCommand(s.executor("git", "clone", repoURL, directory), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to clone repository: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git clone", fn, zap.String("url", repoURL), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
	}

	// Configure git user for GitHub App
	cmd := newGitCommand(s.executor("git", "config", "user.name", s.config.GitHub.BotUsername), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to configure git user name: %w, stderr: %s", err, cmd.getStderr())
	}
	s.logger.Debug("git config user.name", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	cmd = newGitCommand(s.executor("git", "config", "user.email", s.config.GetBotEmail()), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to configure git user email: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git config user.email", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Configure SSH signing if a key is specified
	exists := false
	if s.config.GitHub.SSHKeyPath != "" {
		var err error

		exists, err = fileExists(s.config.GitHub.SSHKeyPath)
		if err != nil {
			return fmt.Errorf("failed to check if SSH key file exists: %w", err)
		}

		s.logger.Debug("SSH key file exists", zap.String("sshKeyPath", s.config.GitHub.SSHKeyPath), zap.Bool("exists", exists))
	}

	if exists {
		cmd = newGitCommand(s.executor("git", "config", "gpg.format", "ssh"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to configure git gpg format: %w, stderr: %s", err, cmd.getStderr())
		}
		s.logger.Debug("git config gpg.format", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		cmd = newGitCommand(s.executor("git", "config", "user.signingkey", s.config.GitHub.SSHKeyPath), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to configure git ssh signing key: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git config user.signingkey", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		cmd = newGitCommand(s.executor("git", "config", "commit.gpgsign", "true"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to enable git commit signing: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git config commit.gpgsign", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		s.logger.Info("Configured SSH signing for repository", zap.String("sshKeyPath", s.config.GitHub.SSHKeyPath))
	} else {
		s.logger.Info("SSH signing not configured for repository")
	}

	// Configure git to use the GitHub token for authentication
	// This prevents credential prompts during push operations
	cmd = newGitCommand(s.executor("git", "config", "credential.helper", "store"), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to configure git credential helper: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git config credential.helper", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Extract owner and repo from the URL first (needed for getting auth token)
	owner, repo, err := ExtractRepoInfo(repoURL)
	if err != nil {
		return fmt.Errorf("failed to extract repo info: %w", err)
	}

	// Set up the credential URL with token
	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	// Set the remote URL with embedded token
	authURL := fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo)
	// Don't log stdout/stderr for this command since it contains the token
	cmd = newGitCommand(s.executor("git", "remote", "set-url", "origin", authURL), directory, false, false)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to set remote URL with token: %w", err)
	}

	return nil
}

// getAuthTokenForRepo gets the appropriate authentication token for a repository
// For PAT: returns the configured PAT
// For GitHub App: discovers installation ID and returns installation token
func (s *GitHubServiceImpl) getAuthTokenForRepo(owner, repo string) (string, error) {
	// If using PAT, return it
	if s.config.GitHub.PersonalAccessToken != "" {
		return s.config.GitHub.PersonalAccessToken, nil
	}

	// If using GitHub App, get installation token
	if s.appTransport != nil {
		installationID, err := s.GetInstallationIDForRepo(owner, repo)
		if err != nil {
			return "", fmt.Errorf("failed to get installation ID: %w", err)
		}

		// Get installation-specific transport with double-check locking pattern
		s.installationAuthMu.RLock()
		transport, ok := s.installationAuth[installationID]
		s.installationAuthMu.RUnlock()

		if !ok {
			s.installationAuthMu.Lock()
			// Double-check pattern: another goroutine may have created it
			if transport, ok = s.installationAuth[installationID]; !ok {
				transport = ghinstallation.NewFromAppsTransport(s.appTransport, installationID)
				s.installationAuth[installationID] = transport
			}
			s.installationAuthMu.Unlock()
		}

		// Get token from transport
		token, err := transport.Token(context.Background())
		if err != nil {
			// Invalidate cached transport on error to allow retry with fresh transport
			s.installationAuthMu.Lock()
			delete(s.installationAuth, installationID)
			s.installationAuthMu.Unlock()
			return "", fmt.Errorf("failed to get installation token (cache invalidated): %w", err)
		}

		return token, nil
	}

	return "", fmt.Errorf("no authentication method configured")
}

// CreateBranch creates a new branch in a local repository based on the latest target branch
func (s *GitHubServiceImpl) CreateBranch(directory, branchName string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "CreateBranch")

	// Fetch the latest changes from origin
	cmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git fetch origin", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Checkout the target branch
	cmd = newGitCommand(s.executor("git", "checkout", s.config.GitHub.TargetBranch), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to checkout target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, cmd.getStderr())
	}

	s.logger.Debug("git checkout", fn, zap.String("branch", s.config.GitHub.TargetBranch), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Reset to the latest commit on the target branch to ensure we're up to date
	cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/"+s.config.GitHub.TargetBranch), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to reset to latest commit on target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, cmd.getStderr())
	}
	s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/"+s.config.GitHub.TargetBranch), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Check if the branch already exists locally
	cmd = newGitCommand(s.executor("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName), directory, debugEnabled, true)

	if err := cmd.run(); err == nil {
		// Branch exists locally, delete it first
		s.logger.Info("Branch already exists locally, deleting it", zap.String("branchName", branchName))
		cmd = newGitCommand(s.executor("git", "branch", "-D", branchName), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to delete existing branch %s: %w, stderr: %s", branchName, err, cmd.getStderr())
		}

		s.logger.Debug("git branch -D", fn, zap.String("branch", branchName), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
	}

	// Create a new branch from the current state
	cmd = newGitCommand(s.executor("git", "checkout", "-b", branchName), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to create branch: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git checkout", fn, zap.String("operation", "-b"), zap.String("branch", branchName), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	return nil
}

// CommitChanges commits changes to a local repository
func (s *GitHubServiceImpl) CommitChanges(directory, message string, coAuthorName, coAuthorEmail string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "CommitChanges")

	// Add all changes
	cmd := newGitCommand(s.executor("git", "add", "."), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to add changes: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git add .", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Check if there are changes to commit (stdout always enabled to read status)
	cmd = newGitCommand(s.executor("git", "status", "--porcelain"), directory, true, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to check status: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git status --porcelain", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	if !cmd.hasStdout() {
		s.logger.Info("No changes made to repository; nothing to commit to git.")
		return nil
	}

	// Build commit message with optional co-author
	commitMessage := message
	if coAuthorName != "" && coAuthorEmail != "" {
		commitMessage = fmt.Sprintf("%s\n\nCo-authored-by: %s <%s>", message, coAuthorName, coAuthorEmail)
	}

	// Commit changes (SSH signing is handled by git config)
	cmd = newGitCommand(s.executor("git", "commit", "-m", commitMessage), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to commit changes: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git commit", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	return nil
}

// PushChanges pushes changes to a remote repository
func (s *GitHubServiceImpl) PushChanges(directory, branchName string, forkOwner, repo string) error {
	// Get authentication token (supports both PAT and GitHub App)
	token, err := s.getAuthTokenForRepo(forkOwner, repo)
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	// Construct authenticated push URL
	pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, forkOwner, repo)

	// Push the changes to the authenticated URL
	// Use --force-with-lease to safely overwrite if branch exists from previous attempt
	// Don't capture stdout/stderr to prevent token leakage in logs
	cmd := newGitCommand(s.executor("git", "push", "--force-with-lease", pushURL, branchName), directory, false, false)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to push changes: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Info("Successfully pushed branch",
		zap.String("branch", branchName),
		zap.String("fork", fmt.Sprintf("%s/%s", forkOwner, repo)))

	return nil
}

// CreatePullRequest creates a pull request
func (s *GitHubServiceImpl) CreatePullRequest(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo)

	// Set maintainer_can_modify to false explicitly
	// This is required when using GitHub App tokens to create PRs from forks
	// See: https://github.com/orgs/community/discussions/39178
	falseValue := false
	payload := models.GitHubCreatePRRequest{
		Title:               title,
		Body:                body,
		Head:                head,
		Base:                base,
		Labels:              []string{s.config.GitHub.PRLabel},
		MaintainerCanModify: &falseValue,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Use installation-specific client if using GitHub App
	// PRs are created on the base repository, so use the base repo's installation
	var client *http.Client
	if s.appTransport != nil {
		// Get installation ID for the base repository (where the PR will be created)
		installationID, err := s.GetInstallationIDForRepo(owner, repo)
		if err != nil {
			return nil, fmt.Errorf("failed to get installation ID for %s/%s: %w", owner, repo, err)
		}
		client, err = s.getInstallationClient(installationID)
		if err != nil {
			return nil, fmt.Errorf("failed to get installation client: %w", err)
		}

		s.logger.Info("Creating PR using base repository's installation",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int64("installationID", installationID))

		// Installation client's transport automatically adds Authorization header
		// Do NOT set it manually here
	} else {
		// PAT mode - set Authorization header manually
		token, err := s.getAuthTokenForRepo(owner, repo)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
		client = s.client
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "CreatePullRequest"))
		}
	}()

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
	token, err := s.getAuthTokenForRepo(owner, repo)
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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "CheckForkExists"))
		}
	}()

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
	if err := os.MkdirAll(directory, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if the directory is already a git repository
	if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
		debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
		fn := zap.String("function", "ResetFork")

		// Directory is already a git repository, fetch and reset
		// Fetch the upstream repository
		cmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, cmd.getStderr())
		}
		s.logger.Debug("git fetch origin", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		// Reset to origin/main or origin/master
		cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/main"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			// Try with master branch
			cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/master"), directory, debugEnabled, true)

			if err := cmd.run(); err != nil {
				return fmt.Errorf("failed to reset to origin/main or origin/master: %w, stderr: %s", err, cmd.getStderr())
			}

			s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/master"), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
		} else {
			s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/main"), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))
		}

		// Clean the repository
		cmd = newGitCommand(s.executor("git", "clean", "-fdx"), directory, debugEnabled, true)

		if err := cmd.run(); err != nil {
			return fmt.Errorf("failed to clean repository: %w, stderr: %s", err, cmd.getStderr())
		}

		s.logger.Debug("git clean -fdx", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

		return nil
	}

	s.logger.Debug("Directory is not a git repository, cloning repository", zap.String("forkCloneURL", forkCloneURL), zap.String("directory", directory))

	// Clone the repository
	return s.CloneRepository(forkCloneURL, directory)
}

// ForkRepository forks a repository and returns the clone URL of the fork
func (s *GitHubServiceImpl) ForkRepository(owner, repo string) (string, error) {
	// Get authentication token
	token, err := s.getAuthTokenForRepo(owner, repo)
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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "ForkRepository"))
		}
	}()

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
	token, err := s.getAuthTokenForRepo(owner, repo)
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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "SyncForkWithUpstream-GetForkDetails"))
		}
	}()

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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "SyncForkWithUpstream-MergeUpstream"))
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to sync fork: %s, status code: %d", string(body), resp.StatusCode)
	}

	return nil
}

// SwitchToTargetBranch switches to the configured target branch after cloning
func (s *GitHubServiceImpl) SwitchToTargetBranch(directory string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "SwitchToTargetBranch")

	// Fetch the latest changes from origin
	cmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git fetch origin", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Checkout the target branch
	cmd = newGitCommand(s.executor("git", "checkout", s.config.GitHub.TargetBranch), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to checkout target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, cmd.getStderr())
	}

	// Reset to the latest commit on the target branch to ensure we're up to date
	cmd = newGitCommand(s.executor("git", "reset", "--hard", "origin/"+s.config.GitHub.TargetBranch), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to reset to latest commit on target branch %s: %w, stderr: %s", s.config.GitHub.TargetBranch, err, cmd.getStderr())
	}

	s.logger.Debug("git reset --hard", fn, zap.String("ref", "origin/"+s.config.GitHub.TargetBranch), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	return nil
}

// SwitchToBranch switches to a specific branch
func (s *GitHubServiceImpl) SwitchToBranch(directory, branchName string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "SwitchToBranch")

	// Fetch the latest changes from origin
	cmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to fetch origin: %w, stderr: %s", err, cmd.getStderr())
	}
	s.logger.Debug("git fetch origin", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// Checkout the specified branch
	cmd = newGitCommand(s.executor("git", "checkout", branchName), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w, stderr: %s", branchName, err, cmd.getStderr())
	}

	s.logger.Debug("git checkout", fn, zap.String("branch", branchName), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	return nil
}

// PullChanges pulls the latest changes from the remote branch
func (s *GitHubServiceImpl) PullChanges(directory, branchName string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "PullChanges")

	// Pull the latest changes from the remote branch
	cmd := newGitCommand(s.executor("git", "pull", "origin", branchName), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		return fmt.Errorf("failed to pull changes from origin/%s: %w, stderr: %s", branchName, err, cmd.getStderr())
	}

	s.logger.Debug("git pull", fn, zap.String("remote", "origin"), zap.String("branch", branchName), zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

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

	token, err := s.getAuthTokenForRepo(owner, repo)
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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "AddPRComment"))
		}
	}()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add PR comment: %s, status: %d", string(body), resp.StatusCode)
	}

	return nil
}

// ReplyToPRComment replies to a specific PR review comment
// For line-based review comments, this creates a threaded reply
func (s *GitHubServiceImpl) ReplyToPRComment(owner, repo string, prNumber int, commentID int64, body string) error {
	commentRequest := struct {
		Body string `json:"body"`
	}{Body: body}

	jsonPayload, err := json.Marshal(commentRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal comment request: %w", err)
	}

	// Use the pulls comments endpoint for threaded replies
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/comments/%d/replies", owner, repo, prNumber, commentID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthTokenForRepo(owner, repo)
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
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "ReplyToPRComment"))
		}
	}()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to reply to PR comment: %s, status: %d", string(body), resp.StatusCode)
	}

	s.logger.Debug("Replied to PR comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int64("comment_id", commentID))

	return nil
}

// fetchPRReviewCommentsPage fetches a single page of PR review comments
func (s *GitHubServiceImpl) fetchPRReviewCommentsPage(owner, repo string, prNumber, page, perPage int) ([]models.GitHubPRComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/comments?per_page=%d&page=%d", owner, repo, prNumber, perPage, page)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "fetchPRReviewCommentsPage"))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PR review comments: %s, status: %d", string(body), resp.StatusCode)
	}

	var comments []models.GitHubPRComment
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil, fmt.Errorf("failed to decode review comments: %w", err)
	}

	return comments, nil
}

// listPRReviewComments lists line-based review comments on a PR (from pulls endpoint)
// Handles pagination to retrieve all comments
func (s *GitHubServiceImpl) listPRReviewComments(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
	var allComments []models.GitHubPRComment
	page := 1
	perPage := 100

	for {
		comments, err := s.fetchPRReviewCommentsPage(owner, repo, prNumber, page, perPage)
		if err != nil {
			return nil, err
		}

		allComments = append(allComments, comments...)

		// If we got fewer than perPage results, we've reached the last page
		// Also break on empty response to avoid infinite loop edge case
		if len(comments) == 0 || len(comments) < perPage {
			break
		}

		page++
		// Safety limit: prevent infinite loop if GitHub API misbehaves
		if page > maxPaginationPages {
			s.logger.Warn("Hit pagination safety limit for PR review comments",
				zap.Int("page", page),
				zap.Int("comments_retrieved", len(allComments)))
			break
		}
	}

	return allComments, nil
}

// fetchPRConversationCommentsPage fetches a single page of PR conversation comments
func (s *GitHubServiceImpl) fetchPRConversationCommentsPage(owner, repo string, prNumber, page, perPage int) ([]models.GitHubPRComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments?per_page=%d&page=%d", owner, repo, prNumber, perPage, page)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "fetchPRConversationCommentsPage"))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PR conversation comments: %s, status: %d", string(body), resp.StatusCode)
	}

	var comments []models.GitHubPRComment
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return nil, fmt.Errorf("failed to decode conversation comments: %w", err)
	}

	return comments, nil
}

// listPRConversationComments lists general conversation comments on a PR (from issues endpoint)
// Handles pagination to retrieve all comments
func (s *GitHubServiceImpl) listPRConversationComments(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
	var allComments []models.GitHubPRComment
	page := 1
	perPage := 100

	for {
		comments, err := s.fetchPRConversationCommentsPage(owner, repo, prNumber, page, perPage)
		if err != nil {
			return nil, err
		}

		allComments = append(allComments, comments...)

		// If we got fewer than perPage results, we've reached the last page
		// Also break on empty response to avoid infinite loop edge case
		if len(comments) == 0 || len(comments) < perPage {
			break
		}

		page++
		// Safety limit: prevent infinite loop if GitHub API misbehaves
		if page > maxPaginationPages {
			s.logger.Warn("Hit pagination safety limit for PR conversation comments",
				zap.Int("page", page),
				zap.Int("comments_retrieved", len(allComments)))
			break
		}
	}

	return allComments, nil
}

// ListPRComments lists all PR comments (both line-based review comments and general conversation comments)
// Note: The /pulls/{pr}/comments and /issues/{pr}/comments endpoints return disjoint sets per GitHub API spec.
// Line-based review comments only appear in pulls endpoint; general conversation comments only in issues endpoint.
func (s *GitHubServiceImpl) ListPRComments(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
	// Get line-based review comments from pulls endpoint
	reviewComments, err := s.listPRReviewComments(owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get review comments: %w", err)
	}

	// Get general conversation comments from issues endpoint
	conversationComments, err := s.listPRConversationComments(owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation comments: %w", err)
	}

	// Merge both types of comments
	allComments := append(reviewComments, conversationComments...)

	s.logger.Debug("Retrieved PR comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int("review_comments", len(reviewComments)),
		zap.Int("conversation_comments", len(conversationComments)),
		zap.Int("total_comments", len(allComments)))

	return allComments, nil
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

	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "GetPRDetails"))
		}
	}()

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

	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "ListPRReviews"))
		}
	}()

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

// GetInstallationIDForRepo discovers the GitHub App installation ID for a specific repository.
// This is used in GitHub App authentication mode to obtain installation-specific tokens.
//
// Parameters:
//   - owner: The repository owner (user or organization)
//   - repo: The repository name
//
// Returns:
//   - The installation ID if the GitHub App is installed on the repository
//   - An error if the app is not configured, not installed, or if the API request fails
//
// Example usage:
//
//	installationID, err := githubService.GetInstallationIDForRepo("myorg", "myrepo")
//	if err != nil {
//	    // Handle error - app may not be installed on this repo
//	}
func (s *GitHubServiceImpl) GetInstallationIDForRepo(owner, repo string) (int64, error) {
	if s.appTransport == nil {
		return 0, fmt.Errorf("GitHub App not configured")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/installation", owner, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Use app transport for this request
	client := &http.Client{Transport: s.appTransport}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to get installation: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "GetInstallationIDForRepo"))
		}
	}()

	if resp.StatusCode == 404 {
		return 0, fmt.Errorf("GitHub App is not installed on %s/%s", owner, repo)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to get installation (status %d): %s", resp.StatusCode, string(body))
	}

	var installation struct {
		ID int64 `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&installation); err != nil {
		return 0, fmt.Errorf("failed to decode installation response: %w", err)
	}

	s.logger.Info("Discovered installation ID",
		zap.String("repo", fmt.Sprintf("%s/%s", owner, repo)),
		zap.Int64("installationID", installation.ID))

	return installation.ID, nil
}

// getInstallationClient returns an HTTP client authenticated for a specific installation
func (s *GitHubServiceImpl) getInstallationClient(installationID int64) (*http.Client, error) {
	if s.appTransport == nil {
		// Fallback to PAT
		return &http.Client{}, nil
	}

	// Check if we already have a transport for this installation with double-check locking
	s.installationAuthMu.RLock()
	transport, ok := s.installationAuth[installationID]
	s.installationAuthMu.RUnlock()

	if ok {
		return &http.Client{Transport: transport}, nil
	}

	// Create new installation transport
	s.installationAuthMu.Lock()
	// Double-check pattern: another goroutine may have created it
	if transport, ok = s.installationAuth[installationID]; !ok {
		transport = ghinstallation.NewFromAppsTransport(s.appTransport, installationID)
		s.installationAuth[installationID] = transport
		s.logger.Debug("Created new installation transport", zap.Int64("installationID", installationID))
	}
	s.installationAuthMu.Unlock()

	return &http.Client{Transport: transport}, nil
}

// CheckForkExistsForUser checks if a specific user has forked a repository.
// This is used in GitHub App mode to verify that the assignee has created their own fork
// before attempting to clone and push changes.
//
// Parameters:
//   - owner: The original repository owner
//   - repo: The original repository name
//   - forkOwner: The username to check for a fork (e.g., the assignee's GitHub username)
//
// Returns:
//   - true if forkOwner has a valid fork of owner/repo
//   - false if no fork exists or if the repository exists but is not a fork of the expected parent
//   - An error if the API request fails or if the repository exists but is not a fork
//
// Example usage:
//
//	exists, err := githubService.CheckForkExistsForUser("upstream-org", "myrepo", "developer123")
//	if err != nil {
//	    return fmt.Errorf("failed to check fork: %w", err)
//	}
//	if !exists {
//	    return fmt.Errorf("developer must fork the repository first")
//	}
func (s *GitHubServiceImpl) CheckForkExistsForUser(owner, repo, forkOwner string) (bool, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", forkOwner, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	// For discovery, use unauthenticated requests (works for public repos)
	// GitHub App JWT tokens can't access repository data, only app-level operations
	// If the repo is private, this will return 404, which is fine - it means fork doesn't exist
	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to check fork: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "CheckForkExistsForUser"))
		}
	}()

	if resp.StatusCode == 404 {
		return false, nil
	}

	if resp.StatusCode == 200 {
		// Verify it's actually a fork of the expected repo
		var repoInfo struct {
			Fork   bool `json:"fork"`
			Parent struct {
				FullName string `json:"full_name"`
			} `json:"parent"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&repoInfo); err != nil {
			return false, fmt.Errorf("failed to decode repository info: %w", err)
		}

		expectedParent := fmt.Sprintf("%s/%s", owner, repo)
		if !repoInfo.Fork || repoInfo.Parent.FullName != expectedParent {
			return false, fmt.Errorf("%s/%s exists but is not a fork of %s", forkOwner, repo, expectedParent)
		}

		return true, nil
	}

	body, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("unexpected response (status %d): %s", resp.StatusCode, string(body))
}

// GetForkCloneURLForUser returns the HTTPS clone URL for a specific user's fork.
// This verifies the fork exists and returns the URL that can be used to clone it.
//
// Parameters:
//   - owner: The original repository owner
//   - repo: The original repository name
//   - forkOwner: The username whose fork URL to retrieve
//
// Returns:
//   - The HTTPS clone URL (e.g., "https://github.com/forkOwner/repo.git")
//   - An error if the fork doesn't exist or verification fails
//
// Example usage:
//
//	cloneURL, err := githubService.GetForkCloneURLForUser("upstream-org", "myrepo", "developer123")
//	if err != nil {
//	    return fmt.Errorf("fork not available: %w", err)
//	}
//	// Use cloneURL to clone the fork
func (s *GitHubServiceImpl) GetForkCloneURLForUser(owner, repo, forkOwner string) (string, error) {
	exists, err := s.CheckForkExistsForUser(owner, repo, forkOwner)
	if err != nil {
		return "", err
	}

	if !exists {
		return "", fmt.Errorf("fork %s/%s does not exist (developer must fork the repository first)", forkOwner, repo)
	}

	return fmt.Sprintf("https://github.com/%s/%s.git", forkOwner, repo), nil
}
