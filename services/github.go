package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// ErrNoChanges is returned by CommitChanges when all workspace changes
// are bot artifacts and there is nothing to commit.
var ErrNoChanges = errors.New("no committable changes")

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
	// githubAPITimeout is the default timeout for GitHub API operations
	// Allows sufficient time for API calls while preventing indefinite hangs
	githubAPITimeout = 30 * time.Second

	// maxPaginationPages is the safety limit for API pagination
	// 100 pages * 100 items per page = 10,000 items max
	maxPaginationPages = 100

	// maxMergeCommitFiles is the safety limit for files in a single merge commit
	// Prevents DoS and OOM when processing commits with thousands of files
	maxMergeCommitFiles = 1000
)

// GitHubServiceImpl is the concrete implementation for GitHub operations.
// There is no shared interface in this package — each consumer package
// declares a narrow interface for only the methods it needs, and
// GitHubServiceImpl satisfies them all implicitly.
type GitHubServiceImpl struct {
	config               *models.Config
	client               *http.Client
	appTransport         *ghinstallation.AppsTransport       // For app-level operations
	installationAuth     map[int64]*ghinstallation.Transport // Per-installation auth
	installationAuthMu   sync.RWMutex                        // Protects installationAuth map
	installationClients  map[int64]*github.Client            // Per-installation go-github clients
	installationClientMu sync.RWMutex                        // Protects installationClients map
	installationIDs      map[string]int64                    // Cache: "owner/repo" -> installation ID
	installationIDsMu    sync.RWMutex                        // Protects installationIDs map
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

// NewGitHubService creates a new GitHubServiceImpl.
func NewGitHubService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) *GitHubServiceImpl {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}

	service := &GitHubServiceImpl{
		config:              config,
		client:              &http.Client{},
		installationAuth:    make(map[int64]*ghinstallation.Transport),
		installationClients: make(map[int64]*github.Client),
		installationIDs:     make(map[string]int64),
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

	logger.Info("Using GitHub App authentication",
		zap.Int64("appID", config.GitHub.AppID))

	return service
}

