// Package executor implements the job execution pipeline that
// processes work items from the job manager.
//
// The executor handles plumbing: workspace preparation, container
// lifecycle, task file creation, committing, PR management, and
// ticket status transitions. The AI agent handles thinking: code
// generation, validation, and fixing.
//
// # Consumer-defined interfaces
//
// The executor defines narrow interfaces for its dependencies
// ([GitService], [ProjectResolver]) rather than importing a shared
// interface package. This follows the Go convention of consumer-
// defined interfaces. Each consumer declares only the methods it
// requires; the underlying implementation satisfies all consumers.
// See docs/architecture.md for rationale.
//
// # Integration with JobManager
//
// The [Pipeline.Execute] method matches the [jobmanager.ExecuteFunc]
// signature, allowing direct injection into the Coordinator:
//
//	pipeline, _ := executor.NewPipeline(cfg, ...)
//	coordinator, _ := jobmanager.NewCoordinator(jmCfg, pipeline.Execute, logger)
//
// # Pipeline steps (new ticket)
//
//  1. Fetch work item details from the issue tracker
//  2. Resolve project-specific settings (repo, statuses, etc.)
//  3. Transition ticket to "in progress"
//  4. Prepare workspace (clone or reuse)
//  5. Create or switch to ticket branch
//  6. Write task file for the AI agent
//  7. Write provider-specific wrapper script
//  8. Load repo-level configuration hints
//  9. Resolve and start dev container
//  10. Execute AI agent inside container
//  11. Check for changes; fail if none
//  12. Commit changes via GitHub API
//  13. Sync workspace with remote
//  14. Create pull request (draft if validation failed)
//  15. Update ticket with PR URL and transition status
//  16. Stop container (workspace retained for future jobs)
//
// # Pipeline steps (PR feedback)
//
//  1. Fetch work item and resolve project settings
//  2. Find open PR by branch name
//  3. Find or recreate workspace (self-healing if cleaned up)
//  4. Switch to PR branch and sync with remote
//  5. Fetch and categorize PR comments (new vs addressed)
//  6. Write feedback task file for the AI agent
//  7. Resolve container, execute AI, check for changes
//  8. Commit changes via GitHub API
//  9. Sync workspace with remote
//  10. Reply to addressed PR comments
//  11. Stop container (workspace retained)
//
// Key differences from the new-ticket pipeline: the feedback pipeline
// does not transition ticket status, does not create branches or PRs,
// reuses the existing workspace and PR branch, and replies to review
// comments after committing.
//
// Test doubles are provided in the [executortest] subpackage.
package executor

