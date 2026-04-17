package scanner_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/scanner"
	"jira-ai-issue-solver/scanner/scannertest"
)

// --- Construction validation ---

func TestNewFeedbackScanner_Validation(t *testing.T) {
	validCfg := scanner.FeedbackScannerConfig{
		PollInterval: time.Minute,
		BotUsername:  "ai-bot",
	}

	tests := []struct {
		name      string
		searcher  scanner.IssueSearcher
		submitter scanner.JobSubmitter
		prs       scanner.PRFetcher
		repos     scanner.RepoLocator
		cfg       scanner.FeedbackScannerConfig
		logger    *zap.Logger
		wantErr   string
	}{
		{
			name: "nil searcher", searcher: nil,
			submitter: &scannertest.StubJobSubmitter{},
			prs:       &scannertest.StubPRFetcher{}, repos: &scannertest.StubRepoLocator{},
			cfg: validCfg, logger: zap.NewNop(), wantErr: "issue searcher",
		},
		{
			name: "nil submitter", searcher: &scannertest.StubIssueSearcher{},
			submitter: nil,
			prs:       &scannertest.StubPRFetcher{}, repos: &scannertest.StubRepoLocator{},
			cfg: validCfg, logger: zap.NewNop(), wantErr: "job submitter",
		},
		{
			name: "nil prs", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			prs:       nil, repos: &scannertest.StubRepoLocator{},
			cfg: validCfg, logger: zap.NewNop(), wantErr: "PR fetcher",
		},
		{
			name: "nil repos", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			prs:       &scannertest.StubPRFetcher{}, repos: nil,
			cfg: validCfg, logger: zap.NewNop(), wantErr: "repo locator",
		},
		{
			name: "zero poll", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			prs:       &scannertest.StubPRFetcher{}, repos: &scannertest.StubRepoLocator{},
			cfg: scanner.FeedbackScannerConfig{BotUsername: "bot"}, logger: zap.NewNop(),
			wantErr: "poll interval",
		},
		{
			name: "empty bot", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			prs:       &scannertest.StubPRFetcher{}, repos: &scannertest.StubRepoLocator{},
			cfg: scanner.FeedbackScannerConfig{PollInterval: time.Minute}, logger: zap.NewNop(),
			wantErr: "bot username",
		},
		{
			name: "nil logger", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			prs:       &scannertest.StubPRFetcher{}, repos: &scannertest.StubRepoLocator{},
			cfg: validCfg, logger: nil, wantErr: "logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scanner.NewFeedbackScanner(
				tt.searcher, tt.submitter, tt.prs, tt.repos, tt.cfg, tt.logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// --- Emits event for actionable comments ---

func TestFeedbackScanner_EmitsEventForActionableComments(t *testing.T) {
	d := newFeedbackDeps()
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		}, nil
	}

	var mu sync.Mutex
	var submitted []jobmanager.Event
	d.submitter.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		mu.Lock()
		submitted = append(submitted, event)
		mu.Unlock()
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if len(submitted) != 1 {
		t.Fatalf("submitted %d events, want 1", len(submitted))
	}
	if submitted[0].TicketKey != "PROJ-1" {
		t.Errorf("ticket = %q, want PROJ-1", submitted[0].TicketKey)
	}
	if submitted[0].Type != jobmanager.JobTypeFeedback {
		t.Errorf("type = %q, want feedback", submitted[0].Type)
	}
}

// --- No event when all comments addressed ---

func TestFeedbackScanner_NoEventWhenAllAddressed(t *testing.T) {
	d := newFeedbackDeps()
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
			{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
		}, nil
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when all comments are addressed")
	}
}

// --- No event when only ignored users ---

func TestFeedbackScanner_IgnoredUsersFiltered(t *testing.T) {
	d := newFeedbackDeps()
	d.cfg.IgnoredUsernames = []string{"packit"}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "packit[bot]"}, Body: "/build"},
		}, nil
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called for ignored users only")
	}
}

// --- Known bot loop filtered ---

func TestFeedbackScanner_KnownBotLoopFiltered(t *testing.T) {
	d := newFeedbackDeps()
	d.cfg.KnownBotUsernames = []string{"coderabbitai"}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
			{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
			// Only "new" comment is from a known bot replying to our bot.
			{ID: 3, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Also", InReplyTo: 2},
		}, nil
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when only actionable comment is a bot loop")
	}
}

