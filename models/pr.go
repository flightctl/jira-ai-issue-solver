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
	HeadSHA    string
	CreatedAt  time.Time
}

// PRComment represents a single comment on a pull request.
// Covers both file-level review comments and general PR comments.
type PRComment struct {
	ID              int64
	Author          Author
	Body            string
	FilePath        string // Empty for general (non-file-specific) comments.
	Line            int    // Zero for general comments.
	URL             string // HTML URL for linking back to the comment.
	Timestamp       time.Time
	InReplyTo       int64 // Zero if this is not a reply to another comment.
	IsReviewComment bool  // True for file-level review comments, false for conversation comments.
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

// PRMergeState holds the mergeability status of a pull request.
// Only available via the single-PR Get endpoint (not the List
// endpoint). Used by the merge scanner to detect unmergeable PRs.
type PRMergeState struct {
	// Mergeable is nil when GitHub is still computing the merge
	// status (async). False indicates merge conflicts exist.
	Mergeable *bool

	// BaseBranch is the target branch the PR merges into.
	BaseBranch string
}

// IssueComment represents a top-level comment on a pull request
// (via the GitHub Issues API, as distinct from review comments).
type IssueComment struct {
	ID   int64
	Body string
}
