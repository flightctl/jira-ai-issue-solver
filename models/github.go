package models

import "time"

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

// GitHubTreeEntry represents a single entry in a tree
type GitHubTreeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode,omitempty"` // "100644" for file, "100755" for executable, "040000" for subdirectory, "160000" for submodule, "120000" for symlink
	Type string  `json:"type,omitempty"` // "blob", "tree", "commit"
	SHA  *string `json:"sha"`            // SHA of the blob or tree, or nil to delete the file
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
