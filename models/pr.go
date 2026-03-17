package models

import "time"

// PRDetails contains identifying information about a pull request.
// Used to provide PR context in feedback task files and for status
// tracking across the ticket lifecycle.
type PRDetails struct {
	Number     int
	Title      string
	Branch     string
	BaseBranch string
	URL        string
}

// PRComment represents a single comment on a pull request.
// Covers both file-level review comments and general PR comments.
type PRComment struct {
	ID        int64
	Author    Author
	Body      string
	FilePath  string // Empty for general (non-file-specific) comments.
	Line      int    // Zero for general comments.
	Timestamp time.Time
	InReplyTo int64 // Zero if this is not a reply to another comment.
}

// PRParams contains the parameters for creating a new pull request.
type PRParams struct {
	Owner     string
	Repo      string
	Title     string
	Body      string
	Head      string // Source branch.
	Base      string // Target branch.
	Draft     bool
	Labels    []string
	Assignees []string
}

// PRUpdateParams contains the parameters for updating an existing pull
// request. Nil pointer fields are not modified.
type PRUpdateParams struct {
	Title *string
	Body  *string
	State *string
	Draft *bool
}

// PR represents a created or existing pull request.
type PR struct {
	Number int
	URL    string
	State  string
}
