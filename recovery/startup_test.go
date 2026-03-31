package recovery_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/recovery"
	"jira-ai-issue-solver/recovery/recoverytest"
)

// --- NewStartupRunner validation ---

func TestNewStartupRunner_RejectsEmptyBotUsername(t *testing.T) {
	d := newDeps()
	_, err := recovery.NewStartupRunner(
		recovery.Config{}, d.tracker, d.git, d.workspaces,
		d.containers, d.jobs, d.projects, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for empty bot username")
	}
}

func TestNewStartupRunner_RejectsNilDependencies(t *testing.T) {
	cfg := recovery.Config{BotUsername: "bot"}
	deps := []struct {
		name string
		fn   func() error
	}{
		{"tracker", func() error {
			_, err := recovery.NewStartupRunner(cfg, nil,
				&recoverytest.StubGitService{}, &recoverytest.StubWorkspaceCleaner{},
				&recoverytest.StubContainerCleaner{}, &recoverytest.StubJobSubmitter{},
				&recoverytest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"git", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				nil, &recoverytest.StubWorkspaceCleaner{},
				&recoverytest.StubContainerCleaner{}, &recoverytest.StubJobSubmitter{},
				&recoverytest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"workspaces", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				&recoverytest.StubGitService{}, nil,
				&recoverytest.StubContainerCleaner{}, &recoverytest.StubJobSubmitter{},
				&recoverytest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"containers", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				&recoverytest.StubGitService{}, &recoverytest.StubWorkspaceCleaner{},
				nil, &recoverytest.StubJobSubmitter{},
				&recoverytest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"jobs", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				&recoverytest.StubGitService{}, &recoverytest.StubWorkspaceCleaner{},
				&recoverytest.StubContainerCleaner{}, nil,
				&recoverytest.StubProjectResolver{}, zap.NewNop())
			return err
		}},
		{"projects", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				&recoverytest.StubGitService{}, &recoverytest.StubWorkspaceCleaner{},
				&recoverytest.StubContainerCleaner{}, &recoverytest.StubJobSubmitter{},
				nil, zap.NewNop())
			return err
		}},
		{"logger", func() error {
			_, err := recovery.NewStartupRunner(cfg, &recoverytest.StubIssueTracker{},
				&recoverytest.StubGitService{}, &recoverytest.StubWorkspaceCleaner{},
				&recoverytest.StubContainerCleaner{}, &recoverytest.StubJobSubmitter{},
				&recoverytest.StubProjectResolver{}, nil)
			return err
		}},
	}

	for _, d := range deps {
		if d.fn() == nil {
			t.Errorf("expected error for nil %s", d.name)
		}
	}
}

