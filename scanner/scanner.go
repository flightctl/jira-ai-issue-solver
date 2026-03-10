// Package scanner implements event-based polling scanners that
// discover work from external systems and emit events to the
// job manager.
//
// # WorkItemScanner
//
// Polls the issue tracker for tickets in "todo" status and emits
// [jobmanager.JobTypeNewTicket] events. The scanner is stateless:
// it queries each cycle and relies on the job manager for
// deduplication.
//
// # FeedbackScanner
//
// Polls the issue tracker for tickets in "in review" status, then
// checks GitHub for new PR comments. Applies bot-loop prevention
// filters (ignored users, known bots, thread depth) via the
// [commentfilter] package before emitting [jobmanager.JobTypeFeedback]
// events.
//
// # Consumer-defined interfaces
//
// The scanner defines narrow interfaces for its dependencies
// ([IssueSearcher], [JobSubmitter], [PRFetcher], [RepoLocator])
// rather than importing shared interface packages. The underlying
// implementations (e.g., Jira adapter, GitHub service) satisfy these
// interfaces implicitly.
//
// Test doubles are provided in the [scannertest] subpackage.
package scanner

import (
	"context"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Scanner manages the lifecycle of a polling scanner.
type Scanner interface {
	// Start begins polling in a background goroutine. The first
	// scan runs immediately. Returns an error if already running.
	// The scanner stops when the context is cancelled or [Stop]
	// is called.
	Start(ctx context.Context) error

	// Stop cancels polling and blocks until the background
	// goroutine exits. Safe to call multiple times or without
	// a prior Start.
	Stop()
}

// IssueSearcher searches for work items in the issue tracker.
type IssueSearcher interface {
	SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error)
}

// JobSubmitter creates jobs from scanner events.
type JobSubmitter interface {
	Submit(event jobmanager.Event) (*jobmanager.Job, error)
}

// PRFetcher retrieves PR details and comments from GitHub.
type PRFetcher interface {
	// GetPRForBranch finds the open pull request whose head
	// branch matches the given name.
	GetPRForBranch(owner, repo, head string) (*models.PRDetails, error)

	// GetPRComments returns all comments on the given pull
	// request.
	GetPRComments(owner, repo string, number int) ([]models.PRComment, error)
}

// RepoLocator maps work items to their GitHub repository coordinates.
type RepoLocator interface {
	// LocateRepo returns the GitHub owner and repo name for the
	// given work item's component-to-repo mapping.
	LocateRepo(workItem models.WorkItem) (owner, repo string, err error)
}
