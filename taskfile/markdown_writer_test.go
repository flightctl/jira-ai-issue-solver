package taskfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/taskfile"
)

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

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: PROJ-123")
	assertContains(t, content, "## Summary\nFix null pointer in UserService")
	assertContains(t, content, "## Description")
	assertContains(t, content, "> [Ticket description]")
	assertContains(t, content, "> When photo is null, getProfile() throws NPE.")
	assertContains(t, content, "## Instructions")
	assertContains(t, content, "Do not push to git")
}

func TestWriteNewTicketTask_EmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:     "PROJ-456",
		Summary: "Quick fix",
	}

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: PROJ-456")
	assertContains(t, content, "## Summary\nQuick fix")
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

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// Full description should be included (AI needs it).
	assertContains(t, content, "> Critical vulnerability in auth handler.")
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

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)
	assertNotContains(t, content, "security level")
}

func TestWriteNewTicketTask_MultilineDescription(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	workItem := models.WorkItem{
		Key:         "PROJ-200",
		Summary:     "Complex change",
		Description: "Line one.\n\nLine three after blank.\nLine four.",
	}

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// Each line should be blockquoted; blank lines get bare ">".
	assertContains(t, content, "> Line one.")
	assertContains(t, content, ">\n> Line three after blank.")
	assertContains(t, content, "> Line four.")
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

	if err := writer.WriteNewTicketTask(workItem, dir); err != nil {
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
	if err := writer.WriteNewTicketTask(first, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Write second task file to same location.
	second := models.WorkItem{Key: "PROJ-2", Summary: "Second task"}
	if err := writer.WriteNewTicketTask(second, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	assertContains(t, content, "# Task: Address PR Review Feedback")
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

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, newComments, addressedComments, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, nil, addressedComments, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, newComments, nil, dir); err != nil {
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

	if err := writer.WriteFeedbackTask(pr, comments, nil, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, taskfile.TaskFilePath)); err != nil {
		t.Fatalf("task file should exist: %v", err)
	}
}

func TestWriteFeedbackTask_NoComments(t *testing.T) {
	dir := t.TempDir()
	writer := taskfile.NewMarkdownWriter()

	pr := models.PRDetails{Number: 10, Title: "PR", Branch: "b"}

	if err := writer.WriteFeedbackTask(pr, nil, nil, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readTaskFile(t, dir)

	// Should still produce a valid file with PR context and instructions.
	assertContains(t, content, "## PR Context")
	assertContains(t, content, "## Instructions")
	assertNotContains(t, content, "## Review Comments")
	assertNotContains(t, content, "## Previously Addressed")
}

// --- helpers ---

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
