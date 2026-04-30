package taskfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"jira-ai-issue-solver/models"
)

// Compile-time check that MarkdownWriter implements Writer.
var _ Writer = (*MarkdownWriter)(nil)

// MarkdownWriter generates task files as structured markdown documents.
// It has no state or dependencies; formatting is deterministic and file
// writing uses standard filesystem operations.
type MarkdownWriter struct{}

// NewMarkdownWriter creates a MarkdownWriter.
func NewMarkdownWriter() *MarkdownWriter {
	return &MarkdownWriter{}
}

func (w *MarkdownWriter) WriteIssue(workItem models.WorkItem, dir string, attachmentFiles []string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n", workItem.Key, workItem.Summary)

	if workItem.Description != "" {
		b.WriteString("\n## Description\n")
		writeBlockquote(&b, "Ticket description", workItem.Description)
	}

	if len(attachmentFiles) > 0 {
		b.WriteString("\n## Attachments\n")
		fmt.Fprintf(&b, "The following files are available in `%s/`:\n", AttachmentsDirPath)
		for _, f := range attachmentFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}

	return writeFile(dir, IssueFilePath, b.String())
}

func (w *MarkdownWriter) WriteNewTicketTask(workItem models.WorkItem, dir, overrideInstructions, overrideWorkflow string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# Task: %s\n\n", workItem.Key)
	fmt.Fprintf(&b, "## Summary\n%s\n\n", workItem.Summary)
	fmt.Fprintf(&b, "The full ticket description is in `%s`.\n\n", IssueFilePath)

	writeNewTicketInstructions(&b, workItem.HasSecurityLevel())

	if err := appendInstructions(&b, dir, overrideInstructions); err != nil {
		return err
	}

	if err := appendWorkflow(&b, dir, overrideWorkflow); err != nil {
		return err
	}

	return writeTaskFile(dir, b.String())
}

func (w *MarkdownWriter) WriteFeedbackTask(
	prDetails models.PRDetails,
	newComments, addressedComments []models.PRComment,
	dir, overrideInstructions, overrideWorkflow string,
) error {
	var b strings.Builder

	b.WriteString("# Task: Address PR Review Feedback\n\n")
	fmt.Fprintf(&b, "The original ticket is described in `%s`.\n\n", IssueFilePath)

	fmt.Fprintf(&b, "## PR Context\n")
	fmt.Fprintf(&b, "PR #%d: %s\n", prDetails.Number, prDetails.Title)
	fmt.Fprintf(&b, "Branch: %s\n\n", prDetails.Branch)

	if len(newComments) > 0 {
		b.WriteString("## Review Comments\n\n")
		writeGroupedComments(&b, newComments)
	}

	if len(addressedComments) > 0 {
		b.WriteString("## Previously Addressed Comments (Context Only)\n\n")
		writeGroupedComments(&b, addressedComments)
	}

	writeFeedbackInstructions(&b)

	if err := appendInstructions(&b, dir, overrideInstructions); err != nil {
		return err
	}

	if err := appendFeedbackWorkflow(&b, dir, overrideWorkflow); err != nil {
		return err
	}

	return writeTaskFile(dir, b.String())
}

func (w *MarkdownWriter) WriteMultiRepoNewTicketTask(workItem models.WorkItem, wsDir string, repos []RepoContext) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# Task: %s\n\n", workItem.Key)
	fmt.Fprintf(&b, "## Summary\n%s\n\n", workItem.Summary)
	fmt.Fprintf(&b, "The full ticket description is in `%s`.\n\n", IssueFilePath)

	writeNewTicketInstructions(&b, workItem.HasSecurityLevel())

	for _, repo := range repos {
		fmt.Fprintf(&b, "\n## Repository: %s\n", repo.Name)
		if err := appendInstructions(&b, repo.Dir, repo.OverrideInstructions); err != nil {
			return err
		}
		if err := appendWorkflow(&b, repo.Dir, repo.OverrideNewTicketWorkflow); err != nil {
			return err
		}
	}

	return writeFile(wsDir, TaskFilePath, b.String())
}

