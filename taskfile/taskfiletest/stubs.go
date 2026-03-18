// Package taskfiletest provides test doubles for the taskfile package.
package taskfiletest

import (
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/taskfile"
)

// Compile-time check that Stub implements taskfile.Writer.
var _ taskfile.Writer = (*Stub)(nil)

// Stub is a test double for [taskfile.Writer].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns nil.
type Stub struct {
	WriteIssueFunc         func(workItem models.WorkItem, dir string, attachmentFiles []string) error
	WriteNewTicketTaskFunc func(workItem models.WorkItem, dir, fallbackInstructions, fallbackWorkflow string) error
	WriteFeedbackTaskFunc  func(prDetails models.PRDetails, newComments, addressedComments []models.PRComment, dir, fallbackInstructions, fallbackWorkflow string) error
}

func (s *Stub) WriteIssue(workItem models.WorkItem, dir string, attachmentFiles []string) error {
	if s.WriteIssueFunc != nil {
		return s.WriteIssueFunc(workItem, dir, attachmentFiles)
	}
	return nil
}

func (s *Stub) WriteNewTicketTask(workItem models.WorkItem, dir, fallbackInstructions, fallbackWorkflow string) error {
	if s.WriteNewTicketTaskFunc != nil {
		return s.WriteNewTicketTaskFunc(workItem, dir, fallbackInstructions, fallbackWorkflow)
	}
	return nil
}

func (s *Stub) WriteFeedbackTask(prDetails models.PRDetails, newComments, addressedComments []models.PRComment, dir, fallbackInstructions, fallbackWorkflow string) error {
	if s.WriteFeedbackTaskFunc != nil {
		return s.WriteFeedbackTaskFunc(prDetails, newComments, addressedComments, dir, fallbackInstructions, fallbackWorkflow)
	}
	return nil
}
