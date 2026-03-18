package taskfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/taskfile"
)

// --- WriteIssue ---

func TestWriteIssue_BasicTicket(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-123",
		Summary:     "Fix null pointer in UserService",
		Description: "When photo is null, getProfile() throws NPE.",
		Type:        "Bug",
	}

	if err := writer.WriteIssue(workItem, dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)

	assertContains(t, content, "# PROJ-123: Fix null pointer in UserService")
	assertContains(t, content, "## Description")
	assertContains(t, content, "> [Ticket description]")
	assertContains(t, content, "> When photo is null, getProfile() throws NPE.")
}

func TestWriteIssue_EmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-456", Summary: "Quick fix"}

	if err := writer.WriteIssue(workItem, dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)

	assertContains(t, content, "# PROJ-456: Quick fix")
	assertNotContains(t, content, "## Description")
}

func TestWriteIssue_MultilineDescription(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-200",
		Summary:     "Complex change",
		Description: "Line one.\n\nLine three after blank.\nLine four.",
	}

	if err := writer.WriteIssue(workItem, dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)

	assertContains(t, content, "> Line one.")
	assertContains(t, content, ">\n> Line three after blank.")
	assertContains(t, content, "> Line four.")
}

func TestWriteIssue_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-300", Summary: "Test dir creation"}

	if err := writer.WriteIssue(workItem, dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, taskfile.IssueFilePath)); err != nil {
		t.Fatalf("issue file should exist: %v", err)
	}
}

func TestWriteIssue_WithAttachments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-400",
		Summary:     "Crash on startup",
		Description: "See attached log.",
	}

	attachments := []string{"crash.log", "config.yaml"}
	if err := writer.WriteIssue(workItem, dir, attachments); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)

	assertContains(t, content, "## Attachments")
	assertContains(t, content, taskfile.AttachmentsDirPath)
	assertContains(t, content, "- `crash.log`")
	assertContains(t, content, "- `config.yaml`")
}

func TestWriteIssue_NoAttachments_NoSection(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-401", Summary: "No attachments"}

	if err := writer.WriteIssue(workItem, dir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)
	assertNotContains(t, content, "## Attachments")
}

func TestWriteIssue_EmptyAttachmentList_NoSection(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-402", Summary: "Empty list"}

	if err := writer.WriteIssue(workItem, dir, []string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readIssueFile(t, dir)
	assertNotContains(t, content, "## Attachments")
}

// --- WriteNewTicketTask ---

func TestWriteNewTicketTask_BasicTicket(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-123",
		Summary:     "Fix null pointer in UserService",
		Description: "When photo is null, getProfile() throws NPE.",
		Type:        "Bug",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: PROJ-123")
	assertContains(t, content, "## Summary\nFix null pointer in UserService")
	assertContains(t, content, taskfile.IssueFilePath)
	assertNotContains(t, content, "> [Ticket description]")
	assertContains(t, content, "## Instructions")
	assertContains(t, content, "Do not push to git")
}

func TestWriteNewTicketTask_ReferencesIssueFile(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-456",
		Summary: "Quick fix",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: PROJ-456")
	assertContains(t, content, "## Summary\nQuick fix")
	assertContains(t, content, taskfile.IssueFilePath)
	assertNotContains(t, content, "## Description")
	assertContains(t, content, "## Instructions")
}

func TestWriteNewTicketTask_SecurityLevel(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:           "SEC-789",
		Summary:       "Fix auth bypass",
		Description:   "Critical vulnerability in auth handler.",
		SecurityLevel: "Embargoed",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// Description should NOT be in task.md (it's in issue.md).
	assertNotContains(t, content, "> Critical vulnerability in auth handler.")
	// Security note should be present.
	assertContains(t, content, "security level set")
	assertContains(t, content, "Do not include specific")
	assertContains(t, content, "vulnerability details")
}

func TestWriteNewTicketTask_NoSecurityNote_WhenNoSecurityLevel(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-100",
		Summary:     "Add feature",
		Description: "Add a button.",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "security level")
}

