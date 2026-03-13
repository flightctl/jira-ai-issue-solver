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

// --- Container start failure with fallback ---

func TestExecuteNewTicket_ContainerStartFailsFallbackSucceeds(t *testing.T) {
	d := newTestDeps(t)

	startAttempt := 0
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		startAttempt++
		if startAttempt == 1 {
			return nil, errors.New("image not found")
		}
		// Second attempt (fallback) succeeds.
		return &container.Container{ID: "c1", Name: "fallback-c1"}, nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		FallbackImage:   "ubuntu:latest",
		AIAPIKeys:       map[string]string{"claude": "test-key"},
	})

	result, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if startAttempt != 2 {
		t.Errorf("start attempts = %d, want 2", startAttempt)
	}
	if result.PRURL == "" {
		t.Error("expected PR to be created")
	}
}

func TestExecuteNewTicket_ContainerStartFailsNoFallback(t *testing.T) {
	d := newTestDeps(t)
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		return nil, errors.New("image not found")
	}

	// No fallback image configured (empty FallbackImage).
	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newTicketJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "start container") {
		t.Fatalf("expected container start error, got %v", err)
	}
}

// --- Commit failure ---

func TestExecuteNewTicket_CommitFails(t *testing.T) {
	d := newTestDeps(t)
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author) (string, error) {
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
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, coAuthor *models.Author) (string, error) {
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
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, coAuthor *models.Author) (string, error) {
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
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author) (string, error) {
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

// --- excludeImportPaths ---

func TestExcludeImportPaths_WritesExcludeFile(t *testing.T) {
	wsDir := t.TempDir()
	gitInfoDir := filepath.Join(wsDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o750); err != nil {
		t.Fatal(err)
	}

	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main"},
		{Repo: "https://github.com/org/tools", Path: "vendor-tools", Ref: "v1"},
	}

	if err := executor.ExcludeImportPaths(wsDir, imports); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(gitInfoDir, "exclude")) // #nosec G304 -- test reads from t.TempDir()
	if err != nil {
		t.Fatalf("failed to read exclude file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "/.ai-workflows/") {
		t.Errorf("exclude file missing .ai-workflows pattern, got: %s", content)
	}
	if !strings.Contains(content, "/vendor-tools/") {
		t.Errorf("exclude file missing vendor-tools pattern, got: %s", content)
	}
}

func TestExcludeImportPaths_SkipsDuplicates(t *testing.T) {
	wsDir := t.TempDir()
	gitInfoDir := filepath.Join(wsDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Pre-populate exclude file with one pattern.
	if err := os.WriteFile(
		filepath.Join(gitInfoDir, "exclude"),
		[]byte("/.ai-workflows/\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main"},
		{Repo: "https://github.com/org/tools", Path: "new-tools", Ref: ""},
	}

	if err := executor.ExcludeImportPaths(wsDir, imports); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(gitInfoDir, "exclude")) // #nosec G304 -- test reads from t.TempDir()
	if err != nil {
		t.Fatalf("failed to read exclude file: %v", err)
	}

	content := string(data)
	// Should appear exactly once (not duplicated).
	if strings.Count(content, "/.ai-workflows/") != 1 {
		t.Errorf("expected .ai-workflows once, got:\n%s", content)
	}
	// New pattern should be added.
	if !strings.Contains(content, "/new-tools/") {
		t.Errorf("exclude file missing new-tools pattern, got:\n%s", content)
	}
}

func TestExcludeImportPaths_NoImports_NoOp(t *testing.T) {
	wsDir := t.TempDir()
	gitInfoDir := filepath.Join(wsDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := executor.ExcludeImportPaths(wsDir, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exclude file should not be created.
	if _, err := os.Stat(filepath.Join(gitInfoDir, "exclude")); !os.IsNotExist(err) {
		t.Error("exclude file should not be created when there are no imports")
	}
}

func TestExcludeImportPaths_CreatesExcludeFileIfMissing(t *testing.T) {
	wsDir := t.TempDir()
	gitInfoDir := filepath.Join(wsDir, ".git", "info")
	if err := os.MkdirAll(gitInfoDir, 0o750); err != nil {
		t.Fatal(err)
	}

	imports := []executor.ImportEntry{
		{Repo: "https://github.com/org/repo", Path: "imported", Ref: ""},
	}

	if err := executor.ExcludeImportPaths(wsDir, imports); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(gitInfoDir, "exclude")) // #nosec G304 -- test reads from t.TempDir()
	if err != nil {
		t.Fatalf("failed to read exclude file: %v", err)
	}
	if !strings.Contains(string(data), "/imported/") {
		t.Errorf("exclude file missing pattern, got: %s", string(data))
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
			CommitChangesFunc: func(_, _, _, _, _ string, _ *models.Author) (string, error) {
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
