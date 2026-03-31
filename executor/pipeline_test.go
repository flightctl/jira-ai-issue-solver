package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/container/containertest"
	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/executor/executortest"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/taskfile/taskfiletest"
	"jira-ai-issue-solver/tracker/trackertest"
	"jira-ai-issue-solver/workspace/workspacetest"
)

// --- NewPipeline validation ---

func TestNewPipeline_RejectsEmptyBotUsername(t *testing.T) {
	_, err := executor.NewPipeline(
		executor.Config{DefaultProvider: "claude"},
		&trackertest.Stub{}, &executortest.StubGitService{},
		&containertest.StubManager{}, &workspacetest.Stub{},
		&taskfiletest.Stub{}, &executortest.StubProjectResolver{},
		zap.NewNop())
	if err == nil {
		t.Fatal("expected error for empty bot username")
	}
}

func TestNewPipeline_RejectsEmptyProvider(t *testing.T) {
	_, err := executor.NewPipeline(
		executor.Config{BotUsername: "bot"},
		&trackertest.Stub{}, &executortest.StubGitService{},
		&containertest.StubManager{}, &workspacetest.Stub{},
		&taskfiletest.Stub{}, &executortest.StubProjectResolver{},
		zap.NewNop())
	if err == nil {
		t.Fatal("expected error for empty default provider")
	}
}

func TestNewPipeline_RejectsNilDependencies(t *testing.T) {
	cfg := executor.Config{BotUsername: "bot", DefaultProvider: "claude"}
	deps := []struct {
		name string
		fn   func() error
	}{
		{"tracker", func() error {
			_, err := executor.NewPipeline(cfg, nil,
				&executortest.StubGitService{}, &containertest.StubManager{},
				&workspacetest.Stub{}, &taskfiletest.Stub{},
				&executortest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"git", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				nil, &containertest.StubManager{},
				&workspacetest.Stub{}, &taskfiletest.Stub{},
				&executortest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"containers", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				&executortest.StubGitService{}, nil,
				&workspacetest.Stub{}, &taskfiletest.Stub{},
				&executortest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"workspaces", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				&executortest.StubGitService{}, &containertest.StubManager{},
				nil, &taskfiletest.Stub{},
				&executortest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"taskWriter", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				&executortest.StubGitService{}, &containertest.StubManager{},
				&workspacetest.Stub{}, nil,
				&executortest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"projects", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				&executortest.StubGitService{}, &containertest.StubManager{},
				&workspacetest.Stub{}, &taskfiletest.Stub{},
				nil, zap.NewNop())
			return err
		}},
		{"logger", func() error {
			_, err := executor.NewPipeline(cfg, &trackertest.Stub{},
				&executortest.StubGitService{}, &containertest.StubManager{},
				&workspacetest.Stub{}, &taskfiletest.Stub{},
				&executortest.StubProjectResolver{}, nil)
			return err
		}},
	}

	for _, d := range deps {
		if d.fn() == nil {
			t.Errorf("expected error for nil %s", d.name)
		}
	}
}

func TestNewPipeline_ValidConfig(t *testing.T) {
	p, err := executor.NewPipeline(
		executor.Config{BotUsername: "bot", DefaultProvider: "claude"},
		&trackertest.Stub{}, &executortest.StubGitService{},
		&containertest.StubManager{}, &workspacetest.Stub{},
		&taskfiletest.Stub{}, &executortest.StubProjectResolver{},
		zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil pipeline")
	}
}

// --- Execute dispatch ---

func TestExecute_FeedbackDispatch(t *testing.T) {
	d := newTestDeps(t)
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number: 42, Title: "Fix a bug",
			Branch: head, URL: "https://github.com/org/repo/pull/42",
		}, nil
	}
	d.git.GetPRCommentsFunc = func(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), &jobmanager.Job{
		ID: "j1", TicketKey: "PROJ-1", Type: jobmanager.JobTypeFeedback,
		AttemptNum: 1,
	})
	// Should not return "not yet implemented".
	if err != nil && strings.Contains(err.Error(), "not yet implemented") {
		t.Fatalf("feedback pipeline should be implemented, got %v", err)
	}
}

func TestExecute_UnknownTypeReturnsError(t *testing.T) {
	d := newTestDeps(t)
	p := d.pipeline(t)

	_, err := p.Execute(context.Background(), &jobmanager.Job{
		ID: "j1", TicketKey: "PROJ-1", Type: "unknown",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown job type") {
		t.Fatalf("expected unknown type error, got %v", err)
	}
}

// --- Happy path ---

func TestExecuteNewTicket_HappyPath(t *testing.T) {
	d := newTestDeps(t)

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var prParams models.PRParams
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prParams = params
		return &models.PR{Number: 42, URL: "https://github.com/org/repo/pull/42"}, nil
	}

	var commentPosted string
	d.tracker.AddCommentFunc = func(key, body string) error {
		commentPosted = body
		return nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newTicketJob("PROJ-123"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify result.
	if result.PRURL != "https://github.com/org/repo/pull/42" {
		t.Errorf("PRURL = %q, want github URL", result.PRURL)
	}
	if result.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", result.PRNumber)
	}
	if result.Draft {
		t.Error("expected non-draft PR")
	}
	if !result.ValidationPassed {
		t.Error("expected ValidationPassed = true")
	}

	// Verify status transitions: in-progress, then in-review.
	if len(transitions) != 2 {
		t.Fatalf("transitions = %v, want 2 entries", transitions)
	}
	if transitions[0] != "In Progress" {
		t.Errorf("first transition = %q, want In Progress", transitions[0])
	}
	if transitions[1] != "In Review" {
		t.Errorf("second transition = %q, want In Review", transitions[1])
	}

	// Verify PR was created with correct params.
	if prParams.Owner != "org" || prParams.Repo != "repo" {
		t.Errorf("PR owner/repo = %s/%s, want org/repo", prParams.Owner, prParams.Repo)
	}
	if prParams.Base != "main" {
		t.Errorf("PR base = %q, want main", prParams.Base)
	}
	if !strings.Contains(prParams.Head, "PROJ-123") {
		t.Errorf("PR head = %q, should contain ticket key", prParams.Head)
	}

	// Verify PR URL posted as comment (no PRURLFieldName configured).
	if !strings.Contains(commentPosted, "https://github.com/org/repo/pull/42") {
		t.Errorf("comment = %q, should contain PR URL", commentPosted)
	}
}

// --- No changes ---

func TestExecuteNewTicket_NoChanges(t *testing.T) {
	d := newTestDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var errorComment string
	d.tracker.AddCommentFunc = func(key, body string) error {
		errorComment = body
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Fatalf("expected no-changes error, got %v", err)
	}

	// Verify status reverted: in-progress, then back to todo.
	if len(transitions) < 2 {
		t.Fatalf("transitions = %v, want at least 2", transitions)
	}
	if last := transitions[len(transitions)-1]; last != "To Do" {
		t.Errorf("last transition = %q, want To Do (revert)", last)
	}

	// Verify error comment posted.
	if !strings.Contains(errorComment, "no changes") {
		t.Errorf("error comment = %q, should mention no changes", errorComment)
	}
}

// --- AI timeout ---

func TestExecuteNewTicket_SessionTimeout(t *testing.T) {
	d := newTestDeps(t)

	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		<-ctx.Done() // wait for session timeout
		return "", 0, ctx.Err()
	}

	var reverted bool
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		if status == "To Do" {
			reverted = true
		}
		return nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		SessionTimeout:  100 * time.Millisecond,
		AIAPIKeys:       map[string]string{"claude": "test-key"},
	})

	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if !strings.Contains(err.Error(), "session timeout exceeded") {
		t.Errorf("expected session timeout error, got %v", err)
	}
	if !reverted {
		t.Error("expected status to be reverted to todo")
	}
}