func TestWriteNewTicketTask_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-300",
		Summary: "Test dir creation",
	}

	// Verify .ai-bot/ doesn't exist yet.
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if _, err := os.Stat(aiBotDir); err == nil {
		t.Fatal(".ai-bot directory should not exist before write")
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify directory was created and file exists.
	if _, err := os.Stat(aiBotDir); err != nil {
		t.Fatalf(".ai-bot directory should exist after write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, taskfile.TaskFilePath)); err != nil {
		t.Fatalf("task file should exist: %v", err)
	}
}

func TestWriteNewTicketTask_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	// Write first task file.
	first := models.WorkItem{Key: "PROJ-1", Summary: "First task"}
	if err := writer.WriteNewTicketTask(first, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Write second task file to same location.
	second := models.WorkItem{Key: "PROJ-2", Summary: "Second task"}
	if err := writer.WriteNewTicketTask(second, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: PROJ-2")
	assertNotContains(t, content, "PROJ-1")
}

// --- WriteFeedbackTask ---

func TestWriteFeedbackTask_SingleNewComment(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{
		Number: 42,
		Title:  "Fix NPE in UserService",
		Branch: "ai-bot/PROJ-123",
	}
	newComments := []models.PRComment{{
		ID:       1,
		Author:   models.Author{Username: "reviewer1"},
		Body:     "Use Optional instead of null check.",
		FilePath: "src/UserService.java",
		Line:     145,
	}}

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: Address PR Review Feedback")
	assertContains(t, content, taskfile.IssueFilePath)
	assertContains(t, content, "PR #42: Fix NPE in UserService")
	assertContains(t, content, "Branch: ai-bot/PROJ-123")
	assertContains(t, content, "## Review Comments")
	assertContains(t, content, "### File: src/UserService.java")
	assertContains(t, content, "> [@reviewer1, line 145]")
	assertContains(t, content, "> Use Optional instead of null check.")
	assertContains(t, content, "## Instructions")
	assertNotContains(t, content, "Previously Addressed")
}

func TestWriteFeedbackTask_CommentsGroupedByFile(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	newComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix A", FilePath: "file_b.go", Line: 10},
		{Author: models.Author{Username: "r2"}, Body: "Fix B", FilePath: "file_a.go", Line: 20},
		{Author: models.Author{Username: "r3"}, Body: "Fix C", FilePath: "file_a.go", Line: 30},
	}

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// file_a.go should appear before file_b.go (sorted).
	idxA := strings.Index(content, "### File: file_a.go")
	idxB := strings.Index(content, "### File: file_b.go")
	if idxA < 0 || idxB < 0 {
		t.Fatal("expected both file headers in output")
	}
	if idxA >= idxB {
		t.Error("file_a.go should appear before file_b.go (sorted)")
	}

	// Both comments for file_a.go should be present.
	assertContains(t, content, "> Fix B")
	assertContains(t, content, "> Fix C")
}

func TestWriteFeedbackTask_GeneralComments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	newComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "File comment", FilePath: "main.go", Line: 5},
		{Author: models.Author{Username: "r2"}, Body: "General comment"},
	}

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "### File: main.go")
	assertContains(t, content, "### General")
	assertContains(t, content, "> [@r2]")

	// General should appear after file-specific comments.
	idxFile := strings.Index(content, "### File: main.go")
	idxGeneral := strings.Index(content, "### General")
	if idxGeneral <= idxFile {
		t.Error("general comments should appear after file-specific comments")
	}
}

func TestWriteFeedbackTask_NewAndAddressedComments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	newComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "New feedback", FilePath: "main.go", Line: 10},
	}
	addressedComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Old feedback", FilePath: "main.go", Line: 5},
	}

	if err := writer.WriteFeedbackTask(pr, newComments, addressedComments, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Review Comments")
	assertContains(t, content, "> New feedback")
	assertContains(t, content, "## Previously Addressed Comments (Context Only)")
	assertContains(t, content, "> Old feedback")

	// New should appear before addressed.
	idxNew := strings.Index(content, "## Review Comments")
	idxAddr := strings.Index(content, "## Previously Addressed")
	if idxNew >= idxAddr {
		t.Error("new comments section should appear before addressed section")
	}
}

func TestWriteFeedbackTask_OnlyAddressedComments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	addressedComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Already fixed"},
	}

	if err := writer.WriteFeedbackTask(pr, nil, addressedComments, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertNotContains(t, content, "## Review Comments")
	assertContains(t, content, "## Previously Addressed Comments (Context Only)")
	assertContains(t, content, "## Instructions")
}

func TestWriteFeedbackTask_CommentWithNoLine(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	newComments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "File-level comment", FilePath: "main.go"},
	}

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// No "line" annotation when Line is zero.
	assertContains(t, content, "> [@r1]")
	assertNotContains(t, content, "line 0")
}

func TestWriteFeedbackTask_MultilineCommentBody(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	newComments := []models.PRComment{{
		Author:   models.Author{Username: "r1"},
		Body:     "First paragraph.\n\nSecond paragraph.",
		FilePath: "main.go",
		Line:     10,
	}}

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "> First paragraph.")
	assertContains(t, content, "> Second paragraph.")
}

func TestWriteFeedbackTask_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 1, Title: "T", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r"}, Body: "c"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, taskfile.TaskFilePath)); err != nil {
		t.Fatalf("task file should exist: %v", err)
	}
}