func TestNewStartupRunner_ValidConfig(t *testing.T) {
	d := newDeps()
	r, err := recovery.NewStartupRunner(
		recovery.Config{BotUsername: "bot"}, d.tracker, d.git,
		d.workspaces, d.containers, d.jobs, d.projects, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

// --- No orphans / no stuck tickets ---

func TestRun_NoOrphansNoStuckTickets(t *testing.T) {
	d := newDeps()
	r := d.runner(t)

	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Orphaned containers cleaned ---

func TestRun_OrphanedContainersCleaned(t *testing.T) {
	d := newDeps()
	var cleanedPrefix string
	d.containers.CleanupOrphansFunc = func(ctx context.Context, prefix string) error {
		cleanedPrefix = prefix
		return nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if cleanedPrefix != "ai-bot-" {
		t.Errorf("prefix = %q, want ai-bot-", cleanedPrefix)
	}
}

func TestRun_CustomContainerPrefix(t *testing.T) {
	d := newDeps()
	var cleanedPrefix string
	d.containers.CleanupOrphansFunc = func(ctx context.Context, prefix string) error {
		cleanedPrefix = prefix
		return nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername:     "bot",
		ContainerPrefix: "my-bot-",
	})
	_ = r.Run(context.Background())

	if cleanedPrefix != "my-bot-" {
		t.Errorf("prefix = %q, want my-bot-", cleanedPrefix)
	}
}

func TestRun_OrphanCleanupFailureNonFatal(t *testing.T) {
	d := newDeps()
	d.containers.CleanupOrphansFunc = func(ctx context.Context, prefix string) error {
		return errors.New("permission denied")
	}

	r := d.runner(t)
	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error (non-fatal), got %v", err)
	}
}

// --- Stuck ticket with PR (case 1: complete transition) ---

func TestRun_StuckTicketWithPR_CompletesTransition(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-1", Summary: "Fix bug", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number: 42, Title: "Fix bug", Branch: head,
			URL: "https://github.com/org/repo/pull/42",
		}, nil
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var commentPosted string
	d.tracker.AddCommentFunc = func(key, body string) error {
		commentPosted = body
		return nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// Verify transition to in-review.
	if len(transitions) != 1 || transitions[0] != "In Review" {
		t.Errorf("transitions = %v, want [In Review]", transitions)
	}

	// Verify PR URL posted as comment (no PRURLFieldName configured).
	if !strings.Contains(commentPosted, "https://github.com/org/repo/pull/42") {
		t.Errorf("comment = %q, should contain PR URL", commentPosted)
	}
}

func TestRun_StuckTicketWithPR_PRURLViaField(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-1", Summary: "Fix bug", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number: 42, URL: "https://github.com/org/repo/pull/42",
		}, nil
	}
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:          "org",
			Repo:           "repo",
			BaseBranch:     "main",
			InReviewStatus: "In Review",
			TodoStatus:     "To Do",
			PRURLFieldName: "Git Pull Request",
		}, nil
	}

	var fieldSet bool
	d.tracker.SetFieldValueFunc = func(key, field, value string) error {
		if field == "Git Pull Request" && strings.Contains(value, "github.com") {
			fieldSet = true
		}
		return nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if !fieldSet {
		t.Error("expected PR URL to be set via field")
	}
}

// --- Stuck ticket with commits but no PR (case 2: create PR) ---

func TestRun_StuckTicketWithCommitsNoPR_CreatesPR(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-2", Summary: "Add feature", Type: "Story",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	// No PR found.
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}
	// Branch has commits.
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return true, nil
	}

	var prParams models.PRParams
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prParams = params
		return &models.PR{Number: 10, URL: "https://github.com/org/repo/pull/10"}, nil
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// Verify PR was created.
	if prParams.Owner != "org" || prParams.Repo != "repo" {
		t.Errorf("PR owner/repo = %s/%s, want org/repo", prParams.Owner, prParams.Repo)
	}
	if !strings.Contains(prParams.Title, "PROJ-2") {
		t.Errorf("PR title = %q, should contain ticket key", prParams.Title)
	}
	if prParams.Base != "main" {
		t.Errorf("PR base = %q, want main", prParams.Base)
	}
	if !strings.Contains(prParams.Head, "PROJ-2") {
		t.Errorf("PR head = %q, should contain ticket key", prParams.Head)
	}

	// Verify transition to in-review.
	if len(transitions) != 1 || transitions[0] != "In Review" {
		t.Errorf("transitions = %v, want [In Review]", transitions)
	}
}

func TestRun_StuckTicketWithCommitsNoPR_SecurityRedacted(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "SEC-1", Summary: "Fix auth bypass", Type: "Bug",
				SecurityLevel: "Embargoed",
				Components:    []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return true, nil
	}

	var prParams models.PRParams
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		prParams = params
		return &models.PR{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if strings.Contains(prParams.Title, "auth bypass") {
		t.Errorf("PR title should be redacted, got %q", prParams.Title)
	}
	if !strings.Contains(prParams.Title, "Security fix") {
		t.Errorf("PR title should contain 'Security fix', got %q", prParams.Title)
	}
	if strings.Contains(prParams.Body, "auth bypass") {
		t.Errorf("PR body should be redacted, got %q", prParams.Body)
	}
}

