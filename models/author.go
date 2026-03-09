package models

// Author represents a person involved with a work item or code change.
// Used for work item assignees, PR comment authors, and commit co-author
// attribution. The fields are intentionally tracker-agnostic — each
// IssueTracker adapter maps its native user representation to this type.
type Author struct {
	// Name is the display name (e.g., "Jane Doe").
	Name string

	// Email is the email address.
	Email string

	// Username is the tracker-specific login (e.g., Jira username, GitHub login).
	Username string
}