func TestWriteFeedbackTask_ReferencesSessionContext(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 42, Title: "Fix NPE", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this", FilePath: "main.go", Line: 10},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, taskfile.SessionContextPath)
	assertContains(t, content, "context\nfrom the session that created this PR")

	// Session context reference should appear inside Instructions, before
	// the action items.
	idxCtx := strings.Index(content, taskfile.SessionContextPath)
	idxInstr := strings.Index(content, "## Instructions")
	idxAction := strings.Index(content, "Address each review comment")
	if idxCtx < idxInstr || idxCtx > idxAction {
		t.Error("session context reference should appear inside Instructions, before action items")
	}
}

func TestWriteFeedbackTask_NoComments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}

	if err := writer.WriteFeedbackTask(pr, nil, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// Should still produce a valid file with PR context and instructions.
	assertContains(t, content, "## PR Context")
	assertContains(t, content, "## Instructions")
	assertNotContains(t, content, "## Review Comments")
	assertNotContains(t, content, "## Previously Addressed")
}

// --- WriteNewTicketTask with instructions.md ---

func TestWriteNewTicketTask_WithInstructionsMd(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, `## Workflow
Follow the bugfix workflow at .ai-workflows/bugfix/skills/controller.md.

## Validation
After making changes, run:
- `+"`make build`"+`
- `+"`make test`"+`
- `+"`make lint`")

	workItem := models.WorkItem{
		Key:         "PROJ-500",
		Summary:     "Fix something",
		Description: "Details here.",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Project Instructions")
	assertContains(t, content, "Follow the bugfix workflow")
	assertContains(t, content, "`make build`")
	assertContains(t, content, "`make lint`")
	// Standard instructions should still be present.
	assertContains(t, content, "## Instructions")
	assertContains(t, content, "Do not push to git")
}

func TestWriteNewTicketTask_NoInstructionsMd(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-501",
		Summary: "No instructions file",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertNotContains(t, content, "## Project Instructions")
	assertContains(t, content, "## Instructions")
}

func TestWriteNewTicketTask_EmptyInstructionsMd(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "   \n  \n  ")

	workItem := models.WorkItem{
		Key:     "PROJ-502",
		Summary: "Empty instructions",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "## Project Instructions")
}

func TestWriteNewTicketTask_InstructionsAfterStandardInstructions(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Custom guidance here.")

	workItem := models.WorkItem{
		Key:     "PROJ-503",
		Summary: "Order test",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	idxStd := strings.Index(content, "## Instructions")
	idxProj := strings.Index(content, "## Project Instructions")
	if idxStd < 0 || idxProj < 0 {
		t.Fatal("expected both instruction sections")
	}
	if idxProj <= idxStd {
		t.Error("Project Instructions should appear after standard Instructions")
	}
}

// --- WriteFeedbackTask with instructions.md ---

func TestWriteFeedbackTask_WithInstructionsMd(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Run `make test` after changes.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Project Instructions")
	assertContains(t, content, "Run `make test` after changes.")
	assertContains(t, content, "## Instructions")
}

func TestWriteFeedbackTask_NoInstructionsMd(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "## Project Instructions")
}

// --- Fallback instructions (project config) ---

func TestWriteNewTicketTask_FallbackInstructions(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-600",
		Summary: "Fallback test",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "Run `make check` before committing.", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Project Instructions")
	assertContains(t, content, "Run `make check` before committing.")
}

func TestWriteNewTicketTask_RepoFileOverridesFallback(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Repo-level guidance.")

	workItem := models.WorkItem{
		Key:     "PROJ-601",
		Summary: "Override test",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "Fallback guidance.", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Project Instructions")
	assertContains(t, content, "Repo-level guidance.")
	assertNotContains(t, content, "Fallback guidance.")
}

func TestWriteNewTicketTask_EmptyFallbackIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-602",
		Summary: "Empty fallback",
	}

	if err := writer.WriteNewTicketTask(workItem, dir, "   ", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "## Project Instructions")
}

func TestWriteFeedbackTask_FallbackInstructions(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "Always run lint.", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Project Instructions")
	assertContains(t, content, "Always run lint.")
}

func TestWriteFeedbackTask_RepoFileOverridesFallback(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Repo instructions win.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "Fallback instructions lose.", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "Repo instructions win.")
	assertNotContains(t, content, "Fallback instructions lose.")
}

// --- Workflow (new-ticket-only) ---

func TestWriteNewTicketTask_FallbackWorkflow(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-700", Summary: "Workflow test"}

	workflow := "1. Assess\n2. Diagnose\n3. Fix"
	if err := writer.WriteNewTicketTask(workItem, dir, "", workflow); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Workflow")
	assertContains(t, content, "1. Assess")
	assertContains(t, content, "3. Fix")
}

