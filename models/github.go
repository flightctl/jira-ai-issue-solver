package models

import "time"

// GitHubWebhook represents the webhook payload from GitHub
type GitHubWebhook struct {
	Action      string            `json:"action"`
	PullRequest GitHubPullRequest `json:"pull_request"`
	Repository  GitHubRepository  `json:"repository"`
	Sender      GitHubUser        `json:"sender"`
	Review      GitHubReview      `json:"review,omitempty"`
}

// GitHubPullRequest represents a GitHub pull request
type GitHubPullRequest struct {
	ID        int64      `json:"id"`
	Number    int        `json:"number"`
	State     string     `json:"state"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	HTMLURL   string     `json:"html_url"`
	User      GitHubUser `json:"user"`
	Head      GitHubRef  `json:"head"`
	Base      GitHubRef  `json:"base"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// GitHubRepository represents a GitHub repository
type GitHubRepository struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	FullName string     `json:"full_name"`
	Owner    GitHubUser `json:"owner"`
	HTMLURL  string     `json:"html_url"`
	CloneURL string     `json:"clone_url"`
	SSHURL   string     `json:"ssh_url"`
}

// GitHubUser represents a GitHub user
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
}

// GitHubRef represents a Git reference in a GitHub pull request
type GitHubRef struct {
	Label string           `json:"label"`
	Ref   string           `json:"ref"`
	SHA   string           `json:"sha"`
	Repo  GitHubRepository `json:"repo"`
}

// GitHubReview represents a GitHub pull request review
type GitHubReview struct {
	ID          int64      `json:"id"`
	User        GitHubUser `json:"user"`
	Body        string     `json:"body"`
	State       string     `json:"state"`
	HTMLURL     string     `json:"html_url"`
	SubmittedAt time.Time  `json:"submitted_at"`
}

// GitHubCreatePRRequest represents the request to create a pull request
type GitHubCreatePRRequest struct {
	Title               string   `json:"title"`
	Body                string   `json:"body"`
	Head                string   `json:"head"`
	Base                string   `json:"base"`
	Labels              []string `json:"labels,omitempty"`
	MaintainerCanModify *bool    `json:"maintainer_can_modify,omitempty"`
}

// GitHubCreatePRResponse represents the response from creating a pull request
type GitHubCreatePRResponse struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	State     string    `json:"state"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GitHubPRComment represents a PR comment
// (moved from pr_review_processor.go)
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

// GitHubPRDetails represents detailed PR information including reviews
type GitHubPRDetails struct {
	Number    int               `json:"number"`
	State     string            `json:"state"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	HTMLURL   string            `json:"html_url"`
	Head      GitHubRef         `json:"head"`
	Base      GitHubRef         `json:"base"`
	Reviews   []GitHubReview    `json:"reviews,omitempty"`
	Comments  []GitHubPRComment `json:"-"` // We'll populate this separately
	Files     []GitHubPRFile    `json:"files,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// GitHubPRFile represents a file changed in a PR
type GitHubPRFile struct {
	SHA       string `json:"sha"`
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch"`
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

// GitHubReferenceResponse represents the response from updating a reference
type GitHubReferenceResponse struct {
	Ref    string `json:"ref"`
	NodeID string `json:"node_id"`
	URL    string `json:"url"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
		URL  string `json:"url"`
	} `json:"object"`
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
