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
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v75/github"
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

	// HasChanges checks if there are any uncommitted changes in the repository
	HasChanges(directory string) (bool, error)

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

	// CommitChangesViaAPI creates a commit using the GitHub API (for verified GitHub App commits)
	// Returns the commit SHA
	CommitChangesViaAPI(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string) (string, error)

	// CreateVerifiedCommitFromLocal creates a verified commit via GitHub API from local repository state
	// Handles both working tree changes and local commits (including merge commits with multiple parents)
	// Returns empty string if no changes, otherwise returns the commit SHA
	CreateVerifiedCommitFromLocal(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string) (string, error)
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

	// maxMergeCommitFiles is the safety limit for files in a single merge commit
	// Prevents DoS and OOM when processing commits with thousands of files
	maxMergeCommitFiles = 1000
)

// GitHubServiceImpl implements the GitHubService interface
type GitHubServiceImpl struct {
	config               *models.Config
	client               *http.Client
	appTransport         *ghinstallation.AppsTransport       // For app-level operations
	installationAuth     map[int64]*ghinstallation.Transport // Per-installation auth
	installationAuthMu   sync.RWMutex                        // Protects installationAuth map
	appClient            *github.Client                      // go-github client for app-level operations
	installationClients  map[int64]*github.Client            // Per-installation go-github clients
	installationClientMu sync.RWMutex                        // Protects installationClients map
	executor             models.CommandExecutor
	logger               *zap.Logger
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
		config:              config,
		client:              &http.Client{},
		installationAuth:    make(map[int64]*ghinstallation.Transport),
		installationClients: make(map[int64]*github.Client),
		executor:            commandExecutor,
		logger:              logger,
	}

	// Initialize GitHub App transport
	appTransport, err := ghinstallation.NewAppsTransportKeyFromFile(
		http.DefaultTransport,
		config.GitHub.AppID,
		config.GitHub.PrivateKeyPath,
	)
	if err != nil {
		logger.Fatal("Failed to create GitHub App transport", zap.Error(err))
	}
	service.appTransport = appTransport

	// Create go-github client for app-level operations
	service.appClient = github.NewClient(&http.Client{Transport: appTransport})

	logger.Info("Using GitHub App authentication",
		zap.Int64("appID", config.GitHub.AppID))

	return service
}

// getInstallationGitHubClient returns a go-github client authenticated for a specific installation
// Uses double-checked locking pattern for thread-safe lazy initialization of per-installation clients
func (s *GitHubServiceImpl) getInstallationGitHubClient(installationID int64) (*github.Client, error) {
	// Fast path: check if client already exists with read lock
	s.installationClientMu.RLock()
	client, exists := s.installationClients[installationID]
	s.installationClientMu.RUnlock()

	if exists {
		return client, nil
	}

	// Slow path: create new client with write lock
	s.installationClientMu.Lock()
	defer s.installationClientMu.Unlock()

	// Double-check: another goroutine might have created the client while we waited
	if existingClient, found := s.installationClients[installationID]; found {
		return existingClient, nil
	}

	// Get or create the installation transport
	s.installationAuthMu.RLock()
	transport, exists := s.installationAuth[installationID]
	s.installationAuthMu.RUnlock()

	if !exists {
		// Create new installation transport
		s.installationAuthMu.Lock()
		// Double-check pattern for transport as well
		if transport, exists = s.installationAuth[installationID]; !exists {
			tr := ghinstallation.NewFromAppsTransport(s.appTransport, installationID)
			s.installationAuth[installationID] = tr
			transport = tr
		}
		s.installationAuthMu.Unlock()
	}

	// Create and cache the go-github client
	client = github.NewClient(&http.Client{Transport: transport})
	s.installationClients[installationID] = client

	s.logger.Debug("Created new go-github client for installation",
		zap.Int64("installationID", installationID))

	return client, nil
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

// getAuthTokenForRepo gets the GitHub App installation token for a repository
func (s *GitHubServiceImpl) getAuthTokenForRepo(owner, repo string) (string, error) {
	if s.appTransport == nil {
		return "", fmt.Errorf("GitHub App authentication not configured")
	}

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

	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "PushChanges")

	// Before pushing, fetch the latest remote state to avoid "stale info" errors
	// This updates our local tracking branches to match the current remote state
	// Then --force-with-lease will protect against overwriting human changes
	// We fetch all branches (not just the target branch) to update tracking refs properly
	fetchCmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)
	if err := fetchCmd.run(); err != nil {
		// Fetch might fail, but we can still try to push
		// Log the warning but continue - the push will handle it
		s.logger.Debug("Failed to fetch before push",
			fn,
			zap.String("stderr", fetchCmd.getStderr()),
			zap.Error(err))
	} else {
		s.logger.Debug("git fetch origin",
			fn,
			zap.String("stdout", fetchCmd.getStdout()),
			zap.String("stderr", fetchCmd.getStderr()))
	}

	// Push the changes to the authenticated URL
	// Use --force-with-lease to safely overwrite if branch exists from previous attempt
	// Capture stderr to diagnose push failures, but we'll sanitize it before logging
	cmd := newGitCommand(s.executor("git", "push", "--force-with-lease", pushURL, branchName), directory, debugEnabled, true)

	if err := cmd.run(); err != nil {
		// Sanitize stderr to remove token before logging
		sanitizedStderr := strings.ReplaceAll(cmd.getStderr(), token, "***TOKEN***")
		return fmt.Errorf("failed to push changes: %w, stderr: %s", err, sanitizedStderr)
	}

	s.logger.Info("Successfully pushed branch",
		zap.String("branch", branchName),
		zap.String("fork", fmt.Sprintf("%s/%s", forkOwner, repo)))

	return nil
}