func TestRun_StuckTicketWithCommitsNoPR_CreatePRFails_LeavesInProgress(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-3", Summary: "Broken", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return true, nil
	}
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		return nil, errors.New("API rate limited")
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var comments []string
	d.tracker.AddCommentFunc = func(key, body string) error {
		comments = append(comments, body)
		return nil
	}

	submitted := false
	d.jobs.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// Verify NO status transition (ticket stays in-progress).
	if len(transitions) != 0 {
		t.Errorf("transitions = %v, want none (leave in-progress)", transitions)
	}

	// Verify NOT re-queued (to avoid data loss).
	if submitted {
		t.Error("expected ticket NOT to be re-queued when commits exist")
	}

	// Verify recovery comment was posted.
	if len(comments) != 1 {
		t.Fatalf("comments = %d, want 1 recovery comment", len(comments))
	}
	if !strings.Contains(comments[0], "[AI-BOT-RECOVERY]") {
		t.Errorf("comment = %q, should contain [AI-BOT-RECOVERY]", comments[0])
	}
	if !strings.Contains(comments[0], "ai-bot/PROJ-3") {
		t.Errorf("comment = %q, should contain branch name ai-bot/PROJ-3", comments[0])
	}
	if !strings.Contains(comments[0], "Manual PR creation required") {
		t.Errorf("comment = %q, should contain manual intervention message", comments[0])
	}
}

// --- Stuck ticket with no PR and no commits (case 3: revert and re-queue) ---

func TestRun_StuckTicketNoPRNoCommits_RevertsAndRequeues(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-4", Summary: "New task", Type: "Story",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return false, nil
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var submitted []jobmanager.Event
	d.jobs.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		submitted = append(submitted, event)
		return &jobmanager.Job{}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// Verify reverted to todo.
	if len(transitions) != 1 || transitions[0] != "To Do" {
		t.Errorf("transitions = %v, want [To Do]", transitions)
	}

	// Verify re-queued as new ticket.
	if len(submitted) != 1 {
		t.Fatalf("submitted = %v, want 1 event", submitted)
	}
	if submitted[0].TicketKey != "PROJ-4" {
		t.Errorf("submitted ticket = %q, want PROJ-4", submitted[0].TicketKey)
	}
	if submitted[0].Type != jobmanager.JobTypeNewTicket {
		t.Errorf("submitted type = %q, want new_ticket", submitted[0].Type)
	}
}

// --- BranchHasCommits error ---

func TestRun_BranchHasCommitsError_RevertsAndRequeues(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-5", Summary: "Task", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return false, errors.New("network error")
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	var submitted []string
	d.jobs.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		submitted = append(submitted, event.TicketKey)
		return &jobmanager.Job{}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if len(transitions) != 1 || transitions[0] != "To Do" {
		t.Errorf("transitions = %v, want [To Do]", transitions)
	}
	if len(submitted) != 1 || submitted[0] != "PROJ-5" {
		t.Errorf("submitted = %v, want [PROJ-5]", submitted)
	}
}

// --- Project resolution failure ---

func TestRun_ProjectResolutionFails_SkipsTicket(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-6", Summary: "Task", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return nil, errors.New("unknown project")
	}

	submitted := false
	d.jobs.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if submitted {
		t.Error("expected ticket to be skipped, not re-queued")
	}
}

// --- Search failure ---

func TestRun_SearchFailure_NonFatal(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return nil, errors.New("Jira unreachable")
	}

	r := d.runner(t)
	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error (non-fatal), got %v", err)
	}
}

// --- Stale workspace cleanup ---

func TestRun_StaleWorkspacesCleaned(t *testing.T) {
	d := newDeps()
	var cleanedAge time.Duration
	d.workspaces.CleanupStaleFunc = func(maxAge time.Duration) (int, error) {
		cleanedAge = maxAge
		return 3, nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername:  "bot",
		WorkspaceTTL: 7 * 24 * time.Hour,
	})
	_ = r.Run(context.Background())

	if cleanedAge != 7*24*time.Hour {
		t.Errorf("maxAge = %v, want 7 days", cleanedAge)
	}
}