func TestExecuteNewTicket_ParentContextCancelled(t *testing.T) {
	d := newTestDeps(t)

	ctx, cancel := context.WithCancel(context.Background())
	d.containers.ExecFunc = func(execCtx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		cancel() // simulate shutdown
		return "", 0, context.Canceled
	}

	p := d.pipeline(t)
	_, err := p.Execute(ctx, newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "job cancelled") {
		t.Fatalf("expected job cancelled error, got %v", err)
	}
}

// --- Container start failure ---

func TestExecuteNewTicket_ContainerStartFails(t *testing.T) {
	d := newTestDeps(t)
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		return nil, errors.New("image not found")
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "start container") {
		t.Fatalf("expected container start error, got %v", err)
	}
}

// --- Commit failure ---

func TestExecuteNewTicket_CommitFails(t *testing.T) {
	d := newTestDeps(t)
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "", errors.New("API rate limit")
	}

	var reverted bool
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		if status == "To Do" {
			reverted = true
		}
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "commit changes") {
		t.Fatalf("expected commit error, got %v", err)
	}
	if !reverted {
		t.Error("expected status to be reverted")
	}
}

// --- PR creation failure ---

func TestExecuteNewTicket_PRCreationFails(t *testing.T) {
	d := newTestDeps(t)
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		return nil, errors.New("PR creation failed")
	}

	var reverted bool
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		if status == "To Do" {
			reverted = true
		}
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "create PR") {
		t.Fatalf("expected PR error, got %v", err)
	}
	if !reverted {
		t.Error("expected status to be reverted")
	}
}

// --- Draft PR paths ---

func TestExecuteNewTicket_DraftPR_NonZeroExitCode(t *testing.T) {
	d := newTestDeps(t)
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		return "", 1, nil // non-zero exit, no exec error
	}

	var prDraft bool
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prDraft = params.Draft
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	// Verify ticket is NOT transitioned to in-review for draft PRs.
	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prDraft {
		t.Error("expected draft PR")
	}
	if !result.Draft {
		t.Error("expected result.Draft = true")
	}
	if result.ValidationPassed {
		t.Error("expected ValidationPassed = false for draft")
	}
	// Should only have "In Progress" transition, not "In Review".
	for _, s := range transitions {
		if s == "In Review" {
			t.Error("draft PR should not transition to In Review")
		}
	}
}

func TestExecuteNewTicket_DraftPR_ValidationFailed(t *testing.T) {
	d := newTestDeps(t)

	// Write session-output.json with validation_passed=false.
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		writeSessionOutput(t, d.wsDir, executor.SessionOutput{
			ExitCode:         0,
			ValidationPassed: boolPtr(false),
		})
		return "", 0, nil
	}

	var prDraft bool
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prDraft = params.Draft
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prDraft {
		t.Error("expected draft PR for validation failure")
	}
	if !result.Draft {
		t.Error("expected result.Draft = true")
	}
}

func TestExecuteNewTicket_DraftPR_RepoConfigForcesDraft(t *testing.T) {
	d := newTestDeps(t)

	// Write .ai-bot/config.yaml with pr.draft: true.
	cfgDir := filepath.Join(d.wsDir, ".ai-bot")
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgContent := "pr:\n  draft: true\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var prDraft bool
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prDraft = params.Draft
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prDraft {
		t.Error("expected draft PR from repo config")
	}
}

// --- Security-level tickets ---

func TestExecuteNewTicket_SecurityLevel_RedactedPR(t *testing.T) {
	d := newTestDeps(t)
	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:           key,
			Summary:       "Fix auth bypass vulnerability",
			Description:   "Critical security vulnerability details...",
			Type:          "Bug",
			SecurityLevel: "Embargoed",
			Components:    []string{},
			Labels:        []string{},
		}, nil
	}

	var prParams models.PRParams
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prParams = params
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("SEC-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Title should be redacted.
	if strings.Contains(prParams.Title, "auth bypass") {
		t.Errorf("PR title should be redacted, got %q", prParams.Title)
	}
	if !strings.Contains(prParams.Title, "Security fix") {
		t.Errorf("PR title should contain 'Security fix', got %q", prParams.Title)
	}

	// Body should be redacted.
	if strings.Contains(prParams.Body, "vulnerability details") {
		t.Errorf("PR body should be redacted, got %q", prParams.Body)
	}
	if !strings.Contains(prParams.Body, "redacted") {
		t.Errorf("PR body should mention redaction, got %q", prParams.Body)
	}
}

// --- Co-author attribution ---

func TestExecuteNewTicket_CoAuthorAttribution(t *testing.T) {
	d := newTestDeps(t)
	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:     key,
			Summary: "Fix bug",
			Type:    "Bug",
			Assignee: &models.Author{
				Name:  "Jane Doe",
				Email: "jane@example.com",
			},
			Components: []string{},
			Labels:     []string{},
		}, nil
	}

	var receivedCoAuthor *models.Author
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, coAuthor *models.Author, _ []string) (string, error) {
		receivedCoAuthor = coAuthor
		return "abc123", nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedCoAuthor == nil {
		t.Fatal("expected co-author to be set")
	}
	if receivedCoAuthor.Name != "Jane Doe" {
		t.Errorf("co-author name = %q, want Jane Doe", receivedCoAuthor.Name)
	}
	if receivedCoAuthor.Email != "jane@example.com" {
		t.Errorf("co-author email = %q, want jane@example.com", receivedCoAuthor.Email)
	}
}

func TestExecuteNewTicket_NoAssignee_NilCoAuthor(t *testing.T) {
	d := newTestDeps(t)

	var receivedCoAuthor *models.Author
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, coAuthor *models.Author, _ []string) (string, error) {
		receivedCoAuthor = coAuthor
		return "abc123", nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedCoAuthor != nil {
		t.Errorf("expected nil co-author, got %+v", receivedCoAuthor)
	}
}

// --- Error comments disabled ---

func TestExecuteNewTicket_ErrorCommentsDisabled(t *testing.T) {
	d := newTestDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil // trigger failure
	}

	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:                "org",
			Repo:                 "repo",
			CloneURL:             "https://github.com/org/repo.git",
			BaseBranch:           "main",
			InProgressStatus:     "In Progress",
			InReviewStatus:       "In Review",
			TodoStatus:           "To Do",
			DisableErrorComments: true,
		}, nil
	}

	commentPosted := false
	d.tracker.AddCommentFunc = func(key, body string) error {
		commentPosted = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil {
		t.Fatal("expected error")
	}
	if commentPosted {
		t.Error("expected no error comment when disabled")
	}
}

// --- Workspace reuse (retry scenario) ---

