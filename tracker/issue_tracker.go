// Package tracker defines the IssueTracker interface, the tracker-agnostic
// abstraction for interacting with issue tracking systems.
//
// The interface decouples the system from any specific tracker by expressing
// operations in terms of generic domain types (WorkItem, SearchCriteria)
// defined in the models package.
//
// Implementations:
//   - Jira: [tracker/jira.Adapter] (wraps the existing JiraService)
//   - GitHub Issues: planned
//   - GitLab Issues: planned
package tracker

import "jira-ai-issue-solver/models"

// IssueTracker provides operations for interacting with an issue tracking
// system. Implementations translate these operations into the tracker's
// native API calls.
//
// The interface is intentionally small — it covers only the operations the
// system actually needs. Operations like creating issues or querying
// changelogs are out of scope.
type IssueTracker interface {
	// SearchWorkItems finds work items matching the given criteria.
	// Returns an empty slice (not nil) when no results match.
	SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error)

	// GetWorkItem retrieves a single work item by its key.
	// Returns the complete work item including security level information.
	GetWorkItem(key string) (*models.WorkItem, error)

	// TransitionStatus moves a work item to the specified status.
	// The status parameter is the target status name as it appears in the
	// tracker's workflow (e.g., "In Progress"), not an abstract role.
	TransitionStatus(key, status string) error

	// AddComment posts a comment to a work item.
	AddComment(key, body string) error

	// SetFieldValue writes a string value to a named field on a work item.
	SetFieldValue(key, field, value string) error

	// DownloadAttachment fetches the raw content of an attachment by its
	// tracker-specific download URL. Returns the file bytes.
	DownloadAttachment(url string) ([]byte, error)
}