// --- Thread depth filtered ---

func TestFeedbackScanner_ThreadDepthFiltered(t *testing.T) {
	d := newFeedbackDeps()
	d.cfg.MaxThreadDepth = 1
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
			{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
			// Depth at this comment is 1 (bot at ID 2), which equals max.
			{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Again", InReplyTo: 2},
		}, nil
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when comments exceed thread depth")
	}
}

// --- PR not found is skipped ---

func TestFeedbackScanner_PRNotFound_Skipped(t *testing.T) {
	d := newFeedbackDeps()
	d.prs.GetPRForBranchFunc = func(_, _, _ string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR")
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when PR not found")
	}
}

// --- Repo locate failure skipped ---

func TestFeedbackScanner_RepoLocateFailure_Skipped(t *testing.T) {
	d := newFeedbackDeps()
	d.repos.LocateRepoFunc = func(_ models.WorkItem) (string, string, error) {
		return "", "", errors.New("unknown component")
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when repo locate fails")
	}
}

// --- Comment fetch failure skipped ---

func TestFeedbackScanner_CommentFetchFailure_Skipped(t *testing.T) {
	d := newFeedbackDeps()
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return nil, errors.New("API error")
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when comment fetch fails")
	}
}

// --- Multiple tickets with mixed actionable ---

func TestFeedbackScanner_MultipleTickets_MixedActionable(t *testing.T) {
	d := newFeedbackDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{
			{Key: "PROJ-1"},
			{Key: "PROJ-2"},
		}, nil
	}

	d.prs.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{Number: 1, Branch: head, URL: "https://github.com/org/repo/pull/1"}, nil
	}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		}, nil
	}

	// Second ticket has no actionable comments.
	callCount := 0
	origComments := d.prs.GetPRCommentsFunc
	d.prs.GetPRCommentsFunc = func(owner, repo string, number int, _ time.Time) ([]models.PRComment, error) {
		callCount++
		if callCount == 2 {
			return []models.PRComment{
				{ID: 10, Author: models.Author{Username: "reviewer"}, Body: "Old"},
				{ID: 11, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 10},
			}, nil
		}
		return origComments(owner, repo, number, time.Time{})
	}

	var mu sync.Mutex
	var submitted []string
	d.submitter.SubmitFunc = func(event jobmanager.Event) (*jobmanager.Job, error) {
		mu.Lock()
		submitted = append(submitted, event.TicketKey)
		mu.Unlock()
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	// Only PROJ-1 should get an event (PROJ-2 is all addressed).
	if len(submitted) != 1 {
		t.Fatalf("submitted %d events, want 1", len(submitted))
	}
	if submitted[0] != "PROJ-1" {
		t.Errorf("submitted[0] = %q, want PROJ-1", submitted[0])
	}
}

// --- Circuit open stops scan cycle ---

func TestFeedbackScanner_CircuitOpenStopsCycle(t *testing.T) {
	d := newFeedbackDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{{Key: "PROJ-1"}, {Key: "PROJ-2"}}, nil
	}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		}, nil
	}

	var mu sync.Mutex
	var callCount int
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return nil, jobmanager.ErrCircuitOpen
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("Submit called %d times, want 1 (circuit open stops cycle)", callCount)
	}
}

// --- Budget exceeded stops scan cycle ---

func TestFeedbackScanner_BudgetExceededStopsCycle(t *testing.T) {
	d := newFeedbackDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{{Key: "PROJ-1"}, {Key: "PROJ-2"}}, nil
	}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		}, nil
	}

	var mu sync.Mutex
	var callCount int
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return nil, jobmanager.ErrBudgetExceeded
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("Submit called %d times, want 1 (budget exceeded stops cycle)", callCount)
	}
}

// --- Start/Stop lifecycle ---