// getInstallationGitHubClient returns a go-github client authenticated for a specific installation
// Uses double-checked locking pattern for thread-safe lazy initialization of per-installation clients
// Note: Currently no error path exists (NewFromAppsTransport doesn't return error), but error return
// is kept for defensive programming in case future go-github versions change the API
func (s *GitHubServiceImpl) getInstallationGitHubClient(installationID int64) (*github.Client, error) {
	// Fast path: check if client already exists with read lock
	s.installationClientMu.RLock()
	client, exists := s.installationClients[installationID]
	s.installationClientMu.RUnlock()

	if exists {
		return client, nil
	}

	// Get or create the installation transport FIRST (outside client lock to avoid deadlock)
	// This ensures consistent lock ordering: always acquire installationAuthMu before installationClientMu
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

	// Now acquire client lock and create client
	s.installationClientMu.Lock()
	defer s.installationClientMu.Unlock()

	// Double-check: another goroutine might have created the client while we waited
	if existingClient, found := s.installationClients[installationID]; found {
		return existingClient, nil
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
	owner, repo, err := extractRepoInfo(repoURL)
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

	installationID, err := s.getInstallationIDForRepo(owner, repo)
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

// CommitChanges creates a verified commit via the GitHub API from local
// repository state. It handles both working tree changes and local commits
// (including merge commits with multiple parents). Returns an empty string
// if there are no changes; otherwise returns the commit SHA.
//
// If coAuthor is non-nil, a Co-authored-by trailer is appended to the
// commit message using the author's Name and Email.
func (s *GitHubServiceImpl) CommitChanges(owner, repo, branch, message, dir string, coAuthor *models.Author, importExcludes []string) (string, error) {
	// Extract co-author name/email (empty strings when nil).
	var coAuthorName, coAuthorEmail string
	if coAuthor != nil {
		coAuthorName = coAuthor.Name
		coAuthorEmail = coAuthor.Email
	}

	fn := zap.String("function", "CommitChanges")

	// Check what kind of changes we have
	hasChanges, err := s.HasChanges(dir)
	if err != nil {
		return "", fmt.Errorf("failed to check for changes: %w", err)
	}

	if !hasChanges {
		s.logger.Info("No changes to commit")
		return "", nil
	}

	// Normalize: ensure all changes are committed locally so that
	// createVerifiedCommitFromLocalHEAD (which uses git diff-tree
	// against HEAD) sees everything the AI produced — whether the
	// AI committed, staged, or left changes in the working tree.
	if err := s.stageAndCommitLocal(dir, fn); err != nil {
		return "", fmt.Errorf("failed to normalize local changes: %w", err)
	}

	excludes := mergeExcludes(importExcludes)
	return s.createVerifiedCommitFromLocalHEAD(owner, repo, branch, message, dir, coAuthorName, coAuthorEmail, excludes)
}

// createVerifiedCommitFromLocalHEAD creates a verified commit via API from local HEAD commit
// Preserves merge commit structure if HEAD is a merge commit
func (s *GitHubServiceImpl) createVerifiedCommitFromLocalHEAD(owner, repo, branchName, message, directory string, coAuthorName, coAuthorEmail string, excludes []string) (string, error) {
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

	// Get the base tree from the first parent on GitHub.
	// The parent may not exist on the remote when the AI created
	// local commits (rebase, merge, or regular commits that only
	// exist locally). In that case, fall back to the remote branch
	// HEAD so we can still create a valid API commit.
	baseTreeSHA, err := s.getTreeSHAFromCommit(owner, repo, firstParent, token)
	if err != nil {
		remoteSHA, branchExists, remoteErr := s.getBranchBaseCommit(owner, repo, branchName, token)
		if remoteErr != nil || !branchExists {
			return "", fmt.Errorf("failed to get base tree from first parent: %w", err)
		}
		s.logger.Warn("Local parent not found on remote, falling back to remote branch HEAD",
			zap.String("localParent", firstParent),
			zap.String("remoteSHA", remoteSHA),
			zap.Error(err))
		firstParent = remoteSHA
		parentSHAs = []string{remoteSHA}
		baseTreeSHA, err = s.getTreeSHAFromCommit(owner, repo, firstParent, token)
		if err != nil {
			return "", fmt.Errorf("failed to get base tree from remote branch HEAD: %w", err)
		}
	}

	// Create blobs for files that changed from the first parent
	// For merge commits, this includes files that differ from the first parent
	treeEntries, err := s.createBlobsForFilesChangedFromParent(owner, repo, directory, firstParent, token, excludes)
	if err != nil {
		return "", fmt.Errorf("failed to create blobs: %w", err)
	}

	if len(treeEntries) == 0 {
		s.logger.Info("No changes from first parent; nothing to commit")
		return "", ErrNoChanges
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

	// Create or update the branch reference to point to the new commit.
	// The branch exists locally but may not have been pushed to the
	// remote yet, so we must check before choosing the operation.
	_, branchExists, err := s.getBranchBaseCommit(owner, repo, branchName, token)
	if err != nil {
		return "", fmt.Errorf("failed to check branch existence: %w", err)
	}

	if branchExists {
		if err := s.updateReference(owner, repo, branchName, commitSHA, token); err != nil {
			return "", fmt.Errorf("failed to update reference: %w", err)
		}
	} else {
		if err := s.createReference(owner, repo, branchName, commitSHA, token); err != nil {
			return "", fmt.Errorf("failed to create reference: %w", err)
		}
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

// createBlobsForFilesChangedFromParent creates tree entries for all changes from a specific parent commit
// Handles additions, modifications, deletions, and renames properly using git diff-tree
func (s *GitHubServiceImpl) createBlobsForFilesChangedFromParent(owner, repo, directory, parentSHA, token string, excludes []string) ([]models.GitHubTreeEntry, error) {
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
			// Skip new files at the repo root — these are almost always
			// AI scratch files (test scripts, notes) rather than real
			// source changes. Modified root files are allowed.
			if status == "A" && !strings.Contains(filename, "/") {
				s.logger.Info("Skipping new root-level file",
					zap.String("file", filename))
				continue
			}
			entry, err := s.createTreeEntryForFile(owner, repo, directory, filename, token, excludes)
			if errors.Is(err, errSkipEntry) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("failed to create tree entry for %s: %w", filename, err)
			}
			treeEntries = append(treeEntries, entry)

		case status == "D":
			// Deleted file - add entry with nil SHA to remove from tree
			filename := parts[1]
			if isExcludedPath(filename, excludes) {
				continue
			}
			s.logger.Debug("File deleted, adding deletion entry",
				zap.String("file", filename))
			treeEntries = append(treeEntries, models.GitHubTreeEntry{
				Path: filename,
				Mode: "100644",
				Type: "blob",
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
			if isExcludedPath(oldPath, excludes) && isExcludedPath(newPath, excludes) {
				continue
			}

			s.logger.Debug("File renamed",
				zap.String("old_path", oldPath),
				zap.String("new_path", newPath))

			// Delete old path
			if !isExcludedPath(oldPath, excludes) {
				treeEntries = append(treeEntries, models.GitHubTreeEntry{
					Path: oldPath,
					Mode: "100644",
					Type: "blob",
					SHA:  nil,
				})
			}

			// Add new path
			entry, err := s.createTreeEntryForFile(owner, repo, directory, newPath, token, excludes)
			if errors.Is(err, errSkipEntry) {
				continue
			}
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
			entry, err := s.createTreeEntryForFile(owner, repo, directory, newPath, token, excludes)
			if errors.Is(err, errSkipEntry) {
				continue
			}
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

// errSkipEntry signals that a tree entry should be skipped (e.g.,
// the path is a directory or a bot artifact).
var errSkipEntry = errors.New("skip entry")

// builtinExcludes lists directories that are always excluded from
// commits. Import-declared excludes are merged at call time.
var builtinExcludes = []string{".ai-bot/"}

// mergeExcludes combines builtin excludes with import-declared excludes,
// normalizing each entry to have a trailing slash for prefix matching.
func mergeExcludes(importExcludes []string) []string {
	all := make([]string, len(builtinExcludes), len(builtinExcludes)+len(importExcludes))
	copy(all, builtinExcludes)
	for _, e := range importExcludes {
		if !strings.HasSuffix(e, "/") {
			e += "/"
		}
		all = append(all, e)
	}
	return all
}

// isExcludedPath reports whether filename is inside any of the
// excluded directories.
func isExcludedPath(filename string, excludes []string) bool {
	for _, prefix := range excludes {
		dir := strings.TrimSuffix(prefix, "/")
		if filename == dir || strings.HasPrefix(filename, prefix) {
			return true
		}
	}
	return false
}

// createTreeEntryForFile creates a tree entry for a single file by reading it and creating a blob.
// Returns errSkipEntry if the path is a directory.
func (s *GitHubServiceImpl) createTreeEntryForFile(owner, repo, directory, filename, token string, excludes []string) (models.GitHubTreeEntry, error) {
	// Skip excluded paths — bot artifacts and import-declared output dirs.
	if isExcludedPath(filename, excludes) {
		s.logger.Debug("Skipping excluded path",
			zap.String("path", filename))
		return models.GitHubTreeEntry{}, errSkipEntry
	}

	filePath := filepath.Join(directory, filename)

	// Check if path is a directory.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return models.GitHubTreeEntry{}, fmt.Errorf("failed to stat file %s: %w", filename, err)
	}
	if fileInfo.IsDir() {
		s.logger.Debug("Skipping directory entry",
			zap.String("path", filename))
		return models.GitHubTreeEntry{}, errSkipEntry
	}

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

// CreatePR creates a pull request from the given parameters and returns
// the created PR metadata. If params.Labels is non-empty those labels are
// applied; otherwise the configured PRLabel (if any) is used as a
// backwards-compatible default.
func (s *GitHubServiceImpl) CreatePR(params models.PRParams) (*models.PR, error) {
	// Use installation-specific client
	// PRs are created on the base repository, so use the base repo's installation
	installationID, err := s.getInstallationIDForRepo(params.Owner, params.Repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation ID for %s/%s: %w", params.Owner, params.Repo, err)
	}
	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	s.logger.Info("Creating PR using base repository's installation",
		zap.String("owner", params.Owner),
		zap.String("repo", params.Repo),
		zap.Int64("installationID", installationID))

	// Set maintainer_can_modify to false explicitly
	// This is required when using GitHub App tokens to create PRs from forks
	// See: https://github.com/orgs/community/discussions/39178
	falseValue := false
	newPR := &github.NewPullRequest{
		Title:               &params.Title,
		Body:                &params.Body,
		Head:                &params.Head,
		Base:                &params.Base,
		MaintainerCanModify: &falseValue,
		Draft:               &params.Draft,
	}

	// Create PR with its own timeout
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	pr, _, err := ghClient.PullRequests.Create(ctx, params.Owner, params.Repo, newPR)
	cancel() // Release resources immediately after PR creation
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	// Defensive nil check - should never occur if go-github contract is honored,
	// but protects against unexpected API behavior changes
	if pr == nil {
		return nil, fmt.Errorf("GitHub API returned nil pull request (unexpected API contract violation)")
	}

	// Determine labels to apply: prefer explicit params, fall back to config.
	labels := params.Labels
	if len(labels) == 0 && s.config.GitHub.PRLabel != "" {
		labels = []string{s.config.GitHub.PRLabel}
	}

	// Add labels with a fresh timeout to prevent cascading timeout if PR creation was slow
	if len(labels) > 0 {
		labelCtx, labelCancel := context.WithTimeout(context.Background(), githubAPITimeout)
		defer labelCancel()
		_, _, err = ghClient.Issues.AddLabelsToIssue(labelCtx, params.Owner, params.Repo, pr.GetNumber(), labels)
		if err != nil {
			return nil, fmt.Errorf("failed to add labels to PR #%d: %w (PR created but not labeled)",
				pr.GetNumber(), err)
		}
	}

	// Assign users to the PR.
	if len(params.Assignees) > 0 {
		assignCtx, assignCancel := context.WithTimeout(context.Background(), githubAPITimeout)
		defer assignCancel()
		_, _, err = ghClient.Issues.AddAssignees(assignCtx, params.Owner, params.Repo, pr.GetNumber(), params.Assignees)
		if err != nil {
			// Non-fatal: log but don't fail the PR creation.
			s.logger.Warn("Failed to assign PR",
				zap.Int("pr", pr.GetNumber()),
				zap.Strings("assignees", params.Assignees),
				zap.Error(err))
		}
	}

	return &models.PR{
		Number: pr.GetNumber(),
		URL:    pr.GetHTMLURL(),
		State:  pr.GetState(),
	}, nil
}

// SwitchBranch switches to a specific branch.
func (s *GitHubServiceImpl) SwitchBranch(directory, branchName string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "SwitchBranch")

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

// stageAndCommitLocal ensures all working tree and staged changes are
// committed locally. This normalizes the three possible states an AI
// session can leave behind (committed, staged, or unstaged) into a
// single committed state that git diff-tree can see.
func (s *GitHubServiceImpl) stageAndCommitLocal(directory string, fn zapcore.Field) error {
	// Check for uncommitted changes (staged or unstaged).
	statusCmd := newGitCommand(s.executor("git", "status", "--porcelain"), directory, true, true)
	if err := statusCmd.run(); err != nil {
		return fmt.Errorf("git status: %w, stderr: %s", err, statusCmd.getStderr())
	}
	if !statusCmd.hasStdout() {
		// Everything is already committed.
		return nil
	}

	s.logger.Debug("Staging uncommitted changes", fn)

	addCmd := newGitCommand(s.executor("git", "add", "-A"), directory, false, true)
	if err := addCmd.run(); err != nil {
		return fmt.Errorf("git add -A: %w, stderr: %s", err, addCmd.getStderr())
	}

	commitCmd := newGitCommand(
		s.executor("git", "commit", "-m", "AI changes (local only)"),
		directory, false, true)
	if err := commitCmd.run(); err != nil {
		return fmt.Errorf("git commit: %w, stderr: %s", err, commitCmd.getStderr())
	}

	s.logger.Debug("Committed local changes", fn)
	return nil
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

	// Determine the remote ref to compare against: origin/<branch> if it
	// exists, otherwise origin/<targetBranch> (the branch we forked from).
	remoteRef := fmt.Sprintf("origin/%s", branchName)
	remoteExistsCmd := newGitCommand(s.executor("git", "rev-parse", "--verify", remoteRef), directory, false, false)
	if err := remoteExistsCmd.run(); err != nil {
		// Remote branch doesn't exist. Compare against origin/<target>
		// to check if HEAD has diverged (i.e., the AI made local commits).
		remoteRef = fmt.Sprintf("origin/%s", s.config.GitHub.TargetBranch)
		s.logger.Debug("Remote branch does not exist, comparing against target branch", fn,
			zap.String("branch", branchName),
			zap.String("targetRef", remoteRef))
	}

	// Check for commits that exist locally but not on the remote ref.
	logCmd := newGitCommand(s.executor("git", "log", fmt.Sprintf("%s..HEAD", remoteRef), "--oneline"), directory, true, true)
	if err := logCmd.run(); err != nil {
		return false, fmt.Errorf("failed to check unpushed commits: %w, stderr: %s", err, logCmd.getStderr())
	}

	s.logger.Debug("git log ref..HEAD", fn,
		zap.String("ref", remoteRef),
		zap.String("stdout", logCmd.getStdout()),
		zap.String("stderr", logCmd.getStderr()))

	// If there's any output, we have unpushed commits
	return logCmd.hasStdout(), nil
}

// StripRemoteAuth removes authentication credentials from the
// workspace's origin remote URL. After this call, push operations
// will be rejected by the remote.
func (s *GitHubServiceImpl) StripRemoteAuth(directory string) error {
	cmd := newGitCommand(s.executor("git", "remote", "get-url", "origin"), directory, false, true)
	if err := cmd.run(); err != nil {
		return fmt.Errorf("get remote URL: %w, stderr: %s", err, cmd.getStderr())
	}

	rawURL := strings.TrimSpace(cmd.getStdout())
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse remote URL: %w", err)
	}
	parsed.User = nil

	setCmd := newGitCommand(
		s.executor("git", "remote", "set-url", "origin", parsed.String()),
		directory, false, false)
	if err := setCmd.run(); err != nil {
		return fmt.Errorf("strip remote auth: %w, stderr: %s", err, setCmd.getStderr())
	}

	s.logger.Debug("Stripped remote auth", zap.String("directory", directory))
	return nil
}

// RestoreRemoteAuth restores authentication credentials on the
// workspace's origin remote URL using a fresh installation token.
func (s *GitHubServiceImpl) RestoreRemoteAuth(directory, owner, repo string) error {
	token, err := s.getAuthTokenForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("get auth token: %w", err)
	}

	authURL := fmt.Sprintf("https://%s@github.com/%s/%s.git", token, owner, repo)
	cmd := newGitCommand(
		s.executor("git", "remote", "set-url", "origin", authURL),
		directory, false, false)
	if err := cmd.run(); err != nil {
		return fmt.Errorf("restore remote auth: %w, stderr: %s", err, cmd.getStderr())
	}

	s.logger.Debug("Restored remote auth", zap.String("directory", directory))
	return nil
}

// SyncWithRemote reconciles the local workspace with the remote branch by
// fetching and hard-resetting to the remote ref. Excluded artifact
// directories are preserved across the reset because they are filtered
// from API commits and therefore absent on the remote branch.
// FetchRemote fetches all refs from the origin remote. Used in
// fork-based workflows to make fork branches available in a
// workspace that was originally cloned from upstream.
func (s *GitHubServiceImpl) FetchRemote(directory string) error {
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	fn := zap.String("function", "FetchRemote")

	fetchCmd := newGitCommand(s.executor("git", "fetch", "origin"), directory, debugEnabled, true)
	if err := fetchCmd.run(); err != nil {
		return fmt.Errorf("failed to fetch from origin: %w, stderr: %s", err, fetchCmd.getStderr())
	}
	s.logger.Debug("git fetch origin", fn, zap.String("stdout", fetchCmd.getStdout()), zap.String("stderr", fetchCmd.getStderr()))

	return nil
}

func (s *GitHubServiceImpl) SyncWithRemote(directory, branch string, importExcludes []string) error {
	fn := zap.String("function", "SyncWithRemote")

	// Preserve excluded directories across the hard reset.
	// The normalization commit tracks these files locally, but they
	// are filtered from the API commit and absent on the remote —
	// so git reset --hard would delete them.
	excludes := mergeExcludes(importExcludes)
	preserved := s.preserveExcludedDirs(directory, excludes, fn)

	if err := s.FetchRemote(directory); err != nil {
		return err
	}

	// Reset the working tree and index to match the remote branch.
	debugEnabled := s.logger.Core().Enabled(zapcore.DebugLevel)
	ref := "origin/" + branch
	resetCmd := newGitCommand(s.executor("git", "reset", "--hard", ref), directory, debugEnabled, true)
	if err := resetCmd.run(); err != nil {
		return fmt.Errorf("failed to reset to %s: %w, stderr: %s", ref, err, resetCmd.getStderr())
	}
	s.logger.Debug("git reset --hard", fn, zap.String("ref", ref), zap.String("stdout", resetCmd.getStdout()), zap.String("stderr", resetCmd.getStderr()))

	// Restore preserved directories after the reset.
	s.restoreExcludedDirs(directory, preserved, fn)

	return nil
}

// preserveExcludedDirs moves excluded artifact directories to temporary
// names so they survive a git reset --hard. Returns the list of
// directory base names that were successfully preserved.
func (s *GitHubServiceImpl) preserveExcludedDirs(directory string, excludes []string, fn zapcore.Field) []string {
	var preserved []string
	for _, prefix := range excludes {
		dir := strings.TrimSuffix(prefix, "/")
		src := filepath.Join(directory, dir)
		dst := filepath.Join(directory, dir+".preserve")

		if _, err := os.Stat(src); err != nil {
			continue
		}
		_ = os.RemoveAll(dst) // clean up any stale preserve dir
		if err := os.Rename(src, dst); err != nil {
			s.logger.Warn("Failed to preserve directory", fn,
				zap.String("dir", dir), zap.Error(err))
			continue
		}
		preserved = append(preserved, dir)
	}
	return preserved
}

// restoreExcludedDirs moves preserved artifact directories back to
// their original names after a git reset --hard.
func (s *GitHubServiceImpl) restoreExcludedDirs(directory string, preserved []string, fn zapcore.Field) {
	for _, dir := range preserved {
		src := filepath.Join(directory, dir+".preserve")
		dst := filepath.Join(directory, dir)
		if err := os.Rename(src, dst); err != nil {
			s.logger.Warn("Failed to restore directory", fn,
				zap.String("dir", dir), zap.Error(err))
		}
	}
}

// ReplyToComment replies to a specific PR review comment.
// For line-based review comments, this creates a threaded reply.
func (s *GitHubServiceImpl) ReplyToComment(owner, repo string, prNumber int, commentID int64, body string) error {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	_, _, err = ghClient.PullRequests.CreateCommentInReplyTo(ctx, owner, repo, prNumber, body, commentID)
	if err != nil {
		return fmt.Errorf("failed to reply to PR comment: %w", err)
	}

	s.logger.Debug("Replied to review comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber),
		zap.Int64("comment_id", commentID))

	return nil
}

// PostIssueComment posts a top-level comment on a PR (via the issues
// endpoint). Used for replying to conversation comments, which do not
// support threading.
func (s *GitHubServiceImpl) PostIssueComment(owner, repo string, prNumber int, body string) error {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	comment := &github.IssueComment{Body: github.Ptr(body)}
	_, _, err = ghClient.Issues.CreateComment(ctx, owner, repo, prNumber, comment)
	if err != nil {
		return fmt.Errorf("failed to post issue comment: %w", err)
	}

	s.logger.Debug("Posted issue comment",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", prNumber))

	return nil
}

// CloneImport clones an auxiliary repository into destDir. If ref is
// non-empty, the specified branch/tag/commit is checked out. This is a
// shallow clone (depth 1) since import repos are read-only references.
func (s *GitHubServiceImpl) CloneImport(url, destDir, ref string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, destDir)

	cmd := s.executor("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s into %s: %w, stderr: %s", url, destDir, err, stderr.String())
	}

	s.logger.Debug("Cloned import repo",
		zap.String("url", url),
		zap.String("dest", destDir),
		zap.String("ref", ref))

	return nil
}

// fetchPRReviewCommentsPage fetches a single page of PR review comments
func (s *GitHubServiceImpl) fetchPRReviewCommentsPage(owner, repo string, prNumber, page, perPage int) ([]models.GitHubPRComment, error) {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	opts := &github.PullRequestListCommentsOptions{
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: perPage,
		},
	}

	ghComments, _, err := ghClient.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR review comments: %w", err)
	}

	// Convert go-github comments to our model
	comments := make([]models.GitHubPRComment, 0, len(ghComments))
	for _, c := range ghComments {
		// Defensive nil check for User object
		user := c.GetUser()
		login := ""
		if user != nil {
			login = user.GetLogin()
		}

		comments = append(comments, models.GitHubPRComment{
			ID:          c.GetID(),
			InReplyToID: c.GetInReplyTo(),
			User: models.GitHubUser{
				Login: login,
			},
			Body:      c.GetBody(),
			Path:      c.GetPath(),
			Line:      c.GetLine(),
			StartLine: c.GetStartLine(),
			Side:      c.GetSide(),
			StartSide: c.GetStartSide(),
			HTMLURL:   c.GetHTMLURL(),
			CreatedAt: c.GetCreatedAt().Time,
			UpdatedAt: c.GetUpdatedAt().Time,
		})
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
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation ID: %w", err)
	}

	ghClient, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: perPage,
		},
	}

	ghComments, _, err := ghClient.Issues.ListComments(ctx, owner, repo, prNumber, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR conversation comments: %w", err)
	}

	// Convert go-github comments to our model
	// Note: Issue comments don't have path/line/side fields (only PR review comments do)
	comments := make([]models.GitHubPRComment, 0, len(ghComments))
	for _, c := range ghComments {
		// Defensive nil check for User object
		user := c.GetUser()
		login := ""
		if user != nil {
			login = user.GetLogin()
		}

		comments = append(comments, models.GitHubPRComment{
			ID: c.GetID(),
			User: models.GitHubUser{
				Login: login,
			},
			Body:      c.GetBody(),
			HTMLURL:   c.GetHTMLURL(),
			CreatedAt: c.GetCreatedAt().Time,
			UpdatedAt: c.GetUpdatedAt().Time,
			// Path, Line, StartLine, Side, StartSide not set for conversation comments
		})
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

// GetPRComments returns all PR comments (both line-based review comments
// and general conversation comments), converted to models.PRComment.
// If since is non-zero, only comments created after that timestamp are
// returned.
func (s *GitHubServiceImpl) GetPRComments(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
	// Get line-based review comments from pulls endpoint
	reviewComments, err := s.listPRReviewComments(owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to get review comments: %w", err)
	}

	// Get general conversation comments from issues endpoint
	conversationComments, err := s.listPRConversationComments(owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation comments: %w", err)
	}

	s.logger.Debug("Retrieved PR comments",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("pr_number", number),
		zap.Int("review_comments", len(reviewComments)),
		zap.Int("conversation_comments", len(conversationComments)),
		zap.Int("total_comments", len(reviewComments)+len(conversationComments)))

	// Convert to models.PRComment and apply the since filter.
	// Review comments and conversation comments are converted
	// separately so IsReviewComment is set correctly.
	result := make([]models.PRComment, 0, len(reviewComments)+len(conversationComments))
	for _, c := range reviewComments {
		if !since.IsZero() && !c.CreatedAt.After(since) {
			continue
		}
		result = append(result, models.PRComment{
			ID: c.ID,
			Author: models.Author{
				Name:     c.User.Login,
				Username: c.User.Login,
			},
			Body:            c.Body,
			FilePath:        c.Path,
			Line:            c.Line,
			Timestamp:       c.CreatedAt,
			InReplyTo:       c.InReplyToID,
			IsReviewComment: true,
		})
	}
	for _, c := range conversationComments {
		if !since.IsZero() && !c.CreatedAt.After(since) {
			continue
		}
		result = append(result, models.PRComment{
			ID: c.ID,
			Author: models.Author{
				Name:     c.User.Login,
				Username: c.User.Login,
			},
			Body:      c.Body,
			Timestamp: c.CreatedAt,
		})
	}

	return result, nil
}

// extractRepoInfo extracts owner and repo from a repository URL.
func extractRepoInfo(repoURL string) (owner, repo string, err error) {
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

// getInstallationIDForRepo discovers the GitHub App installation ID for a specific repository.
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
//	installationID, err := githubService.getInstallationIDForRepo("myorg", "myrepo")
//	if err != nil {
//	    // Handle error - app may not be installed on this repo
//	}
func (s *GitHubServiceImpl) getInstallationIDForRepo(owner, repo string) (int64, error) {
	if s.appTransport == nil {
		return 0, fmt.Errorf("GitHub App not configured")
	}

	key := fmt.Sprintf("%s/%s", owner, repo)

	// Fast path: check cache with read lock
	s.installationIDsMu.RLock()
	installationID, exists := s.installationIDs[key]
	s.installationIDsMu.RUnlock()

	if exists {
		return installationID, nil
	}

	// Slow path: fetch from API with timeout
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/installation", owner, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
			s.logger.Error("Failed to close response body", zap.Error(localErr), zap.String("operation", "getInstallationIDForRepo"))
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
		zap.String("repo", key),
		zap.Int64("installationID", installation.ID))

	// Cache the result
	s.installationIDsMu.Lock()
	s.installationIDs[key] = installation.ID
	s.installationIDsMu.Unlock()

	return installation.ID, nil
}

// GetPRForBranch finds the open pull request whose head branch matches
// the given name. Returns an error if no matching PR is found.
func (s *GitHubServiceImpl) GetPRForBranch(owner, repo, head string) (*models.PRDetails, error) {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get installation ID: %w", err)
	}

	client, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("get GitHub client: %w", err)
	}

	prs, _, err := client.PullRequests.List(context.Background(), owner, repo, &github.PullRequestListOptions{
		State: "open",
		Head:  head,
	})
	if err != nil {
		return nil, fmt.Errorf("list PRs for branch %s: %w", head, err)
	}

	// The head parameter may use "owner:branch" format for cross-repo
	// (fork) PRs. Extract just the branch name for comparison since
	// pr.GetHead().GetRef() returns the branch without an owner prefix.
	refToMatch := head
	if _, branch, ok := strings.Cut(head, ":"); ok {
		refToMatch = branch
	}

	for _, pr := range prs {
		if pr.GetHead().GetRef() == refToMatch {
			return &models.PRDetails{
				Number:     pr.GetNumber(),
				Title:      pr.GetTitle(),
				Branch:     pr.GetHead().GetRef(),
				BaseBranch: pr.GetBase().GetRef(),
				URL:        pr.GetHTMLURL(),
			}, nil
		}
	}

	return nil, fmt.Errorf("no open PR found for branch %s", head)
}

