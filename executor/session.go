package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"jira-ai-issue-solver/taskfile"
)

// sessionOutputPath is the path, relative to the workspace root,
// where the wrapper script writes AI session metadata.
const sessionOutputPath = ".ai-bot/session-output.json"

// SessionOutput holds the AI session results parsed from
// session-output.json. The wrapper script (run.sh) writes this file
// after the AI CLI exits.
type SessionOutput struct {
	// ExitCode is the AI CLI's exit code.
	ExitCode int `json:"exit_code"`

	// CostUSD is the session cost reported by the AI provider.
	CostUSD float64 `json:"cost_usd"`

	// ValidationPassed indicates whether the AI's own validation
	// succeeded. Nil means unknown (field absent or not reported).
	ValidationPassed *bool `json:"validation_passed"`

	// Summary is a brief description of what the AI did.
	Summary string `json:"summary"`
}

// PRDescription holds the AI-generated PR title and body parsed from
// .ai-bot/pr.md. The first non-empty line is the title; remaining
// lines form the body.
type PRDescription struct {
	Title string
	Body  string
}

// readPRDescription reads the AI-generated PR description from the
// workspace. Returns nil if the file does not exist, is empty, or
// contains only whitespace. The first non-empty line is used as the
// PR title; the rest (after trimming a leading blank line) is the body.
func readPRDescription(dir string) *PRDescription {
	path := filepath.Join(dir, taskfile.PRDescriptionPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
	if err != nil {
		return nil
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}

	// Split into title (first line) and body (rest).
	title, body, _ := strings.Cut(content, "\n")
	title = strings.TrimSpace(title)

	// Strip markdown heading prefix (e.g., "# Title" → "Title").
	// AI models frequently format the first line as a heading.
	title = strings.TrimLeft(title, "# ")

	return &PRDescription{
		Title: title,
		Body:  strings.TrimSpace(body),
	}
}

// CommentResponse maps a PR comment ID to the AI's summary of how it
// was addressed. The AI writes an array of these to
// .ai-bot/comment-responses.json after a feedback session.
type CommentResponse struct {
	CommentID int64  `json:"comment_id"`
	Response  string `json:"response"`
}

// readCommentResponses reads the AI-generated per-comment response
// summaries from the workspace. Returns nil if the file does not exist
// or cannot be parsed. The bot uses these to post descriptive replies
// instead of generic "Addressed in <commit>" messages.
func readCommentResponses(dir string) map[int64]string {
	path := filepath.Join(dir, taskfile.CommentResponsesPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
	if err != nil {
		return nil
	}

	var responses []CommentResponse
	if err := json.Unmarshal(data, &responses); err != nil {
		return nil
	}

	if len(responses) == 0 {
		return nil
	}

	m := make(map[int64]string, len(responses))
	for _, r := range responses {
		if r.CommentID != 0 && r.Response != "" {
			m[r.CommentID] = r.Response
		}
	}

	if len(m) == 0 {
		return nil
	}

	return m
}

// readSessionOutput reads and parses the session output file from the
// workspace. Returns a zero-value SessionOutput if the file does not
// exist or cannot be parsed. Missing files are expected when the
// container is killed by timeout before the wrapper script finishes.
func readSessionOutput(dir string) SessionOutput {
	path := filepath.Join(dir, sessionOutputPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
	if err != nil {
		return SessionOutput{}
	}

	var output SessionOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return SessionOutput{}
	}

	return output
}
