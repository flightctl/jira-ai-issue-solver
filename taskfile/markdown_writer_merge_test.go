package taskfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/taskfile"
)

func TestWriteMergeConflictTask(t *testing.T) {
	dir := t.TempDir()

	w := taskfile.NewMarkdownWriter()
	pr := models.PRDetails{
		Number:     42,
		Title:      "Fix auth flow",
		Branch:     "ai-bot/PROJ-1",
		BaseBranch: "main",
	}
	conflicts := []string{"pkg/auth.go", "cmd/server.go"}

	if err := w.WriteMergeConflictTask(pr, conflicts, dir, ""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(dir, taskfile.TaskFilePath)) //nolint:gosec // test reads from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	body := string(content)

	checks := []string{
		"# Task: Resolve Merge Conflicts",
		"PR #42: Fix auth flow",
		"Branch: ai-bot/PROJ-1",
		"Base: main",
		"## Conflict Details",
		"- `cmd/server.go`",
		"- `pkg/auth.go`",
		"conflict markers",
		"run the validation commands in the Project Instructions section below",
	}

	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("task file should contain %q", want)
		}
	}
}

func TestWriteMergeConflictTask_SortsFiles(t *testing.T) {
	dir := t.TempDir()

	w := taskfile.NewMarkdownWriter()
	pr := models.PRDetails{Number: 1, Branch: "b", BaseBranch: "main"}
	conflicts := []string{"z.go", "a.go", "m.go"}

	if err := w.WriteMergeConflictTask(pr, conflicts, dir, ""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(dir, taskfile.TaskFilePath)) //nolint:gosec // test reads from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	body := string(content)
	aIdx := strings.Index(body, "- `a.go`")
	mIdx := strings.Index(body, "- `m.go`")
	zIdx := strings.Index(body, "- `z.go`")

	if aIdx > mIdx || mIdx > zIdx {
		t.Error("conflict files should be sorted alphabetically")
	}
}

func TestWriteMergeConflictTask_WithOverrideInstructions(t *testing.T) {
	dir := t.TempDir()

	w := taskfile.NewMarkdownWriter()
	pr := models.PRDetails{Number: 1, Branch: "b", BaseBranch: "main"}

	if err := w.WriteMergeConflictTask(pr, []string{"file.go"}, dir, "Run make test after resolving"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(dir, taskfile.TaskFilePath)) //nolint:gosec // test reads from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "Run make test after resolving") {
		t.Error("task file should contain override instructions")
	}
}

func TestWriteMergeConflictTask_EmptyConflictFiles(t *testing.T) {
	dir := t.TempDir()

	w := taskfile.NewMarkdownWriter()
	pr := models.PRDetails{Number: 1, Branch: "b", BaseBranch: "main"}

	if err := w.WriteMergeConflictTask(pr, nil, dir, ""); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(dir, taskfile.TaskFilePath)) //nolint:gosec // test reads from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(content), "### Conflicted Files") {
		t.Error("should not include Conflicted Files section when empty")
	}
}

func TestWriteMultiRepoMergeConflictTask(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	if err := os.MkdirAll(repoDir, 0o750); err != nil {
		t.Fatal(err)
	}

	w := taskfile.NewMarkdownWriter()
	pr := models.PRDetails{
		Number:     10,
		Title:      "Multi-repo fix",
		Branch:     "ai-bot/PROJ-2",
		BaseBranch: "main",
	}

	repos := []taskfile.RepoContext{
		{Name: "repo1", Dir: repoDir},
	}

	if err := w.WriteMultiRepoMergeConflictTask(pr, []string{"repo1/api.go"}, wsDir, repos); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(wsDir, taskfile.TaskFilePath)) //nolint:gosec // test reads from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}

	body := string(content)
	if !strings.Contains(body, "Multi-Repo") {
		t.Error("task file should indicate multi-repo")
	}
	if !strings.Contains(body, "- `repo1/api.go`") {
		t.Error("task file should list conflict files")
	}
}