// CommitChangesViaAPI creates a commit using the GitHub API
// This creates verified commits when using GitHub App authentication
// Returns the SHA of the created commit
func (s *GitHubServiceImpl) CommitChangesViaAPI(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string) (string, error) {
	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return "", fmt.Errorf("failed to get auth token: %w", err)
	}

	// Get the current commit SHA for the branch (or target branch if new branch doesn't exist)
	baseSHA, branchExists, err := s.getBranchBaseCommit(owner, repo, branchName, token)
	if err != nil {
		return "", fmt.Errorf("failed to get base commit: %w", err)
	}

	s.logger.Debug("Got base commit SHA",
		zap.String("sha", baseSHA),
		zap.Bool("branchExists", branchExists),
		zap.String("branch", branchName))

	// Get the base tree SHA from the commit
	baseTreeSHA, err := s.getTreeSHAFromCommit(owner, repo, baseSHA, token)
	if err != nil {
		return "", fmt.Errorf("failed to get base tree: %w", err)
	}

	// Create blobs for all changed files and build tree entries
	treeEntries, err := s.createBlobsForChangedFiles(owner, repo, directory, token)
	if err != nil {
		return "", fmt.Errorf("failed to create blobs: %w", err)
	}

	if len(treeEntries) == 0 {
		s.logger.Info("No changes made to repository; nothing to commit")
		return baseSHA, nil
	}

	// Create a new tree
	treeSHA, err := s.createTree(owner, repo, baseTreeSHA, treeEntries, token)
	if err != nil {
		return "", fmt.Errorf("failed to create tree: %w", err)
	}

	// Build commit message with optional co-author
	commitMessage := message
	if coAuthorName != "" && coAuthorEmail != "" {
		commitMessage = fmt.Sprintf("%s\n\nCo-authored-by: %s <%s>", message, coAuthorName, coAuthorEmail)
	}

	// Create the commit
	commitSHA, err := s.createCommit(owner, repo, commitMessage, treeSHA, baseSHA, token)
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	// Create or update the branch reference to point to the new commit
	if branchExists {
		// Branch exists, update the reference
		if err := s.updateReference(owner, repo, branchName, commitSHA, token); err != nil {
			return "", fmt.Errorf("failed to update reference: %w", err)
		}
	} else {
		// Branch doesn't exist, create new reference
		if err := s.createReference(owner, repo, branchName, commitSHA, token); err != nil {
			return "", fmt.Errorf("failed to create reference: %w", err)
		}
	}

	s.logger.Info("Successfully created commit via API",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("branch", branchName),
		zap.String("commit_sha", commitSHA),
		zap.Bool("newBranch", !branchExists))

	return commitSHA, nil
}

// CreateVerifiedCommitFromLocal creates a verified commit via GitHub API from local repository state
// Handles both working tree changes and local commits (including merge commits with multiple parents)
// Returns empty string if no changes, otherwise returns the commit SHA
func (s *GitHubServiceImpl) CreateVerifiedCommitFromLocal(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string) (string, error) {
	// Check what kind of changes we have
	hasChanges, err := s.HasChanges(directory)
	if err != nil {
		return "", fmt.Errorf("failed to check for changes: %w", err)
	}

	if !hasChanges {
		s.logger.Info("No changes to commit")
		return "", nil
	}

	// Check if there are unpushed local commits (created by AI)
	hasLocalCommits, err := s.hasUnpushedCommits(directory, zap.String("function", "CreateVerifiedCommitFromLocal"))
	if err != nil {
		return "", fmt.Errorf("failed to check unpushed commits: %w", err)
	}

	if hasLocalCommits {
		// Local commits exist - create verified commit from local HEAD
		// This preserves merge commit structure (two parents)
		return s.createVerifiedCommitFromLocalHEAD(owner, repo, branchName, message, directory, coAuthorName, coAuthorEmail)
	}

	// Only working tree changes exist - use existing API commit logic
	return s.CommitChangesViaAPI(owner, repo, branchName, message, directory, coAuthorName, coAuthorEmail)
}