func TestExecuteNewTicket_WorkspaceReused_SwitchesBranch(t *testing.T) {
	d := newTestDeps(t)
	d.workspaces.FindOrCreateFunc = func(ticketKey, repoURL string) (string, bool, error) {
		return d.wsDir, true, nil // reused
	}
	d.git.RemoteBranchExistsFunc = func(owner, repo, branch string) (bool, error) {
		return true, nil // remote branch still exists
	}

	branchSwitched := false
	d.git.SwitchBranchFunc = func(dir, name string) error {
		branchSwitched = true
		if !strings.Contains(name, "PROJ-1") {
			t.Errorf("branch name = %q, should contain ticket key", name)
		}
		return nil
	}

	branchCreated := false
	d.git.CreateBranchFunc = func(dir, name string) error {
		branchCreated = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !branchSwitched {
		t.Error("expected SwitchBranch to be called on reuse")
	}
	if branchCreated {
		t.Error("expected CreateBranch NOT to be called on reuse")
	}
}

func TestExecuteNewTicket_WorkspaceReused_RemoteBranchDeleted_RecreatesBranch(t *testing.T) {
	d := newTestDeps(t)
	d.workspaces.FindOrCreateFunc = func(ticketKey, repoURL string) (string, bool, error) {
		return d.wsDir, true, nil // reused
	}
	d.git.RemoteBranchExistsFunc = func(owner, repo, branch string) (bool, error) {
		return false, nil // remote branch was deleted
	}

	branchSwitched := false
	d.git.SwitchBranchFunc = func(dir, name string) error {
		branchSwitched = true
		return nil
	}

	branchCreated := false
	d.git.CreateBranchFunc = func(dir, name string) error {
		branchCreated = true
		if !strings.Contains(name, "PROJ-1") {
			t.Errorf("branch name = %q, should contain ticket key", name)
		}
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branchSwitched {
		t.Error("expected SwitchBranch NOT to be called when remote branch deleted")
	}
	if !branchCreated {
		t.Error("expected CreateBranch to be called when remote branch deleted")
	}
}

// --- PR URL field ---

func TestExecuteNewTicket_PRURLViaField(t *testing.T) {
	d := newTestDeps(t)
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			PRURLFieldName:   "Git Pull Request",
		}, nil
	}

	var fieldSet bool
	d.tracker.SetFieldValueFunc = func(key, field, value string) error {
		if field == "Git Pull Request" && strings.Contains(value, "github.com") {
			fieldSet = true
		}
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fieldSet {
		t.Error("expected PR URL to be set via field")
	}
}

// --- Container stopped on all paths ---

func TestExecuteNewTicket_ContainerStoppedOnSuccess(t *testing.T) {
	d := newTestDeps(t)

	stopped := false
	d.containers.StopFunc = func(ctx context.Context, ctr *container.Container) error {
		stopped = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Error("expected container to be stopped")
	}
}

func TestExecuteNewTicket_ContainerStoppedOnFailure(t *testing.T) {
	d := newTestDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil // trigger failure
	}

	stopped := false
	d.containers.StopFunc = func(ctx context.Context, ctr *container.Container) error {
		stopped = true
		return nil
	}

	p := d.pipeline(t)
	_, _ = p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if !stopped {
		t.Error("expected container to be stopped on failure")
	}
}

// --- Session cost ---

func TestExecuteNewTicket_SessionCostCaptured(t *testing.T) {
	d := newTestDeps(t)

	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		writeSessionOutput(t, d.wsDir, executor.SessionOutput{
			ExitCode: 0,
			CostUSD:  1.25,
		})
		return "", 0, nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CostUSD != 1.25 {
		t.Errorf("CostUSD = %f, want 1.25", result.CostUSD)
	}
}

// --- Branch naming ---

func TestExecuteNewTicket_BranchNameFormat(t *testing.T) {
	d := newTestDeps(t)

	var branchName string
	d.git.CreateBranchFunc = func(dir, name string) error {
		branchName = name
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-123"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branchName != "ai-bot/PROJ-123" {
		t.Errorf("branch name = %q, want ai-bot/PROJ-123", branchName)
	}
}

// --- PR title prefix from repo config ---

func TestExecuteNewTicket_TitlePrefixFromRepoConfig(t *testing.T) {
	d := newTestDeps(t)

	cfgDir := filepath.Join(d.wsDir, ".ai-bot")
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgContent := "pr:\n  title_prefix: \"[AI]\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var prTitle string
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prTitle = params.Title
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(prTitle, "[AI] ") {
		t.Errorf("PR title = %q, should start with [AI]", prTitle)
	}
}

// --- Provider resolution ---

func TestExecuteNewTicket_ProjectOverridesDefaultProvider(t *testing.T) {
	d := newTestDeps(t)
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			AIProvider:       "gemini",
		}, nil
	}

	var envVars map[string]string
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		envVars = env
		return &container.Container{ID: "c1", Name: "test"}, nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		AIAPIKeys:       map[string]string{"claude": "c-key", "gemini": "g-key"},
	})
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if envVars["AI_PROVIDER"] != "gemini" {
		t.Errorf("AI_PROVIDER = %q, want gemini", envVars["AI_PROVIDER"])
	}
	if _, ok := envVars["GEMINI_API_KEY"]; !ok {
		t.Error("expected GEMINI_API_KEY in container env")
	}
}

// --- ErrNoChanges handling ---

func TestExecuteNewTicket_ErrNoChanges_ReturnsError(t *testing.T) {
	d := newTestDeps(t)
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "", services.ErrNoChanges
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil {
		t.Fatal("expected error when CommitChanges returns ErrNoChanges")
	}
	if !strings.Contains(err.Error(), "no committable changes") {
		t.Errorf("error = %q, should mention 'no committable changes'", err.Error())
	}
}

// --- Auth strip/restore ---

func TestExecuteNewTicket_AuthStrippedBeforeAI(t *testing.T) {
	d := newTestDeps(t)

	var order []string
	d.git.StripRemoteAuthFunc = func(dir string) error {
		order = append(order, "strip")
		return nil
	}
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		order = append(order, "restore")
		return nil
	}
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		order = append(order, "exec")
		return "", 0, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ordering: strip must happen before exec, restore after exec.
	stripIdx, execIdx, restoreIdx := -1, -1, -1
	for i, v := range order {
		switch v {
		case "strip":
			stripIdx = i
		case "exec":
			execIdx = i
		case "restore":
			if restoreIdx == -1 {
				restoreIdx = i
			}
		}
	}

	if stripIdx < 0 {
		t.Fatal("StripRemoteAuth was not called")
	}
	if execIdx < 0 {
		t.Fatal("container Exec was not called")
	}
	if restoreIdx < 0 {
		t.Fatal("RestoreRemoteAuth was not called")
	}
	if stripIdx >= execIdx {
		t.Error("StripRemoteAuth must be called before container Exec")
	}
	if execIdx >= restoreIdx {
		t.Error("RestoreRemoteAuth must be called after container Exec")
	}
}

func TestExecuteNewTicket_AuthRestoredOnExecFailure(t *testing.T) {
	d := newTestDeps(t)

	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		return "", 1, errors.New("exec failed")
	}

	restored := false
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		restored = true
		return nil
	}

	p := d.pipeline(t)
	_, _ = p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if !restored {
		t.Error("RestoreRemoteAuth should be called even when exec fails")
	}
}

// --- Container settings override ---

func TestExecuteNewTicket_ContainerSettingsPassedToResolve(t *testing.T) {
	d := newTestDeps(t)
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Container: models.ContainerSettings{
				Image: "project-image:v1",
				ResourceLimits: models.ContainerResourceLimits{
					Memory: "16g",
				},
			},
		}, nil
	}

	var receivedOverride *container.SettingsOverride
	d.containers.ResolveConfigFunc = func(repoDir string, projectOverride *container.SettingsOverride) (*container.Config, error) {
		receivedOverride = projectOverride
		return &container.Config{Image: "project-image:v1"}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedOverride == nil {
		t.Fatal("expected project override to be passed to ResolveConfig")
	}
	if receivedOverride.Image != "project-image:v1" {
		t.Errorf("override image = %q, want %q", receivedOverride.Image, "project-image:v1")
	}
	if receivedOverride.Limits.Memory != "16g" {
		t.Errorf("override memory = %q, want %q", receivedOverride.Limits.Memory, "16g")
	}
}