func TestWriteNewTicketTask_WorkflowFile(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeWorkflow(t, dir, "Read assess.md, then fix.md.")

	workItem := models.WorkItem{Key: "PROJ-701", Summary: "Workflow file test"}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Workflow")
	assertContains(t, content, "Read assess.md, then fix.md.")
}

func TestWriteNewTicketTask_WorkflowFileOverridesFallback(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeWorkflow(t, dir, "Repo workflow wins.")

	workItem := models.WorkItem{Key: "PROJ-702", Summary: "Override test"}

	if err := writer.WriteNewTicketTask(workItem, dir, "", "Fallback workflow loses."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "Repo workflow wins.")
	assertNotContains(t, content, "Fallback workflow loses.")
}

func TestWriteNewTicketTask_NoWorkflow(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{Key: "PROJ-703", Summary: "No workflow"}

	if err := writer.WriteNewTicketTask(workItem, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "## Workflow")
}

func TestWriteNewTicketTask_WorkflowAfterProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Run make test.")

	workItem := models.WorkItem{Key: "PROJ-704", Summary: "Order test"}

	if err := writer.WriteNewTicketTask(workItem, dir, "", "Execute workflow phases."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	idxInstr := strings.Index(content, "## Project Instructions")
	idxWf := strings.Index(content, "## Workflow")
	if idxInstr < 0 || idxWf < 0 {
		t.Fatal("expected both sections")
	}
	if idxWf <= idxInstr {
		t.Error("Workflow should appear after Project Instructions")
	}
}

func TestWriteFeedbackTask_NewTicketWorkflowNotIncluded(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	// New-ticket workflow file should NOT be included in feedback tasks.
	writeWorkflow(t, dir, "This should not appear.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "This should not appear")
}

// --- WriteFeedbackTask with feedback-workflow.md ---

func TestWriteFeedbackTask_FeedbackWorkflowFile(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeFeedbackWorkflow(t, dir, "Read session-context.md, address comments, update artifacts.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Workflow")
	assertContains(t, content, "Read session-context.md, address comments, update artifacts.")
}

func TestWriteFeedbackTask_FallbackFeedbackWorkflow(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", "Fallback feedback workflow."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "## Workflow")
	assertContains(t, content, "Fallback feedback workflow.")
}

func TestWriteFeedbackTask_FeedbackWorkflowFileOverridesFallback(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeFeedbackWorkflow(t, dir, "Repo feedback workflow wins.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", "Fallback feedback workflow loses."); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "Repo feedback workflow wins.")
	assertNotContains(t, content, "Fallback feedback workflow loses.")
}

func TestWriteFeedbackTask_NoFeedbackWorkflow(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "## Workflow")
}

func TestWriteFeedbackTask_WorkflowAfterProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	writeInstructions(t, dir, "Run make test.")
	writeFeedbackWorkflow(t, dir, "Update session-context.md after changes.")

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}
	comments := []models.PRComment{
		{Author: models.Author{Username: "r1"}, Body: "Fix this"},
	}

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	idxInstr := strings.Index(content, "## Project Instructions")
	idxWf := strings.Index(content, "## Workflow")
	if idxInstr < 0 || idxWf < 0 {
		t.Fatal("expected both sections")
	}
	if idxWf <= idxInstr {
		t.Error("Workflow should appear after Project Instructions")
	}
}

// --- helpers ---

func writeFeedbackWorkflow(t *testing.T, dir, content string) {
	t.Helper()
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aiBotDir, "feedback-workflow.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeWorkflow(t *testing.T, dir, content string) {
	t.Helper()
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aiBotDir, "new-ticket-workflow.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInstructions(t *testing.T, dir, content string) {
	t.Helper()
	instrDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(instrDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instrDir, "instructions.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readIssueFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, taskfile.IssueFilePath)) // #nosec G304 -- test reads from t.TempDir()
	if err != nil {
		t.Fatalf("failed to read issue file: %v", err)
	}
	return string(data)
}

func readTaskFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, taskfile.TaskFilePath)) // #nosec G304 -- test reads from t.TempDir()
	if err != nil {
		t.Fatalf("failed to read task file: %v", err)
	}
	return string(data)
}

func assertContains(t *testing.T, content, substr string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("expected content to contain %q\ngot:\n%s", substr, content)
	}
}

func assertNotContains(t *testing.T, content, substr string) {
	t.Helper()
	if strings.Contains(content, substr) {
		t.Errorf("expected content to NOT contain %q\ngot:\n%s", substr, content)
	}
}