// createVerifiedCommitFromLocalHEAD creates a verified commit via API from local HEAD commit
// Preserves merge commit structure if HEAD is a merge commit
func (s *GitHubServiceImpl) createVerifiedCommitFromLocalHEAD(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string) (string, error) {
	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return "", fmt.Errorf("failed to get auth token: %w", err)
	}

	// Get parent commit SHAs from local HEAD (could be 1 or 2 for merge commits)
	parentSHAs, err := s.getLocalParentSHAs(directory)
	if err != nil {
		return "", fmt.Errorf("failed to get parent SHAs from local HEAD: %w", err)
	}

	s.logger.Debug("Creating verified commit from local HEAD",
		zap.Strings("parents", parentSHAs),
		zap.Int("parent_count", len(parentSHAs)))

	// Use first parent as base tree (the branch we're merging into)
	firstParent := parentSHAs[0]

	// Get the base tree from the first parent on GitHub
	baseTreeSHA, err := s.getTreeSHAFromCommit(owner, repo, firstParent, token)
	if err != nil {
		return "", fmt.Errorf("failed to get base tree from first parent: %w", err)
	}

	// Create blobs for files that changed from the first parent
	// For merge commits, this includes files that differ from the first parent
	treeEntries, err := s.createBlobsForFilesChangedFromParent(owner, repo, directory, firstParent, token)
	if err != nil {
		return "", fmt.Errorf("failed to create blobs: %w", err)
	}

	if len(treeEntries) == 0 {
		s.logger.Info("No changes from first parent; nothing to commit")
		return "", nil
	}

	// Create a new tree on GitHub
	treeSHA, err := s.createTree(owner, repo, baseTreeSHA, treeEntries, token)
	if err != nil {
		return "", fmt.Errorf("failed to create tree: %w", err)
	}

	s.logger.Debug("Created tree on GitHub",
		zap.String("tree", treeSHA),
		zap.String("baseTree", baseTreeSHA),
		zap.Int("entries", len(treeEntries)))

	// Build commit message with optional co-author
	commitMessage := message
	if coAuthorName != "" && coAuthorEmail != "" {
		commitMessage = fmt.Sprintf("%s\n\nCo-authored-by: %s <%s>", message, coAuthorName, coAuthorEmail)
	}

	// Create the commit with the tree and all parents from local HEAD
	commitSHA, err := s.createCommitWithParents(owner, repo, commitMessage, treeSHA, parentSHAs, token)
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}

	// Update the branch reference to point to the new commit
	// Branch must exist since we have local commits on it
	if err := s.updateReference(owner, repo, branchName, commitSHA, token); err != nil {
		return "", fmt.Errorf("failed to update reference: %w", err)
	}

	s.logger.Info("Successfully created verified commit from local HEAD",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("branch", branchName),
		zap.String("commit_sha", commitSHA),
		zap.Bool("isMergeCommit", len(parentSHAs) > 1))

	return commitSHA, nil
}

// getLocalParentSHAs gets the parent commit SHAs from local HEAD
// Returns 1 parent for normal commits, 2 parents for merge commits.
// Octopus merges (3+ parents) are not supported and will return an error.
func (s *GitHubServiceImpl) getLocalParentSHAs(directory string) ([]string, error) {
	// Get parent SHAs (one per line)
	cmd := newGitCommand(s.executor("git", "rev-parse", "HEAD^@"), directory, true, true)
	if err := cmd.run(); err != nil {
		return nil, fmt.Errorf("failed to get parent SHAs: %w, stderr: %s", err, cmd.getStderr())
	}

	output := strings.TrimSpace(cmd.getStdout())
	if output == "" {
		return nil, fmt.Errorf("no parent commits found")
	}

	// Split by newlines to get individual parent SHAs
	parents := []string{}
	for _, line := range strings.Split(output, "\n") {
		parent := strings.TrimSpace(line)
		if parent != "" {
			parents = append(parents, parent)
		}
	}

	// Validate parent count
	parentCount := len(parents)
	if parentCount == 0 {
		return nil, fmt.Errorf("failed to parse parent SHAs")
	}
	if parentCount > 2 {
		return nil, fmt.Errorf("octopus merges (3+ parents) are not supported - found %d parents", parentCount)
	}

	return parents, nil
}

// createCommitWithParents creates a commit via GitHub API with specific parents (supports merge commits)
func (s *GitHubServiceImpl) createCommitWithParents(owner, repo, message, treeSHA string, parentSHAs []string, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/commits", owner, repo)

	commitRequest := struct {
		Message string   `json:"message"`
		Tree    string   `json:"tree"`
		Parents []string `json:"parents"`
	}{
		Message: message,
		Tree:    treeSHA,
		Parents: parentSHAs,
	}

	jsonPayload, err := json.Marshal(commitRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal commit request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "createCommitWithParents"))
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create commit: %s, status: %d", string(body), resp.StatusCode)
	}

	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		return "", fmt.Errorf("failed to decode commit response: %w", err)
	}

	return commit.SHA, nil
}