func TestExecuteNewTicket_EmptyContainerSettingsNoOverride(t *testing.T) {
	d := newTestDeps(t)

	var receivedOverride *container.SettingsOverride
	d.containers.ResolveConfigFunc = func(repoDir string, projectOverride *container.SettingsOverride) (*container.Config, error) {
		receivedOverride = projectOverride
		return &container.Config{Image: "default:latest"}, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedOverride != nil {
		t.Error("expected nil override when project has no container settings")
	}
}

// --- helpers ---

// --- mergeImports ---

func TestMergeImports_ProjectOnly(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/workflows", Path: ".workflows", Ref: "main"},
		},
	}
	repoCfg := repoconfig.Default()

	result := executor.MergeImports(settings, repoCfg)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Repo != "https://github.com/org/workflows" {
		t.Errorf("Repo = %q, want %q", result[0].Repo, "https://github.com/org/workflows")
	}
	if result[0].Path != ".workflows" {
		t.Errorf("Path = %q, want %q", result[0].Path, ".workflows")
	}
}

func TestMergeImports_RepoOnly(t *testing.T) {
	settings := &models.ProjectSettings{}
	repoCfg := &repoconfig.Config{
		ValidationCommands: []string{},
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/org/tools", Path: ".tools"},
		},
		PR: repoconfig.PRConfig{Labels: []string{}},
	}

	result := executor.MergeImports(settings, repoCfg)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Path != ".tools" {
		t.Errorf("Path = %q, want %q", result[0].Path, ".tools")
	}
}

func TestMergeImports_RepoOverridesProjectOnPathConflict(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/workflows-v1", Path: ".workflows", Ref: "v1"},
		},
	}
	repoCfg := &repoconfig.Config{
		ValidationCommands: []string{},
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/team/workflows-v2", Path: ".workflows", Ref: "v2"},
		},
		PR: repoconfig.PRConfig{Labels: []string{}},
	}

	result := executor.MergeImports(settings, repoCfg)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	// Repo-level should win.
	if result[0].Repo != "https://github.com/team/workflows-v2" {
		t.Errorf("Repo = %q, want repo-level override", result[0].Repo)
	}
	if result[0].Ref != "v2" {
		t.Errorf("Ref = %q, want %q", result[0].Ref, "v2")
	}
}

func TestMergeImports_BothSourcesDifferentPaths(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/alpha", Path: ".alpha"},
		},
	}
	repoCfg := &repoconfig.Config{
		ValidationCommands: []string{},
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/org/beta", Path: ".beta"},
		},
		PR: repoconfig.PRConfig{Labels: []string{}},
	}

	result := executor.MergeImports(settings, repoCfg)

	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	// Sorted by path.
	if result[0].Path != ".alpha" {
		t.Errorf("result[0].Path = %q, want .alpha", result[0].Path)
	}
	if result[1].Path != ".beta" {
		t.Errorf("result[1].Path = %q, want .beta", result[1].Path)
	}
}

func TestMergeImports_Empty(t *testing.T) {
	settings := &models.ProjectSettings{}
	repoCfg := repoconfig.Default()

	result := executor.MergeImports(settings, repoCfg)

	if len(result) != 0 {
		t.Fatalf("len(result) = %d, want 0", len(result))
	}
}

func TestMergeImports_PathNormalized(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/a", Path: ".workflows/"},
		},
	}
	repoCfg := &repoconfig.Config{
		ValidationCommands: []string{},
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/org/b", Path: ".workflows"},
		},
		PR: repoconfig.PRConfig{Labels: []string{}},
	}

	result := executor.MergeImports(settings, repoCfg)

	// Trailing slash should be normalized, so both map to same path.
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1 (trailing slash normalized)", len(result))
	}
	// Repo-level wins.
	if result[0].Repo != "https://github.com/org/b" {
		t.Errorf("Repo = %q, want repo-level", result[0].Repo)
	}
}

// --- Clone imports via pipeline ---

func TestExecuteNewTicket_ClonesImports(t *testing.T) {
	d := newTestDeps(t)

	// Configure project-level imports.
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Imports: []models.ImportConfig{
				{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main"},
			},
		}, nil
	}

	var clonedURL, clonedDest, clonedRef string
	d.git.CloneImportFunc = func(url, destDir, ref string) error {
		clonedURL = url
		clonedDest = destDir
		clonedRef = ref
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if clonedURL != "https://github.com/org/workflows" {
		t.Errorf("CloneImport URL = %q, want %q", clonedURL, "https://github.com/org/workflows")
	}
	expectedDest := filepath.Join(d.wsDir, ".ai-workflows")
	if clonedDest != expectedDest {
		t.Errorf("CloneImport destDir = %q, want %q", clonedDest, expectedDest)
	}
	if clonedRef != "main" {
		t.Errorf("CloneImport ref = %q, want %q", clonedRef, "main")
	}
}

func TestExecuteNewTicket_SkipsExistingImportDir(t *testing.T) {
	d := newTestDeps(t)

	// Pre-create the import directory (simulates workspace reuse).
	importDir := filepath.Join(d.wsDir, ".ai-workflows")
	if err := os.MkdirAll(importDir, 0o750); err != nil {
		t.Fatal(err)
	}

	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Imports: []models.ImportConfig{
				{Repo: "https://github.com/org/workflows", Path: ".ai-workflows"},
			},
		}, nil
	}

	cloneCalled := false
	d.git.CloneImportFunc = func(url, destDir, ref string) error {
		cloneCalled = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cloneCalled {
		t.Error("CloneImport should not be called when directory exists")
	}
}

func TestExecuteNewTicket_ImportCloneFailure(t *testing.T) {
	d := newTestDeps(t)

	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Imports: []models.ImportConfig{
				{Repo: "https://github.com/org/broken", Path: ".broken"},
			},
		}, nil
	}

	d.git.CloneImportFunc = func(url, destDir, ref string) error {
		return errors.New("clone failed: repository not found")
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil {
		t.Fatal("expected error when import clone fails")
	}
	if !strings.Contains(err.Error(), "clone import") {
		t.Errorf("error = %q, should mention clone import", err.Error())
	}
}

func TestExecuteNewTicket_NoImports(t *testing.T) {
	d := newTestDeps(t)

	cloneCalled := false
	d.git.CloneImportFunc = func(url, destDir, ref string) error {
		cloneCalled = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cloneCalled {
		t.Error("CloneImport should not be called when no imports configured")
	}
}

// --- runImportInstalls ---

func TestRunImportInstalls_RunsInstallCommands(t *testing.T) {
	d := newTestDeps(t)

	var execCmds [][]string
	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, cmd []string) (string, int, error) {
		execCmds = append(execCmds, cmd)
		return "", 0, nil
	}

	p := d.pipeline(t)
	ctr := &container.Container{ID: "c1", Name: "test-c1"}
	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main", Install: ".ai-workflows/install.sh"},
		{Repo: "https://github.com/org/tools", Path: "tools", Ref: "", Install: "tools/setup.sh --flag"},
	}

	err := p.RunImportInstalls(context.Background(), zap.NewNop(), ctr, imports)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(execCmds) != 2 {
		t.Fatalf("expected 2 exec calls, got %d", len(execCmds))
	}

	// Verify command structure: sh -c "cd /workspace && <install>"
	wantCmd0 := []string{"sh", "-c", "cd /workspace && .ai-workflows/install.sh"}
	if !equalSlice(execCmds[0], wantCmd0) {
		t.Errorf("exec cmd[0] = %v, want %v", execCmds[0], wantCmd0)
	}
	wantCmd1 := []string{"sh", "-c", "cd /workspace && tools/setup.sh --flag"}
	if !equalSlice(execCmds[1], wantCmd1) {
		t.Errorf("exec cmd[1] = %v, want %v", execCmds[1], wantCmd1)
	}
}

