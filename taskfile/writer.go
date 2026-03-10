// Package taskfile generates structured task files that communicate goals
// from the bot to the AI agent.
//
// Task files are markdown documents written to a well-known path in the
// workspace (.ai-bot/task.md). The AI agent reads these files to
// understand what work needs to be done. The bot writes goals (what),
// not instructions (how) -- the AI determines how to implement changes,
// what to validate, and how to fix issues.
//
// Two task file formats are supported:
//   - New ticket: includes the ticket summary, full description, and
//     standard instructions for the AI.
//   - PR feedback: includes the PR context, review comments grouped by
//     file, and standard instructions. Comments are split into "action
//     required" (new comments) and "context only" (previously addressed).
//
// User-provided content (ticket descriptions, PR comments) is placed
// inside blockquotes with explicit labels to demarcate boundaries
// between bot-authored instructions and user content.
package taskfile

import "jira-ai-issue-solver/models"

const (
	// TaskFilePath is the path, relative to the workspace root, where
	// the task file is written. The AI agent reads this file to
	// discover its task.
	TaskFilePath = ".ai-bot/task.md"
)

// Writer generates task files that the AI agent reads to understand
// what work needs to be done.
type Writer interface {
	// WriteNewTicketTask generates a task file for implementing a new
	// ticket. The file is written to <dir>/.ai-bot/task.md.
	WriteNewTicketTask(workItem models.WorkItem, dir string) error

	// WriteFeedbackTask generates a task file for addressing PR review
	// feedback. newComments are comments requiring action;
	// addressedComments are previously handled comments included for
	// context. The file is written to <dir>/.ai-bot/task.md.
	WriteFeedbackTask(prDetails models.PRDetails,
		newComments, addressedComments []models.PRComment,
		dir string) error
}