// getTreeSHAFromCommit gets the tree SHA from a commit
func (s *GitHubServiceImpl) getTreeSHAFromCommit(owner, repo, commitSHA, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/commits/%s", owner, repo, commitSHA)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get commit: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "getTreeSHAFromCommit"))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get commit: %s, status: %d", string(body), resp.StatusCode)
	}

	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		return "", fmt.Errorf("failed to decode commit response: %w", err)
	}

	return commit.Tree.SHA, nil
}

// createBlobsForChangedFiles creates blobs for all changed files in the directory
func (s *GitHubServiceImpl) createBlobsForChangedFiles(owner, repo, directory, token string) ([]models.GitHubTreeEntry, error) {
	// Use git to get list of changed files
	cmd := newGitCommand(s.executor("git", "status", "--porcelain"), directory, true, true)
	if err := cmd.run(); err != nil {
		return nil, fmt.Errorf("failed to get status: %w, stderr: %s", err, cmd.getStderr())
	}

	if !cmd.hasStdout() {
		// No changes
		return []models.GitHubTreeEntry{}, nil
	}

	var treeEntries []models.GitHubTreeEntry
	// Don't trim the entire output as it would remove leading spaces from first line
	// Git status --porcelain format requires the leading space to be preserved
	lines := strings.Split(cmd.getStdout(), "\n")

	for _, line := range lines {
		// Skip empty lines (will occur at end due to trailing newline)
		if len(line) < 3 {
			continue
		}

		// Parse status line: "XY filename" where X and Y are status codes
		// Git status --porcelain format: positions 0-1 are status, position 2 is space, 3+ is filename
		// Example: " M api/file.go" where position 0=' ', 1='M', 2=' ', 3+='api/file.go'
		status := line[0:2]
		rawFilename := line[3:]

		// Handle renamed files: "R  old -> new" - we want the new filename
		filename := rawFilename
		if strings.Contains(rawFilename, " -> ") {
			parts := strings.Split(rawFilename, " -> ")
			if len(parts) == 2 {
				filename = parts[1]
				s.logger.Debug("Detected renamed file",
					zap.String("oldPath", parts[0]),
					zap.String("newPath", parts[1]))
			}
		}

		// Handle quoted filenames - git quotes filenames with special characters
		// Format: "path/to/file"
		if strings.HasPrefix(filename, "\"") && strings.HasSuffix(filename, "\"") {
			// Remove surrounding quotes
			filename = filename[1 : len(filename)-1]
			// Unescape special characters
			filename = strings.ReplaceAll(filename, "\\t", "\t")
			filename = strings.ReplaceAll(filename, "\\n", "\n")
			filename = strings.ReplaceAll(filename, "\\\\", "\\")
			filename = strings.ReplaceAll(filename, "\\\"", "\"")
		}

		filename = strings.TrimSpace(filename)

		// Skip deleted files (D status)
		if strings.Contains(status, "D") {
			s.logger.Debug("Skipping deleted file", zap.String("file", filename))
			continue
		}

		// Log the file being processed for debugging
		s.logger.Debug("Processing file for blob creation",
			zap.String("status", status),
			zap.String("filename", filename),
			zap.String("rawLine", line))

		// Read file content
		filePath := filepath.Join(directory, filename)
		// #nosec G304 - filename comes from git status output in controlled repo directory
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
		}

		// Create blob
		blobSHA, err := s.createBlob(owner, repo, string(content), token)
		if err != nil {
			return nil, fmt.Errorf("failed to create blob for %s: %w", filename, err)
		}

		// Determine file mode (check if executable)
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to stat file %s: %w", filename, err)
		}

		mode := "100644" // Regular file
		if fileInfo.Mode()&0111 != 0 {
			mode = "100755" // Executable
		}

		treeEntries = append(treeEntries, models.GitHubTreeEntry{
			Path: filename,
			Mode: mode,
			Type: "blob",
			SHA:  &blobSHA,
		})

		s.logger.Debug("Created blob for file",
			zap.String("file", filename),
			zap.String("sha", blobSHA))

		// Add a small delay between blob creations to avoid rate limiting
		// GitHub's secondary rate limit is triggered by rapid API calls
		time.Sleep(100 * time.Millisecond)
	}

	return treeEntries, nil
}