func (w *MarkdownWriter) WriteMultiRepoFeedbackTask(
	prDetails models.PRDetails,
	newComments, addressedComments []models.PRComment,
	wsDir string, repos []RepoContext,
) error {
	var b strings.Builder

	b.WriteString("# Task: Address PR Review Feedback\n\n")
	fmt.Fprintf(&b, "The original ticket is described in `%s`.\n\n", IssueFilePath)

	fmt.Fprintf(&b, "## PR Context\n")
	fmt.Fprintf(&b, "PR #%d: %s\n", prDetails.Number, prDetails.Title)
	fmt.Fprintf(&b, "Branch: %s\n\n", prDetails.Branch)

	if len(newComments) > 0 {
		b.WriteString("## Review Comments\n\n")
		writeGroupedComments(&b, newComments)
	}

	if len(addressedComments) > 0 {
		b.WriteString("## Previously Addressed Comments (Context Only)\n\n")
		writeGroupedComments(&b, addressedComments)
	}

	writeFeedbackInstructions(&b)

	for _, repo := range repos {
		fmt.Fprintf(&b, "\n## Repository: %s\n", repo.Name)
		if err := appendInstructions(&b, repo.Dir, repo.OverrideInstructions); err != nil {
			return err
		}
		if err := appendFeedbackWorkflow(&b, repo.Dir, repo.OverrideFeedbackWorkflow); err != nil {
			return err
		}
	}

	return writeFile(wsDir, TaskFilePath, b.String())
}

// writeNewTicketInstructions writes the standard instructions section
// for a new ticket task file.
func writeNewTicketInstructions(b *strings.Builder, hasSecurityLevel bool) {
	b.WriteString("## Instructions\n")
	b.WriteString("Implement this task. Validate your changes compile and pass tests using\n")
	b.WriteString("whatever build tools this project provides. Fix any issues you find.\n")
	b.WriteString("Do not push to git -- the system handles that.\n")

	if hasSecurityLevel {
		b.WriteString("\nThis ticket has a security level set. Do not include specific\n")
		b.WriteString("vulnerability details in commit messages, code comments, or any\n")
		b.WriteString("content that may appear in the public pull request.\n")
	}
}

// writeFeedbackInstructions writes the standard instructions section
// for a feedback task file.
func writeFeedbackInstructions(b *strings.Builder) {
	b.WriteString("## Instructions\n")
	fmt.Fprintf(b, "If `%s` exists, read it first — it contains context\n", SessionContextPath)
	b.WriteString("from the session that created this PR (design decisions, rationale,\n")
	b.WriteString("test strategy) that may be relevant when addressing feedback.\n\n")
	b.WriteString("Address each review comment listed above. Validate your changes compile\n")
	b.WriteString("and pass tests. Do not push to git -- the system handles that.\n")

	b.WriteString("\n## Required Output\n")
	fmt.Fprintf(b, "Write a JSON file to `%s` mapping each comment to a\n", CommentResponsesPath)
	b.WriteString("brief summary of what you did (or chose not to do). Use the comment_id\n")
	b.WriteString("from each review comment header. Format:\n\n")
	b.WriteString("```json\n")
	b.WriteString("[\n")
	b.WriteString("  {\"comment_id\": 123, \"response\": \"Switched to Optional pattern as suggested.\"},\n")
	b.WriteString("  {\"comment_id\": 456, \"response\": \"Kept the fallback path — needed for v1 compat.\"}\n")
	b.WriteString("]\n")
	b.WriteString("```\n")
}

// appendInstructions appends a "Project Instructions" section. The
// override string (from the project config profile) takes precedence,
// enabling rapid prototyping without committing to the source repo.
// If no override is set, .ai-bot/instructions.md from the workspace
// is used. If both are empty, nothing is appended.
func appendInstructions(b *strings.Builder, dir, override string) error {
	content := strings.TrimSpace(override)

	if content == "" {
		path := filepath.Join(dir, InstructionsPath)

		data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read instructions file: %w", err)
		}

		content = strings.TrimSpace(string(data))
	}

	if content == "" {
		return nil
	}

	b.WriteString("\n## Project Instructions\n")
	b.WriteString(content)
	b.WriteString("\n")

	return nil
}

