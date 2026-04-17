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
// contains only whitespace.
//
// The parser tries several strategies to extract the PR title:
//  1. A labeled line like "**Title:** Fix the thing"
//  2. A "## Title" heading followed by the title on the next line
//  3. Fallback: the first non-empty line (strips heading prefixes)
//
// The body is everything after the title, excluding metadata-only
// lines (generic headings, ticket references).
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

	title, body := parsePRContent(content)

	if title == "" && body == "" {
		return nil
	}

	return &PRDescription{
		Title: title,
		Body:  body,
	}
}

// parsePRContent extracts a PR title and body from AI-generated
// markdown. It tries structured formats first (labeled title, heading
// section), then falls back to using the first non-empty line.
func parsePRContent(content string) (title, body string) {
	lines := strings.Split(content, "\n")

	// Strategy 1: labeled title ("**Title:** ..." or "Title: ...")
	// Only scan the header region — a "Title:" match deep in the body
	// would extract the wrong text.
	for i := range min(len(lines), 10) {
		if t, ok := extractLabeledTitle(lines[i]); ok {
			return t, buildBodyExcluding(lines, i)
		}
	}

	// Strategy 2: "## Title" heading with value on next non-empty line
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "## title") {
			for j := i + 1; j < len(lines); j++ {
				if t := strings.TrimSpace(lines[j]); t != "" {
					return cleanPRTitle(t), buildBodyExcluding(lines, i, j)
				}
			}
		}
	}

	// Fallback: first non-empty line is the title, rest is body.
	first, rest, _ := strings.Cut(content, "\n")
	cleaned := cleanPRTitle(strings.TrimSpace(first))
	if isGenericHeading(cleaned) {
		return "", content
	}
	return cleaned, strings.TrimSpace(rest)
}

// isGenericHeading returns true for section headings that are not
// meaningful PR titles (e.g., "Summary", "Description"). When the AI
// writes pr.md without a title line, the first line is typically one
// of these headings.
func isGenericHeading(title string) bool {
	switch strings.ToLower(title) {
	case "summary", "description", "overview", "details",
		"test plan", "testing", "changes", "background",
		"context", "problem", "solution", "notes":
		return true
	}
	return false
}

// extractLabeledTitle checks if a line contains a "Title:" label
// (optionally bold-wrapped) and returns the extracted title value.
func extractLabeledTitle(line string) (string, bool) {
	s := strings.TrimSpace(line)
	s = strings.ReplaceAll(s, "**", "")
	s = strings.TrimSpace(s)

	if !strings.HasPrefix(strings.ToLower(s), "title:") {
		return "", false
	}

	value := strings.TrimSpace(s[len("title:"):])
	if value == "" {
		return "", false
	}
	return value, true
}

// cleanPRTitle strips markdown formatting (heading prefixes, bold
// markers) from a PR title string.
func cleanPRTitle(s string) string {
	s = strings.TrimLeft(s, "# ")
	s = strings.ReplaceAll(s, "**", "")
	return strings.TrimSpace(s)
}

// buildBodyExcluding reconstructs the PR body from all lines except
// those at the given indices and lines that are document metadata
// (generic headings, ticket references) rather than content.
func buildBodyExcluding(lines []string, exclude ...int) string {
	skip := make(map[int]bool, len(exclude))
	for _, idx := range exclude {
		skip[idx] = true
	}

	var bodyLines []string
	for i, line := range lines {
		if skip[i] {
			continue
		}
		if isPRMetadataLine(strings.TrimSpace(line)) {
			continue
		}
		bodyLines = append(bodyLines, line)
	}

	return strings.TrimSpace(strings.Join(bodyLines, "\n"))
}

// isPRMetadataLine returns true for lines that are document metadata
// rather than PR body content.
func isPRMetadataLine(line string) bool {
	lower := strings.ToLower(line)
	if lower == "# pr description" {
		return true
	}
	cleaned := strings.ReplaceAll(lower, "**", "")
	cleaned = strings.TrimSpace(cleaned)
	return strings.HasPrefix(cleaned, "jira ticket:")
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