// createBlobsForFilesChangedFromParent creates tree entries for all changes from a specific parent commit
// Handles additions, modifications, deletions, and renames properly using git diff-tree
func (s *GitHubServiceImpl) createBlobsForFilesChangedFromParent(owner, repo, directory, parentSHA, token string) ([]models.GitHubTreeEntry, error) {
	// Use git diff-tree with -r (recursive), --name-status (show status), -M (detect renames)
	// This shows the exact operation for each file: A (add), M (modify), D (delete), R (rename)
	cmd := newGitCommand(s.executor("git", "diff-tree", "-r", "--name-status", "-M", parentSHA, "HEAD"), directory, true, true)
	if err := cmd.run(); err != nil {
		return nil, fmt.Errorf("failed to get diff-tree from parent: %w, stderr: %s", err, cmd.getStderr())
	}

	if !cmd.hasStdout() {
		// No changes from parent
		return []models.GitHubTreeEntry{}, nil
	}

	var treeEntries []models.GitHubTreeEntry
	lines := strings.Split(strings.TrimSpace(cmd.getStdout()), "\n")

	// Check file count limit to prevent DoS and OOM
	if len(lines) > maxMergeCommitFiles {
		return nil, fmt.Errorf("merge commit has %d files, exceeds limit of %d - refusing to process to prevent resource exhaustion", len(lines), maxMergeCommitFiles)
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse the diff-tree output format:
		// For A/M/D: <status>\t<file>
		// For R: R<similarity>\t<old-file>\t<new-file>
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}

		status := parts[0]

		s.logger.Debug("Processing file change from parent",
			zap.String("status", status),
			zap.String("line", line),
			zap.String("parent", parentSHA))

		switch {
		case status == "A" || status == "M":
			// Added or Modified file - create blob and add to tree
			filename := parts[1]
			entry, err := s.createTreeEntryForFile(owner, repo, directory, filename, token)
			if err != nil {
				return nil, fmt.Errorf("failed to create tree entry for %s: %w", filename, err)
			}
			treeEntries = append(treeEntries, entry)

		case status == "D":
			// Deleted file - add entry with nil SHA to remove from tree
			filename := parts[1]
			s.logger.Debug("File deleted, adding deletion entry",
				zap.String("file", filename))
			treeEntries = append(treeEntries, models.GitHubTreeEntry{
				Path: filename,
				SHA:  nil, // nil SHA tells GitHub to delete this file
			})

		case strings.HasPrefix(status, "R"):
			// Renamed file - delete old path and add new path
			if len(parts) < 3 {
				s.logger.Warn("Invalid rename entry, skipping", zap.String("line", line))
				continue
			}
			oldPath := parts[1]
			newPath := parts[2]

			s.logger.Debug("File renamed",
				zap.String("old_path", oldPath),
				zap.String("new_path", newPath))

			// Delete old path
			treeEntries = append(treeEntries, models.GitHubTreeEntry{
				Path: oldPath,
				SHA:  nil,
			})

			// Add new path
			entry, err := s.createTreeEntryForFile(owner, repo, directory, newPath, token)
			if err != nil {
				return nil, fmt.Errorf("failed to create tree entry for renamed file %s: %w", newPath, err)
			}
			treeEntries = append(treeEntries, entry)

		case strings.HasPrefix(status, "C"):
			// Copied file - just add the new copy
			if len(parts) < 3 {
				s.logger.Warn("Invalid copy entry, skipping", zap.String("line", line))
				continue
			}
			newPath := parts[2]
			entry, err := s.createTreeEntryForFile(owner, repo, directory, newPath, token)
			if err != nil {
				return nil, fmt.Errorf("failed to create tree entry for copied file %s: %w", newPath, err)
			}
			treeEntries = append(treeEntries, entry)

		default:
			s.logger.Warn("Unknown diff-tree status, skipping",
				zap.String("status", status),
				zap.String("line", line))
		}
	}

	return treeEntries, nil
}

// createTreeEntryForFile creates a tree entry for a single file by reading it and creating a blob
func (s *GitHubServiceImpl) createTreeEntryForFile(owner, repo, directory, filename, token string) (models.GitHubTreeEntry, error) {
	filePath := filepath.Join(directory, filename)

	// Read file content
	// #nosec G304 - filename comes from git diff-tree output in controlled repo directory
	content, err := os.ReadFile(filePath)
	if err != nil {
		return models.GitHubTreeEntry{}, fmt.Errorf("failed to read file %s: %w", filename, err)
	}

	// Create blob
	blobSHA, err := s.createBlob(owner, repo, string(content), token)
	if err != nil {
		return models.GitHubTreeEntry{}, fmt.Errorf("failed to create blob: %w", err)
	}

	// Determine file mode (check if executable)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return models.GitHubTreeEntry{}, fmt.Errorf("failed to stat file %s: %w", filename, err)
	}

	mode := "100644" // Regular file
	if fileInfo.Mode()&0111 != 0 {
		mode = "100755" // Executable
	}

	s.logger.Debug("Created blob for file",
		zap.String("file", filename),
		zap.String("sha", blobSHA),
		zap.String("mode", mode))

	// Add a small delay to avoid rate limiting
	time.Sleep(100 * time.Millisecond)

	return models.GitHubTreeEntry{
		Path: filename,
		Mode: mode,
		Type: "blob",
		SHA:  &blobSHA,
	}, nil
}

