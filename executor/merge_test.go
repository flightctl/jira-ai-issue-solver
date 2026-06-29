package executor_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
)

func TestExecuteMerge_CleanMerge(t *testing.T) {
	d := newTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()

	var mergedBranch string
	d.git.MergeBaseFunc = func(_, branch string) ([]string, error) {
		mergedBranch = branch
		return []string{}, nil
	}

	committed := false
	d.git.HasChangesFunc = func(_, _ string) (bool, error) {
		return true, nil
	}
	d.git.CommitChangesFunc = func(_, _, _, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		committed = true
		return "abc123", nil
	}

	p := d.pipeline(t)
	job := mergeJob("PROJ-1")

	result, err := p.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mergedBranch != "main" {
		t.Errorf("expected merge of main, got %s", mergedBranch)
	}
	if !committed {
		t.Error("expected commit after clean merge")
	}
	if result.PRURL == "" {
		t.Error("expected PR URL in result")
	}
}

func TestExecuteMerge_CleanMerge_NoChanges(t *testing.T) {
	d := newTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()

	d.git.MergeBaseFunc = func(_, _ string) ([]string, error) {
		return []string{}, nil
	}
	d.git.HasChangesFunc = func(_, _ string) (bool, error) {
		return false, nil
	}

	committed := false
	d.git.CommitChangesFunc = func(_, _, _, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		committed = true
		return "abc123", nil
	}

	p := d.pipeline(t)
	job := mergeJob("PROJ-1")

	_, err := p.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if committed {
		t.Error("should not commit when no changes after merge")
	}
}

func TestExecuteMerge_ConflictInvokesAI(t *testing.T) {
	d := newTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()

	d.git.MergeBaseFunc = func(_, _ string) ([]string, error) {
		return []string{"file.go"}, fmt.Errorf("%w: CONFLICT in file.go", services.ErrMergeConflict)
	}

	aiInvoked := false
	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, _ []string) (string, int, error) {
		aiInvoked = true
		return "", 0, nil
	}

	d.git.HasChangesFunc = func(_, _ string) (bool, error) {
		return true, nil
	}
	d.git.CommitChangesFunc = func(_, _, _, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "def456", nil
	}

	taskWritten := false
	d.taskWriter.WriteMergeConflictTaskFunc = func(_ models.PRDetails, _ []string, _, _ string) error {
		taskWritten = true
		return nil
	}

	p := d.pipeline(t)
	job := mergeJob("PROJ-1")

	writeSessionOutput(t, d.wsDir, executor.SessionOutput{CostUSD: 0.50})

	_, err := p.Execute(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !aiInvoked {
		t.Error("expected AI container to be invoked for conflict resolution")
	}
	if !taskWritten {
		t.Error("expected merge conflict task file to be written")
	}
}

func TestExecuteMerge_ConflictAINoChanges(t *testing.T) {
	d := newTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()

	d.git.MergeBaseFunc = func(_, _ string) ([]string, error) {
		return []string{"file.go"}, fmt.Errorf("%w: CONFLICT in file.go", services.ErrMergeConflict)
	}

	d.git.HasChangesFunc = func(_, _ string) (bool, error) {
		return false, nil
	}

	p := d.pipeline(t)
	job := mergeJob("PROJ-1")

	writeSessionOutput(t, d.wsDir, executor.SessionOutput{})

	_, err := p.Execute(context.Background(), job)
	if err == nil {
		t.Fatal("expected error when AI produces no changes")
	}
}

func TestExecuteMerge_NoPRFound(t *testing.T) {
	d := newTestDeps(t)

	d.git.GetPRForBranchFunc = func(_, _, _ string) (*models.PRDetails, error) {
		return nil, nil
	}

	p := d.pipeline(t)
	job := mergeJob("PROJ-1")

	_, err := p.Execute(context.Background(), job)
	if err == nil || !strings.Contains(err.Error(), "no open PR found") {
		t.Fatalf("expected 'no open PR found' error, got %v", err)
	}
}

func TestExecuteMerge_MultiRepo_CleanMerge(t *testing.T) {
	d := newMultiRepoTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()
	d.git.MergeBaseFunc = func(_, _ string) ([]string, error) {
		return []string{}, nil
	}
	d.git.HasChangesFunc = func(_, _ string) (bool, error) {
		return true, nil
	}

	committed := 0
	d.git.CommitChangesFunc = func(_, _, _, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		committed++
		return "abc123", nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), mergeJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if committed == 0 {
		t.Error("expected at least one commit for multi-repo clean merge")
	}
	if result.PRURL == "" {
		t.Error("expected PR URL in result")
	}
}

func TestExecuteMerge_MultiRepo_ConflictRestoresAuth(t *testing.T) {
	d := newMultiRepoTestDeps(t)
	d.git.GetPRForBranchFunc = mergePRFunc()
	d.git.MergeBaseFunc = func(_, _ string) ([]string, error) {
		return []string{"conflict.go"}, fmt.Errorf("%w: CONFLICT", services.ErrMergeConflict)
	}

	stripped := make(map[string]bool)
	restored := make(map[string]bool)
	d.git.StripRemoteAuthFunc = func(dir string) error {
		stripped[dir] = true
		return nil
	}
	d.git.RestoreRemoteAuthFunc = func(dir, _, _ string) error {
		restored[dir] = true
		return nil
	}

	// Simulate AI failure after auth is stripped.
	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, _ []string) (string, int, error) {
		return "", 1, fmt.Errorf("container exec failed")
	}

	p := d.pipeline(t)
	writeSessionOutput(t, d.wsDir, executor.SessionOutput{})

	_, err := p.Execute(context.Background(), mergeJob("PROJ-1"))
	if err == nil {
		t.Fatal("expected error from failed container exec")
	}

	// Verify auth was restored for all stripped repos via defer.
	for dir := range stripped {
		if !restored[dir] {
			t.Errorf("auth not restored for %s after failure", dir)
		}
	}
}

func TestExecuteMerge_MultiRepo_NoPRs(t *testing.T) {
	d := newMultiRepoTestDeps(t)
	d.git.GetPRForBranchFunc = func(_, _, _ string) (*models.PRDetails, error) {
		return nil, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), mergeJob("PROJ-1"))
	if err == nil {
		t.Fatal("expected error when no PRs found in any repo")
	}
}

func mergePRFunc() func(string, string, string) (*models.PRDetails, error) {
	return func(_, _, _ string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number:     42,
			Title:      "Fix auth flow",
			Branch:     "ai-bot/PROJ-1",
			BaseBranch: "main",
			URL:        "https://github.com/org/repo/pull/42",
		}, nil
	}
}

func mergeJob(ticketKey string) *jobmanager.Job {
	return &jobmanager.Job{
		ID:         "merge-job-1",
		TicketKey:  ticketKey,
		Type:       jobmanager.JobTypeMerge,
		AttemptNum: 1,
	}
}
