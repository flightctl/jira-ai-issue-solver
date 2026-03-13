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

func (w *MarkdownWriter) WriteNewTicketTask(workItem models.WorkItem, dir, fallbackInstructions string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# Task: %s\n\n", workItem.Key)
	fmt.Fprintf(&b, "## Summary\n%s\n\n", workItem.Summary)

	if workItem.Description != "" {
		b.WriteString("## Description\n")
		writeBlockquote(&b, "Ticket description", workItem.Description)
		b.WriteString("\n")
	}

	writeNewTicketInstructions(&b, workItem.HasSecurityLevel())

	if err := appendInstructions(&b, dir, fallbackInstructions); err != nil {
		return err
	}

	return writeTaskFile(dir, b.String())
}

func (w *MarkdownWriter) WriteFeedbackTask(
	prDetails models.PRDetails,
	newComments, addressedComments []models.PRComment,
	dir, fallbackInstructions string,
) error {
	var b strings.Builder

	b.WriteString("# Task: Address PR Review Feedback\n\n")

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

	if err := appendInstructions(&b, dir, fallbackInstructions); err != nil {
		return err
	}

	return writeTaskFile(dir, b.String())
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
	b.WriteString("Address each review comment listed above. Validate your changes compile\n")
	b.WriteString("and pass tests. Do not push to git -- the system handles that.\n")
}

// appendInstructions reads .ai-bot/instructions.md from the workspace
// and appends its content as a "Project Instructions" section. If the
// file does not exist, the fallback string is used instead (typically
// from the project config for prototyping). If both are empty, nothing
// is appended.
func appendInstructions(b *strings.Builder, dir, fallback string) error {
	path := filepath.Join(dir, InstructionsPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read instructions file: %w", err)
	}

	content := strings.TrimSpace(string(data))

	// Repo-level file takes precedence; fall back to project config.
	if content == "" {
		content = strings.TrimSpace(fallback)
	}

	if content == "" {
		return nil
	}

	b.WriteString("\n## Project Instructions\n")
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
// with author attribution.
func writeCommentBlockquote(b *strings.Builder, c models.PRComment) {
	if c.Line > 0 {
		fmt.Fprintf(b, "> [@%s, line %d]\n", c.Author.Username, c.Line)
	} else {
		fmt.Fprintf(b, "> [@%s]\n", c.Author.Username)
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

// writeTaskFile writes content to <dir>/.ai-bot/task.md, creating the
// .ai-bot directory if needed.
func writeTaskFile(dir, content string) error {
	path := filepath.Join(dir, TaskFilePath)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create task file directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}

	return nil
}
