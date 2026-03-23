// Package workspace manages ticket-scoped workspace directories.
//
// A workspace is a cloned repository directory that persists across jobs for
// the same ticket, enabling AI-generated artifacts (caches, indexes, analysis
// state) to survive between container invocations. Workspaces are identified
// by ticket key and stored under a configurable base directory using the
// naming convention <base_dir>/<ticket-key>/.
//
// # Cleanup
//
// Cleanup is driven by two mechanisms:
//   - TTL: workspaces older than a configured maximum age are removed
//     regardless of ticket status (prevents unbounded disk growth).
//   - Filter: the caller provides a predicate to remove workspaces for
//     tickets in terminal states (Done, Closed, etc.).
//
// # Artifact persistence and TTL tradeoff
//
// TTL-based cleanup can remove a workspace while its ticket still has an
// active PR awaiting review. If a reviewer leaves feedback after the TTL
// has expired, the feedback pipeline will re-clone the repository and
// check out the existing PR branch (self-healing), but AI-generated
// artifacts from prior sessions (caches, documentation indexes, analysis
// state) will be lost. The AI will proceed without them, potentially
// using more tokens to regenerate context it previously cached.
//
// Teams can increase workspaces.ttl_days to reduce the likelihood of
// this scenario, at the cost of higher disk usage. The right value
// depends on typical PR review turnaround times.
//
// Workspace creation includes cloning the repository via the [Cloner]
// dependency. Post-creation git operations (branch creation, syncing with
// remote) are the caller's responsibility.
package workspace

import "time"

// Cloner abstracts the repository cloning operation needed by the workspace
// manager during workspace creation. The existing GitHubServiceImpl
// satisfies this interface.
type Cloner interface {
	CloneRepository(repoURL, directory string) error
}

// Manager manages the lifecycle of ticket-scoped workspace directories.
type Manager interface {
	// Create clones a repository into a new workspace directory for the
	// given ticket. Returns the workspace path. Returns an error if a
	// workspace already exists for this ticket.
	Create(ticketKey, repoURL string) (string, error)

	// Find returns the workspace path for the given ticket and true if
	// the workspace exists, or empty string and false if it does not.
	Find(ticketKey string) (string, bool)

	// FindOrCreate returns an existing workspace or creates a new one.
	// The bool return value indicates whether an existing workspace was
	// reused (true) or a new one was created (false).
	FindOrCreate(ticketKey, repoURL string) (string, bool, error)

	// Cleanup removes the workspace directory for the given ticket.
	// Returns nil if the workspace does not exist.
	Cleanup(ticketKey string) error

	// CleanupStale removes workspaces whose last modification time is
	// older than maxAge. Returns the number of workspaces removed.
	//
	// Note: this may remove workspaces for tickets with active PRs. The
	// feedback pipeline self-heals by re-cloning, but AI-generated
	// artifacts from prior sessions will be lost. See package
	// documentation for details.
	CleanupStale(maxAge time.Duration) (int, error)

	// CleanupByFilter removes workspaces for which shouldRemove returns
	// true. Returns the number of workspaces removed.
	CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error)

	// List returns information about all workspaces currently on disk.
	List() ([]Info, error)
}

// Info describes an existing workspace on disk.
type Info struct {
	// TicketKey is the ticket identifier derived from the directory name.
	TicketKey string

	// Path is the absolute filesystem path to the workspace directory.
	Path string

	// ModTime is the last modification time of the workspace directory,
	// used for TTL-based cleanup decisions.
	ModTime time.Time
}