// createBlob creates a blob on GitHub with retry logic for rate limiting
func (s *GitHubServiceImpl) createBlob(owner, repo, content, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/blobs", owner, repo)

	blobReq := models.GitHubBlobRequest{
		Content:  content,
		Encoding: "utf-8",
	}

	jsonPayload, err := json.Marshal(blobReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal blob request: %w", err)
	}

	// Retry logic for rate limiting
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 2s, 4s, 8s
			// #nosec G115 - attempt is bounded by maxRetries (3), so shift is safe
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			s.logger.Warn("Rate limited, retrying blob creation",
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
				zap.String("owner", owner),
				zap.String("repo", repo))
			time.Sleep(delay)
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		resp, err := s.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to create blob: %w", err)
		}

		// Read response body
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(closeErr), zap.String("operation", "createBlob"))
		}
		if readErr != nil {
			return "", fmt.Errorf("failed to read response body: %w", readErr)
		}

		// Success case
		if resp.StatusCode == http.StatusCreated {
			var blobResp models.GitHubBlobResponse
			if err := json.Unmarshal(body, &blobResp); err != nil {
				return "", fmt.Errorf("failed to decode blob response: %w", err)
			}
			return blobResp.SHA, nil
		}

		// Check for rate limit errors
		// Primary rate limits: HTTP 429
		// Secondary rate limits: HTTP 403 with "rate limit" in error message
		isRateLimit := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode == http.StatusForbidden && strings.Contains(string(body), "rate limit"))

		if isRateLimit {
			// Check if Retry-After header is present
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				s.logger.Warn("Rate limit hit with Retry-After header",
					zap.String("retry_after", retryAfter),
					zap.Int("attempt", attempt),
					zap.Int("status_code", resp.StatusCode))
			}

			// Log rate limit headers for debugging
			s.logger.Debug("Rate limit headers",
				zap.String("x-ratelimit-remaining", resp.Header.Get("X-RateLimit-Remaining")),
				zap.String("x-ratelimit-reset", resp.Header.Get("X-RateLimit-Reset")),
				zap.Int("status_code", resp.StatusCode))

			if attempt < maxRetries {
				// Will retry after backoff
				continue
			}
			// Max retries exceeded
			return "", fmt.Errorf("rate limit exceeded after %d retries: %s, status: %d", maxRetries, string(body), resp.StatusCode)
		}

		// Other error - don't retry
		return "", fmt.Errorf("failed to create blob: %s, status: %d", string(body), resp.StatusCode)
	}

	return "", fmt.Errorf("failed to create blob after %d attempts", maxRetries)
}

// createTree creates a tree on GitHub
func (s *GitHubServiceImpl) createTree(owner, repo, baseTree string, entries []models.GitHubTreeEntry, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees", owner, repo)

	treeReq := models.GitHubTreeRequest{
		BaseTree: baseTree,
		Tree:     entries,
	}

	jsonPayload, err := json.Marshal(treeReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tree request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create tree: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "createTree"))
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create tree: %s, status: %d", string(body), resp.StatusCode)
	}

	var treeResp models.GitHubTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&treeResp); err != nil {
		return "", fmt.Errorf("failed to decode tree response: %w", err)
	}

	return treeResp.SHA, nil
}

// createCommit creates a commit on GitHub
func (s *GitHubServiceImpl) createCommit(owner, repo, message, tree, parent, token string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/commits", owner, repo)

	commitReq := models.GitHubCommitRequest{
		Message: message,
		Tree:    tree,
		Parents: []string{parent},
		// Note: We intentionally do NOT set Author or Committer
		// When these are omitted, GitHub automatically uses the authenticated GitHub App's identity
		// and creates a VERIFIED commit signed with GitHub's key
	}

	jsonPayload, err := json.Marshal(commitReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal commit request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "createCommit"))
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create commit: %s, status: %d", string(body), resp.StatusCode)
	}

	var commitResp models.GitHubCommitResponse
	if err := json.NewDecoder(resp.Body).Decode(&commitResp); err != nil {
		return "", fmt.Errorf("failed to decode commit response: %w", err)
	}

	return commitResp.SHA, nil
}

// updateReference updates a Git reference to point to a new commit
func (s *GitHubServiceImpl) updateReference(owner, repo, branchName, commitSHA, token string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs/heads/%s", owner, repo, branchName)

	refReq := models.GitHubReferenceRequest{
		SHA:   commitSHA,
		Force: false,
	}

	jsonPayload, err := json.Marshal(refReq)
	if err != nil {
		return fmt.Errorf("failed to marshal reference request: %w", err)
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update reference: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "updateReference"))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update reference: %s, status: %d", string(body), resp.StatusCode)
	}

	return nil
}