import (
	"context"
	"time"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Executor runs jobs to completion. The Execute method matches
// [jobmanager.ExecuteFunc] for direct injection into the Coordinator.
type Executor interface {
	Execute(ctx context.Context, job *jobmanager.Job) (jobmanager.JobResult, error)
}

// GitService defines the git and GitHub API operations needed by the
// execution pipelines. This is a consumer-defined interface containing
// the methods required by both the new-ticket and feedback pipelines.
//
// The underlying implementation (e.g., services.GitHubServiceImpl)
// satisfies this interface.
type GitService interface {
	// CreateBranch creates a new git branch in the workspace and
	// switches to it.
	CreateBranch(dir, name string) error

	// SwitchBranch switches to an existing branch. Used when a
	// workspace is reused on retry or when checking out a PR branch
	// for feedback processing.
	SwitchBranch(dir, name string) error

	// RemoteBranchExists reports whether the named branch exists on
	// the remote repository. Used to detect when a user has deleted
	// a branch so the pipeline can start fresh.
	RemoteBranchExists(owner, repo, branch string) (bool, error)

	// HasChanges reports whether the workspace has uncommitted
	// changes (modified, added, or deleted tracked files).
	HasChanges(dir string) (bool, error)

	// CommitChanges creates a verified commit via the GitHub API
	// from local workspace changes. Returns the commit SHA.
	// Returns services.ErrNoChanges if all changes are bot
	// artifacts and there is nothing to commit. importExcludes
	// lists additional directories (from import config) to exclude
	// from commits beyond the built-in .ai-bot/ exclusion.
	CommitChanges(owner, repo, branch, message, dir string,
		coAuthor *models.Author, importExcludes []string) (string, error)

	// StripRemoteAuth removes authentication credentials from the
	// workspace's origin remote URL, preventing push operations.
	// Used before handing control to the AI agent.
	StripRemoteAuth(dir string) error

	// RestoreRemoteAuth restores authentication credentials on the
	// workspace's origin remote URL using a fresh token. Must be
	// called after AI execution, before any operation that needs
	// remote access (e.g., SyncWithRemote).
	RestoreRemoteAuth(dir, owner, repo string) error

	// FetchRemote fetches all refs from the origin remote. Used in
	// fork-based workflows to fetch fork branches into a workspace
	// that was cloned from upstream.
	FetchRemote(dir string) error

	// SyncWithRemote reconciles the local workspace with the remote
	// branch after an API-created commit. importExcludes lists
	// additional directories to preserve across the hard reset.
	SyncWithRemote(dir, branch string, importExcludes []string) error

	// CreatePR creates a pull request.
	CreatePR(params models.PRParams) (*models.PR, error)

	// GetPRForBranch finds the open pull request whose head branch
	// matches the given branch name. Returns an error if no matching
	// PR exists.
	GetPRForBranch(owner, repo, head string) (*models.PRDetails, error)

	// GetPRComments returns comments on the given pull request.
	// If since is the zero time, all comments are returned.
	GetPRComments(owner, repo string, number int,
		since time.Time) ([]models.PRComment, error)

	// ReplyToComment posts a threaded reply to a PR review comment.
	ReplyToComment(owner, repo string, prNumber int,
		commentID int64, body string) error

	// PostIssueComment posts a top-level comment on a PR (via the
	// issues endpoint). Used for replying to conversation comments,
	// which do not support threading.
	PostIssueComment(owner, repo string, prNumber int,
		body string) error

	// CloneImport clones an auxiliary repository into destDir. If ref
	// is non-empty, that branch/tag/commit is checked out after
	// cloning. Used to make shared resources (workflow skills,
	// scripts) available in the workspace before AI execution.
	CloneImport(url, destDir, ref string) error
}

// ProjectResolver maps work items to their project-specific settings.
// The implementation bridges between the bot's configuration model
// and the executor's needs.
type ProjectResolver interface {
	// ResolveProject returns the project-specific settings for the
	// given work item. Returns an error if the work item cannot be
	// mapped to a known project or repository.
	ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error)
}

// Config holds construction parameters for [Pipeline].
type Config struct {
	// BotUsername is used for branch naming
	// ("{bot-username}/{ticket-key}").
	BotUsername string

	// DefaultProvider is the AI provider used when the project
	// doesn't specify one (e.g., "claude", "gemini").
	DefaultProvider string

	// AIAPIKeys maps provider names to API key values injected
	// into the container environment (e.g., {"claude": "sk-..."}).
	AIAPIKeys map[string]string

	// SessionTimeout is the maximum duration for an AI session
	// inside the container. Zero means no explicit timeout (only
	// the parent context controls cancellation).
	SessionTimeout time.Duration

	// IgnoredUsernames lists users whose PR comments are excluded
	// from feedback processing entirely (e.g., CI bots).
	IgnoredUsernames []string

	// KnownBotUsernames lists other bots for loop prevention.
	// Their replies to our bot's comments are excluded.
	KnownBotUsernames []string

	// MaxThreadDepth limits how many times our bot can appear in
	// a comment thread's ancestry before further comments in that
	// thread are excluded. Zero or negative disables the limit.
	MaxThreadDepth int

	// DefaultGeminiModel is the Gemini model to use when the
	// repo-level config doesn't specify one (e.g., "gemini-2.5-pro").
	DefaultGeminiModel string
}