func TestFeedbackScanner_StartWhileRunning(t *testing.T) {
	d := newFeedbackDeps()
	s := d.scanner(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := s.Start(ctx); err == nil {
		t.Fatal("expected error on double start")
	}

	s.Stop()
}

func TestFeedbackScanner_StopWithoutStart(t *testing.T) {
	d := newFeedbackDeps()
	s := d.scanner(t)
	// Should not panic.
	s.Stop()
}

func TestFeedbackScanner_RestartAfterStop(t *testing.T) {
	d := newFeedbackDeps()

	var mu sync.Mutex
	var scanCount int
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		mu.Lock()
		scanCount++
		mu.Unlock()
		return []models.WorkItem{}, nil
	}

	s := d.scanner(t)

	// First Start/Stop cycle.
	runOneFeedbackScan(t, s)

	mu.Lock()
	firstCount := scanCount
	mu.Unlock()
	if firstCount == 0 {
		t.Fatal("expected at least one scan in first cycle")
	}

	// Second Start/Stop cycle -- restart should work.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("restart failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	s.Stop()

	mu.Lock()
	secondCount := scanCount
	mu.Unlock()
	if secondCount <= firstCount {
		t.Errorf("expected additional scans after restart, got %d total (was %d)", secondCount, firstCount)
	}
}

// --- Branch name convention ---

func TestFeedbackScanner_UsesBranchConvention(t *testing.T) {
	d := newFeedbackDeps()

	var receivedHead string
	d.prs.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		receivedHead = head
		return &models.PRDetails{Number: 1}, nil
	}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if receivedHead != "ai-bot/PROJ-1" {
		t.Errorf("branch = %q, want ai-bot/PROJ-1", receivedHead)
	}
}

// --- Fork mode ---

func TestFeedbackScanner_ForkMode_UsesOwnerPrefixedHead(t *testing.T) {
	d := newFeedbackDeps()
	d.repos.ForkOwnerFunc = func(workItem models.WorkItem) string {
		if workItem.Key == "PROJ-1" {
			return "contributor-gh"
		}
		return ""
	}

	var receivedHead string
	d.prs.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		receivedHead = head
		return &models.PRDetails{Number: 42, Branch: head}, nil
	}
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if receivedHead != "contributor-gh:ai-bot/PROJ-1" {
		t.Errorf("head = %q, want contributor-gh:ai-bot/PROJ-1", receivedHead)
	}
}

// --- No event when no comments at all ---

func TestFeedbackScanner_NoCommentsNoEvent(t *testing.T) {
	d := newFeedbackDeps()
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{}, nil
	}

	submitCalled := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitCalled = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneFeedbackScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when no comments exist")
	}
}

// --- helpers ---

type feedbackDeps struct {
	searcher  *scannertest.StubIssueSearcher
	submitter *scannertest.StubJobSubmitter
	prs       *scannertest.StubPRFetcher
	repos     *scannertest.StubRepoLocator
	cfg       scanner.FeedbackScannerConfig
}

func newFeedbackDeps() *feedbackDeps {
	return &feedbackDeps{
		searcher: &scannertest.StubIssueSearcher{
			SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
				return []models.WorkItem{{Key: "PROJ-1"}}, nil
			},
		},
		submitter: &scannertest.StubJobSubmitter{},
		prs: &scannertest.StubPRFetcher{
			GetPRForBranchFunc: func(owner, repo, head string) (*models.PRDetails, error) {
				return &models.PRDetails{
					Number: 42, Branch: head,
					URL: "https://github.com/org/repo/pull/42",
				}, nil
			},
			GetPRCommentsFunc: func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
				return []models.PRComment{
					{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
				}, nil
			},
		},
		repos: &scannertest.StubRepoLocator{
			LocateRepoFunc: func(_ models.WorkItem) (string, string, error) {
				return "org", "repo", nil
			},
		},
		cfg: scanner.FeedbackScannerConfig{
			PollInterval: time.Hour,
			BotUsername:  "ai-bot",
		},
	}
}

func (d *feedbackDeps) scanner(t *testing.T) *scanner.FeedbackScanner {
	t.Helper()
	s, err := scanner.NewFeedbackScanner(
		d.searcher, d.submitter, d.prs, d.repos, d.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// runOneFeedbackScan starts the scanner, waits for the immediate
// first scan to complete, then stops. Uses the long poll interval
// (1 hour) set by newFeedbackDeps to ensure only one scan cycle runs.
//
// The 50ms sleep is sufficient because all stubs complete
// synchronously (no real I/O). Tests that need tighter
// synchronization use channel-based patterns directly.
func runOneFeedbackScan(t *testing.T, s *scanner.FeedbackScanner) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	s.Stop()
}