// getBranchBaseCommit gets the base commit SHA for a branch
// If the branch doesn't exist, falls back to the target branch
// Returns: (baseSHA, branchExists, error)
func (s *GitHubServiceImpl) getBranchBaseCommit(owner, repo, branchName, token string) (string, bool, error) {
	// Try to get the branch reference
	refURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs/heads/%s", owner, repo, branchName)
	req, err := http.NewRequest("GET", refURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to create reference request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("failed to get reference: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "getBranchBaseCommit"))
		}
	}()

	if resp.StatusCode == http.StatusOK {
		// Branch exists, return its SHA
		var refResponse models.GitHubGetReferenceResponse
		if err := json.NewDecoder(resp.Body).Decode(&refResponse); err != nil {
			return "", false, fmt.Errorf("failed to decode reference response: %w", err)
		}
		return refResponse.Object.SHA, true, nil
	}

	if resp.StatusCode == http.StatusNotFound {
		// Branch doesn't exist, fall back to target branch
		s.logger.Info("Branch does not exist on remote, using target branch as base",
			zap.String("branch", branchName),
			zap.String("targetBranch", s.config.GitHub.TargetBranch))

		// Get target branch reference
		targetRefURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs/heads/%s", owner, repo, s.config.GitHub.TargetBranch)
		targetReq, err := http.NewRequest("GET", targetRefURL, nil)
		if err != nil {
			return "", false, fmt.Errorf("failed to create target branch request: %w", err)
		}

		targetReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		targetReq.Header.Set("Accept", "application/vnd.github.v3+json")

		targetResp, err := s.client.Do(targetReq)
		if err != nil {
			return "", false, fmt.Errorf("failed to get target branch reference: %w", err)
		}
		defer func() {
			if localErr := targetResp.Body.Close(); localErr != nil {
				s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "getBranchBaseCommit-target"))
			}
		}()

		if targetResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(targetResp.Body)
			return "", false, fmt.Errorf("failed to get target branch %s: %s, status: %d", s.config.GitHub.TargetBranch, string(body), targetResp.StatusCode)
		}

		var targetRefResponse models.GitHubGetReferenceResponse
		if err := json.NewDecoder(targetResp.Body).Decode(&targetRefResponse); err != nil {
			return "", false, fmt.Errorf("failed to decode target branch response: %w", err)
		}

		return targetRefResponse.Object.SHA, false, nil
	}

	// Unexpected status code
	body, _ := io.ReadAll(resp.Body)
	return "", false, fmt.Errorf("unexpected response when getting branch reference: %s, status: %d", string(body), resp.StatusCode)
}

// createReference creates a new Git reference
func (s *GitHubServiceImpl) createReference(owner, repo, branchName, commitSHA, token string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs", owner, repo)

	refReq := models.GitHubCreateReferenceRequest{
		Ref: fmt.Sprintf("refs/heads/%s", branchName),
		SHA: commitSHA,
	}

	jsonPayload, err := json.Marshal(refReq)
	if err != nil {
		return fmt.Errorf("failed to marshal reference request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create reference: %w", err)
	}
	defer func() {
		if localErr := resp.Body.Close(); localErr != nil {
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "createReference"))
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create reference: %s, status: %d", string(body), resp.StatusCode)
	}

	return nil
}

// CreatePullRequest creates a pull request
func (s *GitHubServiceImpl) CreatePullRequest(owner, repo, title, body, head, base string) (*models.GitHubCreatePRResponse, error) {
	// Use installation-specific client
	// PRs are created on the base repository, so use the base repo's installation
	installationID, err := s.GetInstallationIDForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation ID for %s/%s: %w", owner, repo, err)
	}
	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	s.logger.Info("Creating PR using base repository's installation",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int64("installationID", installationID))

	// Set maintainer_can_modify to false explicitly
	// This is required when using GitHub App tokens to create PRs from forks
	// See: https://github.com/orgs/community/discussions/39178
	falseValue := false
	newPR := &github.NewPullRequest{
		Title:               &title,
		Body:                &body,
		Head:                &head,
		Base:                &base,
		MaintainerCanModify: &falseValue,
	}

	ctx := context.Background()
	pr, _, err := ghClient.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	// Add label to the PR
	if s.config.GitHub.PRLabel != "" {
		_, _, err = ghClient.Issues.AddLabelsToIssue(ctx, owner, repo, pr.GetNumber(), []string{s.config.GitHub.PRLabel})
		if err != nil {
			s.logger.Warn("Failed to add label to PR",
				zap.String("label", s.config.GitHub.PRLabel),
				zap.Int("prNumber", pr.GetNumber()),
				zap.Error(err))
			// Don't fail the whole operation if label addition fails
		}
	}

	// Convert go-github PR to our model
	return &models.GitHubCreatePRResponse{
		ID:      pr.GetID(),
		Number:  pr.GetNumber(),
		State:   pr.GetState(),
		Title:   pr.GetTitle(),
		Body:    pr.GetBody(),
		HTMLURL: pr.GetHTMLURL(),
	}, nil
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