func TestRunImportInstalls_SkipsEmptyInstall(t *testing.T) {
	d := newTestDeps(t)

	execCalled := false
	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, _ []string) (string, int, error) {
		execCalled = true
		return "", 0, nil
	}

	p := d.pipeline(t)
	ctr := &container.Container{ID: "c1", Name: "test-c1"}
	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main"},
		{Repo: "https://github.com/org/tools", Path: "tools", Ref: "v1"},
	}

	err := p.RunImportInstalls(context.Background(), zap.NewNop(), ctr, imports)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if execCalled {
		t.Error("Exec should not be called when no import has an install command")
	}
}

func TestRunImportInstalls_ExecError(t *testing.T) {
	d := newTestDeps(t)

	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, _ []string) (string, int, error) {
		return "", 0, errors.New("exec failed")
	}

	p := d.pipeline(t)
	ctr := &container.Container{ID: "c1", Name: "test-c1"}
	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Install: "./install.sh"},
	}

	err := p.RunImportInstalls(context.Background(), zap.NewNop(), ctr, imports)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "install command for import .ai-workflows") {
		t.Errorf("error should reference import path, got: %v", err)
	}
}

func TestRunImportInstalls_NonZeroExitCode(t *testing.T) {
	d := newTestDeps(t)

	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, _ []string) (string, int, error) {
		return "some output", 1, nil
	}

	p := d.pipeline(t)
	ctr := &container.Container{ID: "c1", Name: "test-c1"}
	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Install: "./install.sh"},
	}

	err := p.RunImportInstalls(context.Background(), zap.NewNop(), ctr, imports)
	if err == nil {
		t.Fatal("expected error on non-zero exit code")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("error should include exit code, got: %v", err)
	}
}

func TestRunImportInstalls_NilImports(t *testing.T) {
	d := newTestDeps(t)
	p := d.pipeline(t)
	ctr := &container.Container{ID: "c1", Name: "test-c1"}

	err := p.RunImportInstalls(context.Background(), zap.NewNop(), ctr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeImports_InstallFieldPropagated(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main", Install: "proj-install.sh"},
		},
	}
	repoCfg := &repoconfig.Config{
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/org/tools", Path: "tools", Ref: "v1", Install: "tools/setup.sh"},
		},
	}

	result := executor.MergeImports(settings, repoCfg)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	// Sorted by path: .ai-workflows before tools.
	if result[0].Install != "proj-install.sh" {
		t.Errorf("result[0].Install = %q, want %q", result[0].Install, "proj-install.sh")
	}
	if result[1].Install != "tools/setup.sh" {
		t.Errorf("result[1].Install = %q, want %q", result[1].Install, "tools/setup.sh")
	}
}

func TestMergeImports_RepoOverridesInstall(t *testing.T) {
	settings := &models.ProjectSettings{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Install: "old-install.sh"},
		},
	}
	repoCfg := &repoconfig.Config{
		Imports: []repoconfig.Import{
			{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Install: "new-install.sh"},
		},
	}

	result := executor.MergeImports(settings, repoCfg)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].Install != "new-install.sh" {
		t.Errorf("Install = %q, want %q (repo-level should override)", result[0].Install, "new-install.sh")
	}
}

func TestExecuteNewTicket_RunsImportInstallAfterContainerStart(t *testing.T) {
	d := newTestDeps(t)

	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Imports: []models.ImportConfig{
				{Repo: "https://github.com/org/wf", Path: ".ai-workflows", Ref: "main", Install: ".ai-workflows/install.sh"},
			},
		}, nil
	}

	d.git.CloneImportFunc = func(_, _, _ string) error { return nil }

	// Track the order: container start, then install exec, then AI exec.
	var events []string
	d.containers.StartFunc = func(_ context.Context, _ *container.Config, _, _ string, _ map[string]string) (*container.Container, error) {
		events = append(events, "start")
		return &container.Container{ID: "c1", Name: "test-c1"}, nil
	}
	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, cmd []string) (string, int, error) {
		if len(cmd) == 3 && strings.Contains(cmd[2], "install.sh") {
			events = append(events, "install")
		} else {
			events = append(events, "ai-exec")
		}
		return "", 0, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ordering: start → install → ai-exec.
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(events), events)
	}
	if events[0] != "start" {
		t.Errorf("events[0] = %q, want start", events[0])
	}
	if events[1] != "install" {
		t.Errorf("events[1] = %q, want install", events[1])
	}
	if events[2] != "ai-exec" {
		t.Errorf("events[2] = %q, want ai-exec", events[2])
	}
}

func TestExecuteNewTicket_ImportInstallFailure_StopsContainer(t *testing.T) {
	d := newTestDeps(t)

	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			Imports: []models.ImportConfig{
				{Repo: "https://github.com/org/wf", Path: ".ai-workflows", Install: "install.sh"},
			},
		}, nil
	}

	d.git.CloneImportFunc = func(_, _, _ string) error { return nil }

	d.containers.ExecFunc = func(_ context.Context, _ *container.Container, cmd []string) (string, int, error) {
		if len(cmd) == 3 && strings.Contains(cmd[2], "install.sh") {
			return "install failed", 1, nil
		}
		return "", 0, nil
	}

	stopCalled := false
	d.containers.StopFunc = func(_ context.Context, _ *container.Container) error {
		stopCalled = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err == nil {
		t.Fatal("expected error from install failure")
	}
	if !strings.Contains(err.Error(), "import install") {
		t.Errorf("error should mention import install, got: %v", err)
	}
	if !stopCalled {
		t.Error("container should be stopped on install failure")
	}
}

// --- readPRDescription ---

func TestReadPRDescription_FileExists(t *testing.T) {
	dir := t.TempDir()
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	content := "Fix NPE in UserService\n\n## Summary\nAdded null check for photo field.\n"
	if err := os.WriteFile(filepath.Join(aiBotDir, "pr.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	pr := executor.ReadPRDescription(dir)
	if pr == nil {
		t.Fatal("expected non-nil PRDescription")
	}
	if pr.Title != "Fix NPE in UserService" {
		t.Errorf("Title = %q, want %q", pr.Title, "Fix NPE in UserService")
	}
	if !strings.Contains(pr.Body, "Added null check") {
		t.Errorf("Body should contain 'Added null check', got: %s", pr.Body)
	}
}

func TestReadPRDescription_FileMissing(t *testing.T) {
	dir := t.TempDir()
	pr := executor.ReadPRDescription(dir)
	if pr != nil {
		t.Errorf("expected nil when file missing, got %+v", pr)
	}
}

func TestReadPRDescription_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aiBotDir, "pr.md"), []byte("  \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pr := executor.ReadPRDescription(dir)
	if pr != nil {
		t.Errorf("expected nil for whitespace-only file, got %+v", pr)
	}
}

func TestReadPRDescription_TitleOnly(t *testing.T) {
	dir := t.TempDir()
	aiBotDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aiBotDir, "pr.md"), []byte("Just a title\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pr := executor.ReadPRDescription(dir)
	if pr == nil {
		t.Fatal("expected non-nil PRDescription")
	}
	if pr.Title != "Just a title" {
		t.Errorf("Title = %q, want %q", pr.Title, "Just a title")
	}
	if pr.Body != "" {
		t.Errorf("Body = %q, want empty", pr.Body)
	}
}

func TestReadPRDescription_StripsMarkdownHeading(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"h1 prefix", "# Fix the bug\n\nBody text.", "Fix the bug"},
		{"h2 prefix", "## Fix the bug\n\nBody text.", "Fix the bug"},
		{"h1 with ticket key", "# EDM-2747: Fix compose apps\n\nBody.", "EDM-2747: Fix compose apps"},
		{"no heading", "Fix the bug\n\nBody text.", "Fix the bug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			aiBotDir := filepath.Join(dir, ".ai-bot")
			if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(aiBotDir, "pr.md"), []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			pr := executor.ReadPRDescription(dir)
			if pr == nil {
				t.Fatal("expected non-nil PRDescription")
			}
			if pr.Title != tt.want {
				t.Errorf("Title = %q, want %q", pr.Title, tt.want)
			}
		})
	}
}