func TestRun_ZeroTTL_SkipsStaleCleanup(t *testing.T) {
	d := newDeps()
	called := false
	d.workspaces.CleanupStaleFunc = func(maxAge time.Duration) (int, error) {
		called = true
		return 0, nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername:  "bot",
		WorkspaceTTL: 0,
	})
	_ = r.Run(context.Background())

	if called {
		t.Error("expected CleanupStale not to be called with zero TTL")
	}
}

// --- Terminal workspace cleanup ---

func TestRun_TerminalWorkspacesCleaned(t *testing.T) {
	d := newDeps()
	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		switch key {
		case "PROJ-DONE":
			return &models.WorkItem{Key: key, Status: "Done",
				Components: []string{}, Labels: []string{}}, nil
		case "PROJ-ACTIVE":
			return &models.WorkItem{Key: key, Status: "In Progress",
				Components: []string{}, Labels: []string{}}, nil
		default:
			return nil, errors.New("not found")
		}
	}

	var removedKeys []string
	d.workspaces.CleanupByFilterFunc = func(shouldRemove func(ticketKey string) bool) (int, error) {
		// Simulate scanning workspaces.
		for _, key := range []string{"PROJ-DONE", "PROJ-ACTIVE", "PROJ-DELETED"} {
			if shouldRemove(key) {
				removedKeys = append(removedKeys, key)
			}
		}
		return len(removedKeys), nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername: "bot",
		ActiveStatuses: map[string]bool{
			"To Do":       true,
			"In Progress": true,
			"In Review":   true,
		},
	})
	_ = r.Run(context.Background())

	// PROJ-DONE should be removed (not in active statuses).
	// PROJ-ACTIVE should be kept (In Progress is active).
	// PROJ-DELETED should be removed (GetWorkItem returns error).
	expected := map[string]bool{"PROJ-DONE": true, "PROJ-DELETED": true}
	if len(removedKeys) != len(expected) {
		t.Fatalf("removed = %v, want %v", removedKeys, expected)
	}
	for _, key := range removedKeys {
		if !expected[key] {
			t.Errorf("unexpectedly removed %q", key)
		}
	}
}

func TestRun_NoActiveStatuses_SkipsTerminalCleanup(t *testing.T) {
	d := newDeps()
	called := false
	d.workspaces.CleanupByFilterFunc = func(shouldRemove func(ticketKey string) bool) (int, error) {
		called = true
		return 0, nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername: "bot",
		// ActiveStatuses not set.
	})
	_ = r.Run(context.Background())

	if called {
		t.Error("expected CleanupByFilter not to be called without active statuses")
	}
}

func TestRun_ContextCancelled_StopsTerminalWorkspaceCleanup(t *testing.T) {
	d := newDeps()

	ctx, cancel := context.WithCancel(context.Background())

	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		if key == "PROJ-2" {
			// Cancel context after the first workspace is evaluated.
			cancel()
		}
		return &models.WorkItem{Key: key, Status: "Done",
			Components: []string{}, Labels: []string{}}, nil
	}

	var evaluatedKeys []string
	d.workspaces.CleanupByFilterFunc = func(shouldRemove func(ticketKey string) bool) (int, error) {
		removed := 0
		for _, key := range []string{"PROJ-1", "PROJ-2", "PROJ-3"} {
			if shouldRemove(key) {
				evaluatedKeys = append(evaluatedKeys, key)
				removed++
			}
		}
		return removed, nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername: "bot",
		ActiveStatuses: map[string]bool{
			"In Progress": true,
		},
	})
	_ = r.Run(ctx)

	// PROJ-1 should be removed (Done is not active).
	// PROJ-2 triggers cancellation during its GetWorkItem call.
	// PROJ-3 should NOT be evaluated (context already cancelled).
	if len(evaluatedKeys) > 2 {
		t.Errorf("evaluated %d workspaces after cancellation, want at most 2", len(evaluatedKeys))
	}
}

// --- Mixed scenario ---

