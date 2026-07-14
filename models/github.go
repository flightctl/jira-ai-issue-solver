package models

import (
	"encoding/json"
	"time"
)

// GitHubUser represents a GitHub user.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
}

// GitHubCreatePRRequest is the raw JSON payload sent to the GitHub REST API
// when creating a pull request. Used internally by GitHubServiceImpl.
type GitHubCreatePRRequest struct {
	Title               string   `json:"title"`
	Body                string   `json:"body"`
	Head                string   `json:"head"`
	Base                string   `json:"base"`
	Labels              []string `json:"labels,omitempty"`
	MaintainerCanModify *bool    `json:"maintainer_can_modify,omitempty"`
}

// GitHubPRComment represents a raw PR comment from the GitHub REST API.
type GitHubPRComment struct {
	ID          int64      `json:"id"`
	InReplyToID int64      `json:"in_reply_to_id,omitempty"` // ID of comment this is replying to (for threaded replies)
	User        GitHubUser `json:"user"`
	Body        string     `json:"body"`
	Path        string     `json:"path"`
	Line        int        `json:"line"`       // Last line of range for multi-line comments
	StartLine   int        `json:"start_line"` // First line of range for multi-line comments (0 if single line)
	Side        string     `json:"side"`       // Which side of diff: "LEFT" or "RIGHT"
	StartSide   string     `json:"start_side"` // Which side of diff for start line
	HTMLURL     string     `json:"html_url"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// GitHub Git Data API structures for creating verified commits

// GitHubBlobRequest represents a request to create a blob
type GitHubBlobRequest struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"` // "utf-8" or "base64"
}

// GitHubBlobResponse represents the response from creating a blob
type GitHubBlobResponse struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}

// GitHubTreeEntry represents a single entry in a tree.
// For new/modified files, set Content with the file's text (GitHub
// creates the blob server-side). For deletions, set SHA to nil.
// For binary files that can't be represented as UTF-8, pre-create
// a blob and set SHA instead. Content and SHA are mutually exclusive
// per the GitHub API contract.
type GitHubTreeEntry struct {
	Path    string  // File path in the tree
	Mode    string  // "100644" for file, "100755" for executable
	Type    string  // "blob", "tree", "commit"
	SHA     *string // SHA of the blob, or nil to delete
	Content *string // Inline file content; GitHub creates the blob automatically
}

// MarshalJSON serializes a tree entry, omitting SHA when Content is
// set. The GitHub API rejects requests that include both fields.
func (e GitHubTreeEntry) MarshalJSON() ([]byte, error) {
	if e.Content != nil {
		return json.Marshal(struct {
			Path    string  `json:"path"`
			Mode    string  `json:"mode,omitempty"`
			Type    string  `json:"type,omitempty"`
			Content *string `json:"content"`
		}{e.Path, e.Mode, e.Type, e.Content})
	}
	return json.Marshal(struct {
		Path string  `json:"path"`
		Mode string  `json:"mode,omitempty"`
		Type string  `json:"type,omitempty"`
		SHA  *string `json:"sha"`
	}{e.Path, e.Mode, e.Type, e.SHA})
}

// GitHubTreeRequest represents a request to create a tree
type GitHubTreeRequest struct {
	BaseTree string            `json:"base_tree,omitempty"` // SHA of the tree to update (optional)
	Tree     []GitHubTreeEntry `json:"tree"`
}

// GitHubTreeResponse represents the response from creating a tree
type GitHubTreeResponse struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}

// GitHubCommitAuthor represents the author/committer of a commit
type GitHubCommitAuthor struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date,omitempty"`
}

// GitHubCommitRequest represents a request to create a commit
type GitHubCommitRequest struct {
	Message   string              `json:"message"`
	Tree      string              `json:"tree"`    // SHA of the tree
	Parents   []string            `json:"parents"` // Array of parent commit SHAs
	Author    *GitHubCommitAuthor `json:"author,omitempty"`
	Committer *GitHubCommitAuthor `json:"committer,omitempty"`
}

// GitHubCommitResponse represents the response from creating a commit
type GitHubCommitResponse struct {
	SHA     string `json:"sha"`
	URL     string `json:"url"`
	Message string `json:"message"`
}

// GitHubCreateReferenceRequest represents a request to create a new reference
type GitHubCreateReferenceRequest struct {
	Ref string `json:"ref"` // Full reference name (e.g., "refs/heads/branch-name")
	SHA string `json:"sha"` // SHA of the commit to point to
}

// GitHubReferenceRequest represents a request to update a reference
type GitHubReferenceRequest struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force,omitempty"`
}

// GitHubGetReferenceResponse represents the response from getting a reference
type GitHubGetReferenceResponse struct {
	Ref    string `json:"ref"`
	NodeID string `json:"node_id"`
	URL    string `json:"url"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
		URL  string `json:"url"`
	} `json:"object"`
}