// --- buildPRContent ---

func TestBuildPRContent_Default(t *testing.T) {
	workItem := &models.WorkItem{
		Key:         "PROJ-1",
		Summary:     "Fix bug",
		Description: "Detailed description.",
	}
	title, body := executor.BuildPRContent(workItem, "PROJ-1", "", nil)
	if title != "PROJ-1: Fix bug" {
		t.Errorf("Title = %q, want %q", title, "PROJ-1: Fix bug")
	}
	if !strings.Contains(body, "Resolves PROJ-1") {
		t.Error("body should contain 'Resolves PROJ-1'")
	}
	if !strings.Contains(body, "Detailed description.") {
		t.Error("body should contain description")
	}
}

func TestBuildPRContent_WithAIPR(t *testing.T) {
	workItem := &models.WorkItem{Key: "PROJ-1", Summary: "Fix bug"}
	aiPR := &executor.PRDescription{
		Title: "Fix null pointer in getProfile()",
		Body:  "## Summary\nAdded null check.",
	}
	title, body := executor.BuildPRContent(workItem, "PROJ-1", "", aiPR)
	if title != "PROJ-1: Fix null pointer in getProfile()" {
		t.Errorf("Title = %q, want %q", title, "PROJ-1: Fix null pointer in getProfile()")
	}
	if !strings.Contains(body, "Added null check") {
		t.Error("body should contain AI-generated content")
	}
	// Jira description should NOT be present.
	if strings.Contains(body, "Fix bug") {
		t.Error("body should not contain Jira summary when AI PR is used")
	}
}

func TestBuildPRContent_SecurityLevelIgnoresAIPR(t *testing.T) {
	workItem := &models.WorkItem{
		Key:           "SEC-1",
		Summary:       "Fix auth bypass",
		SecurityLevel: "Embargoed",
	}
	aiPR := &executor.PRDescription{
		Title: "Fix auth bypass in login handler",
		Body:  "The auth handler skips validation when...",
	}
	title, body := executor.BuildPRContent(workItem, "SEC-1", "", aiPR)
	if !strings.Contains(title, "Security fix") {
		t.Errorf("Title should be redacted, got: %s", title)
	}
	if strings.Contains(body, "auth bypass") {
		t.Error("body should not contain AI content for security-level tickets")
	}
	if !strings.Contains(body, "redacted") {
		t.Error("body should mention redaction")
	}
}

func TestBuildPRContent_AIPREmptyTitle_FallsBack(t *testing.T) {
	workItem := &models.WorkItem{Key: "PROJ-1", Summary: "Fix bug"}
	aiPR := &executor.PRDescription{Title: "", Body: "some body"}
	title, _ := executor.BuildPRContent(workItem, "PROJ-1", "", aiPR)
	if title != "PROJ-1: Fix bug" {
		t.Errorf("Title = %q, want Jira fallback %q", title, "PROJ-1: Fix bug")
	}
}

func TestBuildPRContent_AITitleWithTicketKey(t *testing.T) {
	workItem := &models.WorkItem{Key: "EDM-2747", Summary: "Fix bug"}
	aiPR := &executor.PRDescription{
		Title: "EDM-2747: Differentiate manual stop from completion",
		Body:  "Root cause analysis.",
	}
	title, _ := executor.BuildPRContent(workItem, "EDM-2747", "", aiPR)
	want := "EDM-2747: Differentiate manual stop from completion"
	if title != want {
		t.Errorf("Title = %q, want %q (no duplicate key)", title, want)
	}
}

func TestBuildPRContent_WithTitlePrefix(t *testing.T) {
	workItem := &models.WorkItem{Key: "PROJ-1", Summary: "Fix bug"}
	aiPR := &executor.PRDescription{Title: "AI title", Body: "AI body"}
	title, _ := executor.BuildPRContent(workItem, "PROJ-1", "[bot]", aiPR)
	if title != "[bot] PROJ-1: AI title" {
		t.Errorf("Title = %q, want %q", title, "[bot] PROJ-1: AI title")
	}
}

// --- Attachment download ---

func TestExecuteNewTicket_DownloadsAttachments(t *testing.T) {
	d := newTestDeps(t)

	// Return work item with attachments.
	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:        key,
			Summary:    "Bug with logs",
			Type:       "Bug",
			Components: []string{},
			Labels:     []string{},
			Attachments: []models.Attachment{
				{Filename: "crash.log", MimeType: "text/plain", Size: 100, URL: "https://jira.example.com/att/1"},
				{Filename: "config.yaml", MimeType: "text/yaml", Size: 50, URL: "https://jira.example.com/att/2"},
			},
		}, nil
	}

	// Track download calls.
	var downloadedURLs []string
	d.tracker.DownloadAttachmentFunc = func(url string) ([]byte, error) {
		downloadedURLs = append(downloadedURLs, url)
		if strings.Contains(url, "att/1") {
			return []byte("stack trace here"), nil
		}
		return []byte("key: value"), nil
	}

	// Capture WriteIssue call to verify attachment files are passed.
	var gotAttachments []string
	d.taskWriter.WriteIssueFunc = func(workItem models.WorkItem, dir string, attachmentFiles []string) error {
		gotAttachments = attachmentFiles
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-ATT"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify both attachments were downloaded.
	if len(downloadedURLs) != 2 {
		t.Fatalf("expected 2 downloads, got %d", len(downloadedURLs))
	}

	// Verify WriteIssue received the attachment filenames.
	if len(gotAttachments) != 2 {
		t.Fatalf("expected 2 attachment files, got %d: %v", len(gotAttachments), gotAttachments)
	}
	if gotAttachments[0] != "crash.log" || gotAttachments[1] != "config.yaml" {
		t.Errorf("attachment files = %v, want [crash.log config.yaml]", gotAttachments)
	}

	// Verify files exist on disk.
	for _, name := range []string{"crash.log", "config.yaml"} {
		path := filepath.Join(d.wsDir, ".ai-bot", "attachments", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected attachment file %s to exist: %v", name, err)
		}
	}
}