// appendWorkflow appends a "Workflow" section. The override string
// (from the project config profile) takes precedence. If no override
// is set, .ai-bot/new-ticket-workflow.md from the workspace is used.
// If both are empty, nothing is appended. Only called for new-ticket
// task files — feedback tasks use appendFeedbackWorkflow.
func appendWorkflow(b *strings.Builder, dir, override string) error {
	content := strings.TrimSpace(override)

	if content == "" {
		path := filepath.Join(dir, NewTicketWorkflowPath)

		data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read workflow file: %w", err)
		}

		content = strings.TrimSpace(string(data))
	}

	if content == "" {
		return nil
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString(content)
	b.WriteString("\n")

	return nil
}

// appendFeedbackWorkflow appends a "Workflow" section for feedback
// tasks. The override string (from the project config profile) takes
// precedence. If no override is set, .ai-bot/feedback-workflow.md
// from the workspace is used. If both are empty, nothing is appended.
// Only called for feedback task files — new-ticket tasks use
// appendWorkflow.
func appendFeedbackWorkflow(b *strings.Builder, dir, override string) error {
	content := strings.TrimSpace(override)

	if content == "" {
		path := filepath.Join(dir, FeedbackWorkflowPath)

		data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read feedback workflow file: %w", err)
		}

		content = strings.TrimSpace(string(data))
	}

	if content == "" {
		return nil
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString(content)
	b.WriteString("\n")

	return nil
}

// writeBlockquote writes content as a markdown blockquote with a label.
// Each line of content is prefixed with "> ". Empty lines become bare
// ">" to maintain blockquote continuity.
func writeBlockquote(b *strings.Builder, label, content string) {
	fmt.Fprintf(b, "> [%s]\n", label)
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if line == "" {
			b.WriteString(">\n")
		} else {
			fmt.Fprintf(b, "> %s\n", line)
		}
	}
}

// writeGroupedComments writes comments grouped by file path. File-
// specific comments are sorted alphabetically by path; general comments
// (empty file path) appear last.
func writeGroupedComments(b *strings.Builder, comments []models.PRComment) {
	grouped := groupByFile(comments)
	paths := sortedFilePaths(grouped)

	for _, path := range paths {
		if path == "" {
			b.WriteString("### General\n")
		} else {
			fmt.Fprintf(b, "### File: %s\n", path)
		}
		for _, c := range grouped[path] {
			writeCommentBlockquote(b, c)
		}
		b.WriteString("\n")
	}
}

// writeCommentBlockquote writes a single PR comment as a blockquote
// with author attribution and comment ID.
func writeCommentBlockquote(b *strings.Builder, c models.PRComment) {
	if c.Line > 0 {
		fmt.Fprintf(b, "> [@%s, line %d, comment_id %d]\n", c.Author.Username, c.Line, c.ID)
	} else {
		fmt.Fprintf(b, "> [@%s, comment_id %d]\n", c.Author.Username, c.ID)
	}
	for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		if line == "" {
			b.WriteString(">\n")
		} else {
			fmt.Fprintf(b, "> %s\n", line)
		}
	}
}

// groupByFile partitions comments by their FilePath. Insertion order
// within each group is preserved (comments appear in the order provided
// by the caller, typically chronological).
func groupByFile(comments []models.PRComment) map[string][]models.PRComment {
	grouped := make(map[string][]models.PRComment)
	for _, c := range comments {
		grouped[c.FilePath] = append(grouped[c.FilePath], c)
	}
	return grouped
}

// sortedFilePaths returns the file paths from grouped comments in
// sorted order, with the empty string (general comments) last.
func sortedFilePaths(grouped map[string][]models.PRComment) []string {
	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i] == "" {
			return false
		}
		if paths[j] == "" {
			return true
		}
		return paths[i] < paths[j]
	})
	return paths
}

// writeFile writes content to <dir>/<relPath>, creating parent
// directories as needed.
func writeFile(dir, relPath, content string) error {
	path := filepath.Join(dir, relPath)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", relPath, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { // #nosec G306
		return fmt.Errorf("write %s: %w", relPath, err)
	}

	return nil
}

// writeTaskFile writes content to <dir>/.ai-bot/task.md.
func writeTaskFile(dir, content string) error {
	return writeFile(dir, TaskFilePath, content)
}
