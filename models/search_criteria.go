package models

import "errors"

// SearchCriteria defines the parameters for searching work items across any
// issue tracker. Each IssueTracker adapter translates these fields into the
// tracker's native query language (e.g., JQL for Jira).
//
// All non-empty fields are combined with AND logic. Within each field,
// multiple values are combined with OR logic.
type SearchCriteria struct {
	// ProjectKeys limits results to work items in the specified projects.
	ProjectKeys []string

	// StatusByType maps work item types to acceptable statuses. This
	// supports trackers where different types have different workflow
	// statuses (e.g., Bug "todo" is "Open" while Story "todo" is "To Do").
	//
	// Conditions across types are OR'd:
	//   (type=Bug AND status IN [Open]) OR (type=Story AND status IN [To Do])
	//
	// Mutually exclusive with Statuses.
	StatusByType map[string][]string

	// Statuses filters by status independent of work item type. Use this
	// for queries where the status applies uniformly regardless of type
	// (e.g., finding all "In Progress" items for crash recovery).
	//
	// Mutually exclusive with StatusByType.
	Statuses []string

	// ContributorIsCurrentUser restricts results to work items where the
	// authenticated user is listed as a contributor. In Jira this maps to
	// "Contributors = currentUser()". This is how the bot identifies
	// tickets it should work on without being the assignee.
	ContributorIsCurrentUser bool

	// Labels filters by applied labels. Multiple labels are OR'd.
	Labels []string

	// OrderBy specifies the sort order (e.g., "updated DESC").
	OrderBy string
}

// Validate checks that the SearchCriteria fields are internally consistent.
// Returns an error if StatusByType and Statuses are both set, since combining
// type-specific and uniform status filters produces confusing results.
func (c SearchCriteria) Validate() error {
	if len(c.StatusByType) > 0 && len(c.Statuses) > 0 {
		return errors.New("SearchCriteria: StatusByType and Statuses are mutually exclusive")
	}
	return nil
}