func TestExecuteNewTicket_SkipsLargeAttachments(t *testing.T) {
	d := newTestDeps(t)

	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:        key,
			Summary:    "Large file",
			Type:       "Bug",
			Components: []string{},
			Labels:     []string{},
			Attachments: []models.Attachment{
				{Filename: "small.log", MimeType: "text/plain", Size: 100, URL: "https://jira.example.com/att/1"},
				{Filename: "huge.bin", MimeType: "application/octet-stream", Size: 10 << 20, URL: "https://jira.example.com/att/2"},
			},
		}, nil
	}

	var downloadedURLs []string
	d.tracker.DownloadAttachmentFunc = func(url string) ([]byte, error) {
		downloadedURLs = append(downloadedURLs, url)
		return []byte("data"), nil
	}

	var gotAttachments []string
	d.taskWriter.WriteIssueFunc = func(_ models.WorkItem, _ string, attachmentFiles []string) error {
		gotAttachments = attachmentFiles
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-BIG"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the small file should have been downloaded.
	if len(downloadedURLs) != 1 {
		t.Fatalf("expected 1 download, got %d", len(downloadedURLs))
	}
	if len(gotAttachments) != 1 || gotAttachments[0] != "small.log" {
		t.Errorf("attachment files = %v, want [small.log]", gotAttachments)
	}
}

func TestExecuteNewTicket_NoAttachments_NoSection(t *testing.T) {
	d := newTestDeps(t)

	var gotAttachments []string
	d.taskWriter.WriteIssueFunc = func(_ models.WorkItem, _ string, attachmentFiles []string) error {
		gotAttachments = attachmentFiles
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-NONE"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No attachments means nil passed to WriteIssue.
	if gotAttachments != nil {
		t.Errorf("expected nil attachment files, got %v", gotAttachments)
	}
}

func TestExecuteNewTicket_SanitizesAttachmentFilename(t *testing.T) {
	d := newTestDeps(t)

	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:        key,
			Summary:    "Path traversal test",
			Type:       "Bug",
			Components: []string{},
			Labels:     []string{},
			Attachments: []models.Attachment{
				{Filename: "../../etc/passwd", MimeType: "text/plain", Size: 50, URL: "https://jira.example.com/att/1"},
			},
		}, nil
	}

	d.tracker.DownloadAttachmentFunc = func(string) ([]byte, error) {
		return []byte("safe content"), nil
	}

	var gotAttachments []string
	d.taskWriter.WriteIssueFunc = func(_ models.WorkItem, _ string, attachmentFiles []string) error {
		gotAttachments = attachmentFiles
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-SEC"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filename should be sanitized to just "passwd".
	if len(gotAttachments) != 1 || gotAttachments[0] != "passwd" {
		t.Errorf("attachment files = %v, want [passwd]", gotAttachments)
	}

	// File should be in the attachments dir, not in ../../etc/.
	path := filepath.Join(d.wsDir, ".ai-bot", "attachments", "passwd")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected sanitized file at %s: %v", path, err)
	}
}

func TestExecuteNewTicket_SkipsExistingAttachments(t *testing.T) {
	d := newTestDeps(t)

	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:        key,
			Summary:    "Rerun with cached attachments",
			Type:       "Bug",
			Components: []string{},
			Labels:     []string{},
			Attachments: []models.Attachment{
				{Filename: "existing.log", MimeType: "text/plain", Size: 100, URL: "https://jira.example.com/att/1"},
				{Filename: "new.log", MimeType: "text/plain", Size: 80, URL: "https://jira.example.com/att/2"},
			},
		}, nil
	}

	// Pre-create the first attachment on disk.
	attDir := filepath.Join(d.wsDir, ".ai-bot", "attachments")
	if err := os.MkdirAll(attDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, "existing.log"), []byte("old data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Track download calls — only new.log should be downloaded.
	var downloadedURLs []string
	d.tracker.DownloadAttachmentFunc = func(url string) ([]byte, error) {
		downloadedURLs = append(downloadedURLs, url)
		return []byte("new data"), nil
	}

	var gotAttachments []string
	d.taskWriter.WriteIssueFunc = func(_ models.WorkItem, _ string, attachmentFiles []string) error {
		gotAttachments = attachmentFiles
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-CACHE"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the new attachment should have been downloaded.
	if len(downloadedURLs) != 1 {
		t.Fatalf("expected 1 download, got %d: %v", len(downloadedURLs), downloadedURLs)
	}
	if !strings.Contains(downloadedURLs[0], "att/2") {
		t.Errorf("expected att/2 to be downloaded, got %s", downloadedURLs[0])
	}

	// Both attachments should be reported (existing + new).
	if len(gotAttachments) != 2 {
		t.Fatalf("expected 2 attachment files, got %d: %v", len(gotAttachments), gotAttachments)
	}
	if gotAttachments[0] != "existing.log" || gotAttachments[1] != "new.log" {
		t.Errorf("attachment files = %v, want [existing.log new.log]", gotAttachments)
	}
}

// --- Fork-based workflow tests ---

func TestNewTicketPipeline_ForkMode(t *testing.T) {
	d := newTestDeps(t)

	// Configure fork mode via GitHubUsername.
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "upstream-org",
			Repo:             "repo",
			CloneURL:         "https://github.com/upstream-org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			GitHubUsername:   "bot-fork",
		}, nil
	}

	// Capture arguments to verify fork owner is used.
	var commitOwner, commitRepo string
	d.git.CommitChangesFunc = func(owner, repo, branch, message, dir string, coAuthor *models.Author, importExcludes []string) (string, error) {
		commitOwner = owner
		commitRepo = repo
		return "abc123", nil
	}

	var restoreOwner, restoreRepo string
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		restoreOwner = owner
		restoreRepo = repo
		return nil
	}

	var prParams models.PRParams
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prParams = params
		return &models.PR{Number: 42, URL: "https://github.com/upstream-org/repo/pull/42"}, nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newTicketJob("PROJ-123"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify CommitChanges uses fork owner.
	if commitOwner != "bot-fork" {
		t.Errorf("CommitChanges owner = %q, want bot-fork", commitOwner)
	}
	if commitRepo != "repo" {
		t.Errorf("CommitChanges repo = %q, want repo", commitRepo)
	}

	// Verify RestoreRemoteAuth uses fork owner.
	if restoreOwner != "bot-fork" {
		t.Errorf("RestoreRemoteAuth owner = %q, want bot-fork", restoreOwner)
	}
	if restoreRepo != "repo" {
		t.Errorf("RestoreRemoteAuth repo = %q, want repo", restoreRepo)
	}

	// Verify PR params: Owner/Repo should be upstream, Head should be "bot-fork:branch".
	if prParams.Owner != "upstream-org" {
		t.Errorf("PR Owner = %q, want upstream-org", prParams.Owner)
	}
	if prParams.Repo != "repo" {
		t.Errorf("PR Repo = %q, want repo", prParams.Repo)
	}
	if !strings.HasPrefix(prParams.Head, "bot-fork:") {
		t.Errorf("PR Head = %q, should start with bot-fork:", prParams.Head)
	}
	if !strings.Contains(prParams.Head, "PROJ-123") {
		t.Errorf("PR Head = %q, should contain ticket key", prParams.Head)
	}

	// Verify result is valid.
	if result.PRURL == "" {
		t.Error("expected non-empty PRURL")
	}
}

