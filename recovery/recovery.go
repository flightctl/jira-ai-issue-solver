// Package recovery implements crash recovery and startup orchestration.
//
// The system uses Jira and GitHub as durable state stores — no separate
// database is needed. On startup, the recovery runner detects and
// resolves interrupted work by querying these systems for inconsistent
// state.
//
// # Startup sequence
//
//  1. Clean orphaned containers left by a prior crash
//  2. Query the issue tracker for tickets stuck in "in progress"
//  3. For each stuck ticket, determine what was interrupted and
//     take the appropriate action (complete transition, create PR,
//     or re-queue for execution)
//  4. Clean up workspaces for tickets in terminal states
//  5. Clean up stale workspaces past TTL
//
// # Consumer-defined interfaces
//
// The recovery package defines narrow interfaces for its dependencies
// ([IssueTracker], [GitService], [WorkspaceCleaner], [ContainerCleaner],
// [JobSubmitter], [ProjectResolver]) rather than importing shared
// interface packages. The underlying implementations satisfy these
// interfaces implicitly.
//
// # Error handling
//
// All recovery errors are logged but non-fatal. The runner makes a
// best-effort attempt to recover each stuck ticket independently.
// A failure recovering one ticket does not prevent recovery of others.
//
// Test doubles are provided in the [recoverytest] subpackage.
package recovery

import (
	"context"
	"time"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Runner executes the startup recovery sequence. Run should be called
// once before scanners start. All errors are handled internally
// (logged, not returned) to ensure recovery is best-effort.
type Runner interface {
	Run(ctx context.Context) error
}

// IssueTracker searches for and updates work items. This is a
// consumer-defined subset of the tracker.IssueTracker interface
// containing only the methods needed for crash recovery.
type IssueTracker interface {
	SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error)
	GetWorkItem(key string) (*models.WorkItem, error)
	TransitionStatus(key, status string) error
	SetFieldValue(key, field, value string) error
	AddComment(key, body string) error
}

// GitService provides the git and GitHub API operations needed for
// crash recovery. This is a consumer-defined interface containing
// only the methods required to detect and resolve interrupted work.
type GitService interface {
	// GetPRForBranch finds the open pull request whose head branch
	// matches the given name. Returns an error if no matching PR exists.
	GetPRForBranch(owner, repo, head string) (*models.PRDetails, error)

	// BranchHasCommits reports whether the branch has commits beyond
	// the base branch (i.e., the AI produced work that was committed).
	BranchHasCommits(owner, repo, branch, base string) (bool, error)

	// CreatePR creates a pull request.
	CreatePR(params models.PRParams) (*models.PR, error)
}

// WorkspaceCleaner manages workspace cleanup operations needed at
// startup. This is a consumer-defined subset of workspace.Manager.
type WorkspaceCleaner interface {
	CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error)
	CleanupStale(maxAge time.Duration) (int, error)
}

// ContainerCleaner removes orphaned containers left by a prior crash.
type ContainerCleaner interface {
	CleanupOrphans(ctx context.Context, prefix string) error
}

// JobSubmitter queues jobs for execution via the job manager.
type JobSubmitter interface {
	Submit(event jobmanager.Event) (*jobmanager.Job, error)
}

// ProjectResolver maps work items to their project-specific settings.
type ProjectResolver interface {
	ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error)
}

// Config holds construction parameters for [StartupRunner].
type Config struct {
	// ContainerPrefix is the naming prefix used for orphaned container
	// detection. Defaults to "ai-bot-" when empty.
	ContainerPrefix string

	// WorkspaceTTL is the maximum age for workspace directories.
	// Workspaces older than this are removed. Zero disables TTL-based
	// cleanup.
	WorkspaceTTL time.Duration

	// BotUsername is used for branch name construction
	// ("{bot-username}/{ticket-key}").
	BotUsername string

	// InProgressCriteria defines the search query for finding tickets
	// stuck in "in progress" status. Typically uses StatusByType to
	// handle projects where different ticket types have different
	// "in progress" status names, combined with ContributorIsCurrentUser
	// to limit results to tickets the bot is contributing to.
	InProgressCriteria models.SearchCriteria

	// ActiveStatuses is the set of all statuses that indicate a ticket
	// may still need processing (todo, in_progress, in_review across
	// all projects and ticket types). Used for workspace cleanup:
	// workspaces for tickets whose status is not in this set are
	// removed.
	ActiveStatuses map[string]bool
}
