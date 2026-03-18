// Package taskfile generates structured files that communicate context
// and goals from the bot to the AI agent.
//
// The bot writes three categories of file to the workspace:
//   - issue.md: the stable Jira issue content (key, summary, description).
//     Written once per ticket and referenced by task files in both new-ticket
//     and feedback flows. Persists across sessions.
//   - task.md: session-specific instructions. New-ticket tasks include the
//     summary plus workflow/instructions. Feedback tasks include PR context
//     and review comments. Overwritten each session.
//   - Supporting files (instructions.md, new-ticket-workflow.md): optional
//     project-level guidance appended to task.md.
//
// User-provided content (ticket descriptions, PR comments) is placed
// inside blockquotes with explicit labels to demarcate boundaries
// between bot-authored instructions and user content.
package taskfile

import "jira-ai-issue-solver/models"

const (
	// IssueFilePath is the path, relative to the workspace root,
	// where the Jira issue content is written. This file contains
	// the stable problem definition (key, summary, description)
	// and persists across sessions — new-ticket and feedback flows
	// both write it so the AI always has access to the original
	// ticket context.
	IssueFilePath = ".ai-bot/issue.md"

	// TaskFilePath is the path, relative to the workspace root, where
	// the task file is written. The AI agent reads this file to
	// discover its task.
	TaskFilePath = ".ai-bot/task.md"

	// InstructionsPath is the path, relative to the workspace root,
	// where optional project-specific AI instructions live. If this
	// file exists, its contents are appended to the task file as a
	// "Project Instructions" section. This is the primary mechanism
	// for teams to provide provider-agnostic guidance to the AI
	// (workflow references, validation commands, coding standards).
	InstructionsPath = ".ai-bot/instructions.md"

	// PRDescriptionPath is the path, relative to the workspace root,
	// where the AI agent may write a PR title and description. If
	// present, the first line is used as the PR title and the
	// remaining lines as the PR body. This file is read by the bot
	// after the AI session completes.
	PRDescriptionPath = ".ai-bot/pr.md"

	// AttachmentsDirPath is the path, relative to the workspace
	// root, where downloaded Jira attachments are stored. This
	// directory lives under .ai-bot/ so it is automatically excluded
	// from commits. Both the new-ticket and feedback pipelines write
	// attachments here; the issue file references them when present.
	AttachmentsDirPath = ".ai-bot/attachments"

	// NewTicketWorkflowPath is the path, relative to the workspace
	// root, where optional workflow instructions for new tickets
	// live. Unlike InstructionsPath (which applies to all task
	// types), this file is only appended to new-ticket task files.
	// Use this for multi-phase workflows (assess → diagnose → fix →
	// test → review) that don't apply to PR feedback handling.
	NewTicketWorkflowPath = ".ai-bot/new-ticket-workflow.md"

	// SessionContextPath is the path, relative to the workspace root,
	// where the AI workflow may write a session context manifest
	// summarizing the artifacts and decisions from the initial session.
	// Feedback task files reference this path so the AI addressing
	// PR review comments can recover the reasoning behind the original
	// implementation without re-deriving it from code.
	SessionContextPath = ".ai-bot/session-context.md"

	// FeedbackWorkflowPath is the path, relative to the workspace
	// root, where optional workflow instructions for PR feedback
	// live. Unlike InstructionsPath (which applies to all task
	// types), this file is only appended to feedback task files.
	// Use this for structured feedback processes that maintain
	// session context across review rounds.
	FeedbackWorkflowPath = ".ai-bot/feedback-workflow.md"
)

// Writer generates task files that the AI agent reads to understand
// what work needs to be done.
type Writer interface {
	// WriteIssue writes the Jira issue content to <dir>/.ai-bot/issue.md.
	// This file contains the stable problem definition (key, summary,
	// description) and is referenced by both new-ticket and feedback
	// task files. attachmentFiles lists filenames downloaded to
	// .ai-bot/attachments/; if non-empty, an Attachments section is
	// added referencing them.
	WriteIssue(workItem models.WorkItem, dir string, attachmentFiles []string) error

	// WriteNewTicketTask generates a task file for implementing a new
	// ticket. The file is written to <dir>/.ai-bot/task.md.
	// fallbackInstructions is used when .ai-bot/instructions.md does
	// not exist (universal guidance like validation commands).
	// fallbackWorkflow is used when .ai-bot/new-ticket-workflow.md
	// does not exist (multi-phase workflow for new tickets only).
	WriteNewTicketTask(workItem models.WorkItem, dir, fallbackInstructions, fallbackWorkflow string) error

	// WriteFeedbackTask generates a task file for addressing PR review
	// feedback. newComments are comments requiring action;
	// addressedComments are previously handled comments included for
	// context. The file is written to <dir>/.ai-bot/task.md.
	// fallbackInstructions is used when .ai-bot/instructions.md does
	// not exist in the workspace. fallbackWorkflow is used when
	// .ai-bot/feedback-workflow.md does not exist.
	WriteFeedbackTask(prDetails models.PRDetails,
		newComments, addressedComments []models.PRComment,
		dir, fallbackInstructions, fallbackWorkflow string) error
}