func TestPrepareBranch_ForkMode(t *testing.T) {
	d := newTestDeps(t)

	// Configure fork mode.
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "upstream-org",
			Repo:             "repo",
			CloneURL:         "https://github.com/upstream-org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			GitHubUsername:   "bot-fork",
		}, nil
	}

	// Workspace is reused and remote branch exists.
	d.workspaces.FindOrCreateFunc = func(ticketKey, repoURL string) (string, bool, error) {
		return d.wsDir, true, nil // reused
	}

	var remoteCheckOwner, remoteCheckRepo string
	d.git.RemoteBranchExistsFunc = func(owner, repo, branch string) (bool, error) {
		remoteCheckOwner = owner
		remoteCheckRepo = repo
		return true, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify RemoteBranchExists was called with fork owner.
	if remoteCheckOwner != "bot-fork" {
		t.Errorf("RemoteBranchExists owner = %q, want bot-fork", remoteCheckOwner)
	}
	if remoteCheckRepo != "repo" {
		t.Errorf("RemoteBranchExists repo = %q, want repo", remoteCheckRepo)
	}
}

func TestFeedbackPipeline_ForkMode(t *testing.T) {
	d := newTestDeps(t)

	// Configure fork mode.
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "upstream-org",
			Repo:             "repo",
			CloneURL:         "https://github.com/upstream-org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			GitHubUsername:   "bot-fork",
		}, nil
	}

	// Track the head format passed to GetPRForBranch.
	var prHead string
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		prHead = head
		return &models.PRDetails{
			Number: 42,
			Title:  "Fix a bug",
			Branch: "ai-bot/PROJ-1",
			URL:    "https://github.com/upstream-org/repo/pull/42",
		}, nil
	}

	d.git.GetPRCommentsFunc = func(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		}, nil
	}

	// Track RestoreRemoteAuth and FetchRemote calls.
	var restoreCalled, fetchCalled bool
	var restoreOwner, restoreRepo string
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		if !restoreCalled {
			// First call is the fork setup.
			restoreCalled = true
			restoreOwner = owner
			restoreRepo = repo
		}
		return nil
	}
	d.git.FetchRemoteFunc = func(dir string) error {
		fetchCalled = true
		return nil
	}

	// Track CommitChanges owner.
	var commitOwner, commitRepo string
	d.git.CommitChangesFunc = func(owner, repo, branch, message, dir string, coAuthor *models.Author, importExcludes []string) (string, error) {
		commitOwner = owner
		commitRepo = repo
		return "abc123", nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), &jobmanager.Job{
		ID: "j1", TicketKey: "PROJ-1", Type: jobmanager.JobTypeFeedback,
		AttemptNum: 1,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify GetPRForBranch received "bot-fork:ai-bot/PROJ-1" head format.
	if prHead != "bot-fork:ai-bot/PROJ-1" {
		t.Errorf("GetPRForBranch head = %q, want bot-fork:ai-bot/PROJ-1", prHead)
	}

	// Verify RestoreRemoteAuth was called with fork owner before switching branch.
	if !restoreCalled {
		t.Fatal("RestoreRemoteAuth should be called to set fork remote")
	}
	if restoreOwner != "bot-fork" {
		t.Errorf("RestoreRemoteAuth owner = %q, want bot-fork", restoreOwner)
	}
	if restoreRepo != "repo" {
		t.Errorf("RestoreRemoteAuth repo = %q, want repo", restoreRepo)
	}

	// Verify FetchRemote was called.
	if !fetchCalled {
		t.Error("FetchRemote should be called in fork mode")
	}

	// Verify CommitChanges uses fork owner.
	if commitOwner != "bot-fork" {
		t.Errorf("CommitChanges owner = %q, want bot-fork", commitOwner)
	}
	if commitRepo != "repo" {
		t.Errorf("CommitChanges repo = %q, want repo", commitRepo)
	}
}

func TestFeedbackPipeline_NonForkMode_SkipsFetchRemote(t *testing.T) {
	d := newTestDeps(t)

	// No GitHubUsername set (non-fork mode).
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "org",
			Repo:             "repo",
			CloneURL:         "https://github.com/org/repo.git",
			BaseBranch:       "main",
			InProgressStatus: "In Progress",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
		}, nil
	}

	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number: 42,
			Title:  "Fix a bug",
			Branch: "ai-bot/PROJ-1",
			URL:    "https://github.com/org/repo/pull/42",
		}, nil
	}

	d.git.GetPRCommentsFunc = func(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		}, nil
	}

	// Track FetchRemote — should NOT be called in non-fork mode.
	fetchCalled := false
	d.git.FetchRemoteFunc = func(dir string) error {
		fetchCalled = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), &jobmanager.Job{
		ID: "j1", TicketKey: "PROJ-1", Type: jobmanager.JobTypeFeedback,
		AttemptNum: 1,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fetchCalled {
		t.Error("FetchRemote should NOT be called in non-fork mode")
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// testDeps holds the stub dependencies used by test cases. Individual
// tests override specific Func fields to control behavior.
type testDeps struct {
	tracker    *trackertest.Stub
	git        *executortest.StubGitService
	containers *containertest.StubManager
	workspaces *workspacetest.Stub
	taskWriter *taskfiletest.Stub
	projects   *executortest.StubProjectResolver
	wsDir      string
}

func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	wsDir := t.TempDir()

	return &testDeps{
		tracker: &trackertest.Stub{
			GetWorkItemFunc: func(key string) (*models.WorkItem, error) {
				return &models.WorkItem{
					Key:        key,
					Summary:    "Fix a bug",
					Type:       "Bug",
					Components: []string{},
					Labels:     []string{},
				}, nil
			},
		},
		git: &executortest.StubGitService{
			HasChangesFunc: func(dir string) (bool, error) {
				return true, nil
			},
			CommitChangesFunc: func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
				return "abc123", nil
			},
			CreatePRFunc: func(params models.PRParams) (*models.PR, error) {
				return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
			},
		},
		containers: &containertest.StubManager{
			ResolveConfigFunc: func(repoDir string, projectOverride *container.SettingsOverride) (*container.Config, error) {
				return &container.Config{Image: "test:latest"}, nil
			},
			StartFunc: func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
				return &container.Container{ID: "c1", Name: "test-c1"}, nil
			},
		},
		workspaces: &workspacetest.Stub{
			FindOrCreateFunc: func(ticketKey, repoURL string) (string, bool, error) {
				return wsDir, false, nil
			},
		},
		taskWriter: &taskfiletest.Stub{},
		projects: &executortest.StubProjectResolver{
			ResolveProjectFunc: func(workItem models.WorkItem) (*models.ProjectSettings, error) {
				return &models.ProjectSettings{
					Owner:            "org",
					Repo:             "repo",
					CloneURL:         "https://github.com/org/repo.git",
					BaseBranch:       "main",
					InProgressStatus: "In Progress",
					InReviewStatus:   "In Review",
					TodoStatus:       "To Do",
				}, nil
			},
		},
		wsDir: wsDir,
	}
}

func (d *testDeps) pipeline(t *testing.T) *executor.Pipeline {
	t.Helper()
	return d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		AIAPIKeys:       map[string]string{"claude": "test-key"},
	})
}

func (d *testDeps) pipelineWithConfig(t *testing.T, cfg executor.Config) *executor.Pipeline {
	t.Helper()
	p, err := executor.NewPipeline(cfg,
		d.tracker, d.git, d.containers, d.workspaces,
		d.taskWriter, d.projects, zap.NewNop())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return p
}

func newTicketJob(ticketKey string) *jobmanager.Job {
	return &jobmanager.Job{
		ID:         "job-1",
		TicketKey:  ticketKey,
		Type:       jobmanager.JobTypeNewTicket,
		AttemptNum: 1,
	}
}

func writeSessionOutput(t *testing.T, wsDir string, output executor.SessionOutput) {
	t.Helper()
	dir := filepath.Join(wsDir, ".ai-bot")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session-output.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