func TestRun_MixedScenario(t *testing.T) {
	d := newDeps()

	// Two stuck tickets: one with PR, one without.
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-A", Summary: "Has PR", Type: "Bug",
				Components: []string{}, Labels: []string{}},
			{Key: "PROJ-B", Summary: "No work done", Type: "Story",
				Components: []string{}, Labels: []string{}},
		}, nil
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		if strings.Contains(head, "PROJ-A") {
			return &models.PRDetails{
				Number: 1, URL: "https://github.com/org/repo/pull/1",
			}, nil
		}
		return nil, errors.New("no open PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return false, nil
	}

	transitions := make(map[string][]string)
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions[key] = append(transitions[key], status)
		return nil
	}

	var submitted []string
	d.jobs.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		submitted = append(submitted, event.TicketKey)
		return &jobmanager.Job{}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// PROJ-A: PR exists → transition to in-review.
	if ts := transitions["PROJ-A"]; len(ts) != 1 || ts[0] != "In Review" {
		t.Errorf("PROJ-A transitions = %v, want [In Review]", ts)
	}

	// PROJ-B: No PR, no commits → revert to todo and re-queue.
	if ts := transitions["PROJ-B"]; len(ts) != 1 || ts[0] != "To Do" {
		t.Errorf("PROJ-B transitions = %v, want [To Do]", ts)
	}
	if len(submitted) != 1 || submitted[0] != "PROJ-B" {
		t.Errorf("submitted = %v, want [PROJ-B]", submitted)
	}
}

// --- Context cancellation ---

func TestRun_ContextCancelled_StopsRecovery(t *testing.T) {
	d := newDeps()

	processed := 0
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-1", Summary: "First", Type: "Bug",
				Components: []string{}, Labels: []string{}},
			{Key: "PROJ-2", Summary: "Second", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		processed++
		if processed >= 1 {
			cancel() // Cancel after first ticket.
		}
		return &models.PRDetails{Number: 1, URL: "https://github.com/org/repo/pull/1"}, nil
	}

	r := d.runner(t)
	_ = r.Run(ctx)

	// Exactly 1 ticket should have been processed before cancellation stopped the loop.
	if processed != 1 {
		t.Errorf("processed %d tickets, want exactly 1 (cancelled before second)", processed)
	}
}

// --- Workspace cleanup failure is non-fatal ---

func TestRun_WorkspaceCleanupFailures_NonFatal(t *testing.T) {
	d := newDeps()
	d.workspaces.CleanupByFilterFunc = func(shouldRemove func(ticketKey string) bool) (int, error) {
		return 0, errors.New("filesystem error")
	}
	d.workspaces.CleanupStaleFunc = func(maxAge time.Duration) (int, error) {
		return 0, errors.New("filesystem error")
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername:  "bot",
		WorkspaceTTL: 24 * time.Hour,
		ActiveStatuses: map[string]bool{
			"To Do": true,
		},
	})

	err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error (non-fatal), got %v", err)
	}
}

// --- Branch name format ---

func TestRun_BranchNameFormat(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-99", Summary: "Test", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}

	var queriedBranch string
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		queriedBranch = head
		return nil, errors.New("no PR")
	}
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		return false, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	if queriedBranch != "ai-bot/PROJ-99" {
		t.Errorf("queried branch = %q, want ai-bot/PROJ-99", queriedBranch)
	}
}

// --- Fork-based workflow ---