// BranchHasCommits reports whether the branch has commits beyond the
// base branch. Used by crash recovery to detect completed AI work.
func (s *GitHubServiceImpl) BranchHasCommits(owner, repo, branch, base string) (bool, error) {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return false, fmt.Errorf("get installation ID: %w", err)
	}

	client, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return false, fmt.Errorf("get GitHub client: %w", err)
	}

	comparison, _, err := client.Repositories.CompareCommits(
		context.Background(), owner, repo, base, branch, nil)
	if err != nil {
		return false, fmt.Errorf("compare %s...%s: %w", base, branch, err)
	}

	return comparison.GetAheadBy() > 0, nil
}

// RemoteBranchExists reports whether the named branch exists on the
// remote repository. Used by the executor to detect when a user has
// deleted a branch (e.g., after closing a PR) so the pipeline can
// start fresh instead of reusing stale local state.
func (s *GitHubServiceImpl) RemoteBranchExists(owner, repo, branch string) (bool, error) {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return false, fmt.Errorf("get installation ID: %w", err)
	}

	client, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return false, fmt.Errorf("get GitHub client: %w", err)
	}

	_, _, err = client.Git.GetRef(context.Background(), owner, repo, "refs/heads/"+branch)
	if err != nil {
		// go-github returns an error for 404 responses.
		return false, nil
	}

	return true, nil
}