// HasChanges checks if there are any uncommitted changes in the repository
// Returns true if there are changes (modified, added, or deleted files)
func (s *GitHubServiceImpl) HasChanges(directory string) (bool, error) {
	fn := zap.String("function", "HasChanges")

	// Check for working tree changes
	hasWorkingTreeChanges, err := s.hasWorkingTreeChanges(directory, fn)
	if err != nil {
		return false, err
	}
	if hasWorkingTreeChanges {
		return true, nil
	}

	// Check for unpushed commits (e.g., merge commits created by AI)
	hasUnpushedCommits, err := s.hasUnpushedCommits(directory, fn)
	if err != nil {
		return false, err
	}

	return hasUnpushedCommits, nil
}

// hasWorkingTreeChanges checks if there are uncommitted changes in the working tree
func (s *GitHubServiceImpl) hasWorkingTreeChanges(directory string, fn zapcore.Field) (bool, error) {
	// Use git status --porcelain to get machine-readable status
	// Empty output means no changes
	// Always capture stdout since we need to check if there's output
	cmd := newGitCommand(s.executor("git", "status", "--porcelain"), directory, true, true)

	if err := cmd.run(); err != nil {
		return false, fmt.Errorf("failed to check git status: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("git status --porcelain", fn, zap.String("stdout", cmd.getStdout()), zap.String("stderr", cmd.getStderr()))

	// If stdout is empty, there are no working tree changes
	return cmd.hasStdout(), nil
}

// hasUnpushedCommits checks if there are local commits that haven't been pushed to origin
func (s *GitHubServiceImpl) hasUnpushedCommits(directory string, fn zapcore.Field) (bool, error) {
	// First, check if origin remote exists
	remoteCmd := newGitCommand(s.executor("git", "remote", "get-url", "origin"), directory, false, false)
	if err := remoteCmd.run(); err != nil {
		// No origin remote configured - this is fine, means no unpushed commits to check
		s.logger.Debug("No origin remote configured", fn)
		return false, nil
	}

	// Get the current branch name
	// Always capture stdout since we need the output
	branchCmd := newGitCommand(s.executor("git", "rev-parse", "--abbrev-ref", "HEAD"), directory, true, true)
	if err := branchCmd.run(); err != nil {
		return false, fmt.Errorf("failed to get current branch: %w, stderr: %s", err, branchCmd.getStderr())
	}

	branchName := strings.TrimSpace(branchCmd.getStdout())
	if branchName == "" {
		return false, fmt.Errorf("unable to determine current branch")
	}

	s.logger.Debug("Current branch", fn, zap.String("branch", branchName))

	// Check if the remote branch exists
	// We don't need stdout here, just the exit code
	remoteExistsCmd := newGitCommand(s.executor("git", "rev-parse", "--verify", fmt.Sprintf("origin/%s", branchName)), directory, false, false)
	if err := remoteExistsCmd.run(); err != nil {
		// Remote branch doesn't exist yet - this means we have unpushed commits (the entire branch is new)
		s.logger.Debug("Remote branch does not exist, treating as unpushed commits", fn, zap.String("branch", branchName))
		return true, nil
	}

	// Check for commits that exist locally but not on the remote
	// git log origin/branch..HEAD will show commits in HEAD that aren't in origin/branch
	// Always capture stdout since we need to check if there's output
	logCmd := newGitCommand(s.executor("git", "log", fmt.Sprintf("origin/%s..HEAD", branchName), "--oneline"), directory, true, true)
	if err := logCmd.run(); err != nil {
		return false, fmt.Errorf("failed to check unpushed commits: %w, stderr: %s", err, logCmd.getStderr())
	}

	s.logger.Debug("git log origin/branch..HEAD", fn, zap.String("branch", branchName), zap.String("stdout", logCmd.getStdout()), zap.String("stderr", logCmd.getStderr()))

	// If there's any output, we have unpushed commits
	return logCmd.hasStdout(), nil
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
	installationID, err := s.GetInstallationIDForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx := context.Background()
	comment := &github.IssueComment{
		Body: &body,
	}

	_, _, err = ghClient.Issues.CreateComment(ctx, owner, repo, prNumber, comment)
	if err != nil {
		return fmt.Errorf("failed to add PR comment: %w", err)
	}

	return nil
}

// ReplyToPRComment replies to a specific PR review comment
// For line-based review comments, this creates a threaded reply
func (s *GitHubServiceImpl) ReplyToPRComment(owner, repo string, prNumber int, commentID int64, body string) error {
	installationID, err := s.GetInstallationIDForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx := context.Background()

	// Create a reply to a review comment
	comment := &github.PullRequestComment{
		Body:      &body,
		InReplyTo: &commentID,
	}

	_, _, err = ghClient.PullRequests.CreateComment(ctx, owner, repo, prNumber, comment)
	if err != nil {
		return fmt.Errorf("failed to reply to PR comment: %w", err)
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