func TestRun_ForkMode_UsesSettingsMethods(t *testing.T) {
	d := newDeps()
	d.tracker.SearchWorkItemsFunc = func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-100", Summary: "Fork test", Type: "Bug",
				Components: []string{}, Labels: []string{}},
		}, nil
	}

	// Return settings with GitHubUsername set.
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:            "upstream-org",
			Repo:             "repo",
			BaseBranch:       "main",
			InReviewStatus:   "In Review",
			TodoStatus:       "To Do",
			InProgressStatus: "In Progress",
			GitHubUsername:   "contributor-gh",
		}, nil
	}

	// No PR found. BranchHasCommits should receive contributor-gh as owner.
	var branchOwner string
	d.git.BranchHasCommitsFunc = func(owner, repo, branch, base string) (bool, error) {
		branchOwner = owner
		return true, nil
	}

	// GetPRForBranch should receive "contributor-gh:ai-bot/PROJ-100" as head.
	var prHead string
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		prHead = head
		return nil, errors.New("no PR")
	}

	// CreatePR should receive "contributor-gh:ai-bot/PROJ-100" as Head.
	var createdPRHead string
	d.git.CreatePRFunc = func(params models.PRParams) (*models.PR, error) {
		createdPRHead = params.Head
		return &models.PR{Number: 10, URL: "https://github.com/upstream-org/repo/pull/10"}, nil
	}

	r := d.runner(t)
	_ = r.Run(context.Background())

	// Verify GetPRForBranch received owner-prefixed head.
	if prHead != "contributor-gh:ai-bot/PROJ-100" {
		t.Errorf("GetPRForBranch head = %q, want contributor-gh:ai-bot/PROJ-100", prHead)
	}

	// Verify BranchHasCommits received fork owner.
	if branchOwner != "contributor-gh" {
		t.Errorf("BranchHasCommits owner = %q, want contributor-gh", branchOwner)
	}

	// Verify CreatePR received owner-prefixed head.
	if createdPRHead != "contributor-gh:ai-bot/PROJ-100" {
		t.Errorf("CreatePR head = %q, want contributor-gh:ai-bot/PROJ-100", createdPRHead)
	}
}

// --- InProgressCriteria passed through ---

func TestRun_PassesInProgressCriteria(t *testing.T) {
	d := newDeps()
	criteria := models.SearchCriteria{
		ProjectKeys: []string{"PROJ"},
		StatusByType: map[string][]string{
			"Bug":   {"Development"},
			"Story": {"In Progress"},
		},
		ContributorIsCurrentUser: true,
	}

	var receivedCriteria models.SearchCriteria
	d.tracker.SearchWorkItemsFunc = func(c models.SearchCriteria) ([]models.WorkItem, error) {
		receivedCriteria = c
		return []models.WorkItem{}, nil
	}

	r := d.runnerWithConfig(t, recovery.Config{
		BotUsername:        "ai-bot",
		InProgressCriteria: criteria,
	})
	_ = r.Run(context.Background())

	if !receivedCriteria.ContributorIsCurrentUser {
		t.Error("ContributorIsCurrentUser should be true")
	}
	if len(receivedCriteria.ProjectKeys) != 1 || receivedCriteria.ProjectKeys[0] != "PROJ" {
		t.Errorf("ProjectKeys = %v, want [PROJ]", receivedCriteria.ProjectKeys)
	}
}

// --- helpers ---

type deps struct {
	tracker    *recoverytest.StubIssueTracker
	git        *recoverytest.StubGitService
	workspaces *recoverytest.StubWorkspaceCleaner
	containers *recoverytest.StubContainerCleaner
	jobs       *recoverytest.StubJobSubmitter
	projects   *recoverytest.StubProjectResolver
}

func newDeps() *deps {
	return &deps{
		tracker:    &recoverytest.StubIssueTracker{},
		git:        &recoverytest.StubGitService{},
		workspaces: &recoverytest.StubWorkspaceCleaner{},
		containers: &recoverytest.StubContainerCleaner{},
		jobs:       &recoverytest.StubJobSubmitter{},
		projects: &recoverytest.StubProjectResolver{
			ResolveProjectFunc: func(workItem models.WorkItem) (*models.ProjectSettings, error) {
				return &models.ProjectSettings{
					Owner:          "org",
					Repo:           "repo",
					BaseBranch:     "main",
					InReviewStatus: "In Review",
					TodoStatus:     "To Do",
				}, nil
			},
		},
	}
}

func (d *deps) runner(t *testing.T) *recovery.StartupRunner {
	t.Helper()
	return d.runnerWithConfig(t, recovery.Config{BotUsername: "ai-bot"})
}

func (d *deps) runnerWithConfig(t *testing.T, cfg recovery.Config) *recovery.StartupRunner {
	t.Helper()
	r, err := recovery.NewStartupRunner(cfg,
		d.tracker, d.git, d.workspaces, d.containers,
		d.jobs, d.projects, zap.NewNop())
	if err != nil {
		t.Fatalf("NewStartupRunner: %v", err)
	}
	return r
}
