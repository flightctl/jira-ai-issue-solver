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

func TestNewMergeScanner_Validation(t *testing.T) {
	validCfg := scanner.MergeScannerConfig{
		PollInterval: time.Minute,
		BotUsername:  "ai-bot",
		IdleDays:     7,
		IdleLabel:    "ai-bot/idle",
	}

	tests := []struct {
		name       string
		searcher   scanner.IssueSearcher
		submitter  scanner.JobSubmitter
		prs        scanner.PRFetcher
		repos      scanner.RepoLocator
		mergeCheck scanner.MergeabilityChecker
		labeler    scanner.PRLabeler
		cfg        scanner.MergeScannerConfig
		logger     *zap.Logger
		wantErr    string
	}{
		{
			name: "nil searcher", searcher: nil,
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "issue searcher",
		},
		{
			name: "nil submitter", searcher: &scannertest.StubIssueSearcher{},
			submitter: nil, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "job submitter",
		},
		{
			name: "nil prs", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: nil,
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "PR fetcher",
		},
		{
			name: "nil repos", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: nil, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "repo locator",
		},
		{
			name: "nil mergeCheck", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: nil,
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "mergeability checker",
		},
		{
			name: "nil labeler", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: nil,
			cfg:     validCfg, logger: zap.NewNop(), wantErr: "PR labeler",
		},
		{
			name: "zero poll", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     scanner.MergeScannerConfig{BotUsername: "bot"}, logger: zap.NewNop(),
			wantErr: "poll interval",
		},
		{
			name: "empty bot", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     scanner.MergeScannerConfig{PollInterval: time.Minute}, logger: zap.NewNop(),
			wantErr: "bot username",
		},
		{
			name: "nil logger", searcher: &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{}, prs: &scannertest.StubPRFetcher{},
			repos: &scannertest.StubRepoLocator{}, mergeCheck: &scannertest.StubMergeabilityChecker{},
			labeler: &scannertest.StubPRLabeler{},
			cfg:     validCfg, logger: nil, wantErr: "logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scanner.NewMergeScanner(
				tt.searcher, tt.submitter, tt.prs, tt.repos,
				tt.mergeCheck, tt.labeler, tt.cfg, tt.logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// --- Submits merge event for unmergeable PR ---

func TestMergeScanner_SubmitsMergeEventForUnmergeablePR(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
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
	runOneMergeScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if len(submitted) != 1 {
		t.Fatalf("expected 1 event, got %d", len(submitted))
	}
	if submitted[0].Type != jobmanager.JobTypeMerge {
		t.Errorf("expected JobTypeMerge, got %s", submitted[0].Type)
	}
	if submitted[0].TicketKey != "PROJ-1" {
		t.Errorf("expected PROJ-1, got %s", submitted[0].TicketKey)
	}
}

// --- Skips mergeable PR ---

func TestMergeScanner_SkipsMergeablePR(t *testing.T) {
	d := newMergeDeps()

	mergeable := true
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event for mergeable PR")
	}
}

// --- Skips unknown mergeability (nil) ---

func TestMergeScanner_SkipsUnknownMergeability(t *testing.T) {
	d := newMergeDeps()

	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: nil, BaseBranch: "main"}, nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event when mergeability is unknown")
	}
}

// --- Skips PR with no items ---

func TestMergeScanner_NoItems(t *testing.T) {
	d := newMergeDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{}, nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event when no items found")
	}
}

// --- Handles search error ---

func TestMergeScanner_SearchError(t *testing.T) {
	d := newMergeDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return nil, errors.New("search failed")
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event on search error")
	}
}

// --- Circuit breaker stops scan cycle ---

func TestMergeScanner_CircuitBreakerStopsScan(t *testing.T) {
	d := newMergeDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{{Key: "PROJ-1"}, {Key: "PROJ-2"}}, nil
	}

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	callCount := 0
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		callCount++
		return nil, jobmanager.ErrCircuitOpen
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if callCount != 1 {
		t.Errorf("expected 1 submit call (circuit breaker should stop), got %d", callCount)
	}
}

// --- Idle detection: labels idle PR and skips ---

func TestMergeScanner_LabelsIdlePRAndSkips(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	// Last human comment was 10 days ago (idle threshold is 7).
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{
				ID:        1,
				Author:    models.Author{Username: "reviewer"},
				Body:      "Looks good",
				Timestamp: time.Now().AddDate(0, 0, -10),
			},
		}, nil
	}

	var labeledPR int
	var labeledWith string
	d.labeler.AddPRLabelFunc = func(_, _ string, number int, label string) error {
		labeledPR = number
		labeledWith = label
		return nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event for idle PR")
	}
	if labeledPR != 42 {
		t.Errorf("expected PR 42 to be labeled, got %d", labeledPR)
	}
	if labeledWith != "ai-bot/idle" {
		t.Errorf("expected label ai-bot/idle, got %s", labeledWith)
	}
}

// --- Idle detection: skips PR already labeled ---

func TestMergeScanner_SkipsPRWithIdleLabel(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	d.labeler.HasPRLabelFunc = func(_, _ string, _ int, _ string) (bool, error) {
		return true, nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	// Should not even fetch comments.
	commentsFetched := false
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		commentsFetched = true
		return []models.PRComment{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event for labeled idle PR")
	}
	if commentsFetched {
		t.Error("should not fetch comments when idle label is present")
	}
}

// --- Active PR (recent comments) is not labeled ---

func TestMergeScanner_ActivePRNotLabeled(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	// Recent human comment (1 day ago, threshold is 7).
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{
				ID:        1,
				Author:    models.Author{Username: "reviewer"},
				Body:      "Please fix this",
				Timestamp: time.Now().AddDate(0, 0, -1),
			},
		}, nil
	}

	labeled := false
	d.labeler.AddPRLabelFunc = func(_, _ string, _ int, _ string) error {
		labeled = true
		return nil
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
	runOneMergeScan(t, s)

	if labeled {
		t.Error("should not label active PR")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(submitted) != 1 {
		t.Fatalf("expected 1 event, got %d", len(submitted))
	}
}

// --- Idle detection disabled when IdleDays is zero ---

func TestMergeScanner_IdleDetectionDisabledWhenZero(t *testing.T) {
	d := newMergeDeps()
	d.cfg.IdleDays = 0

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
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
	runOneMergeScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if len(submitted) != 1 {
		t.Fatalf("expected 1 event (idle detection disabled), got %d", len(submitted))
	}
}

// --- Bot comments are excluded from activity detection ---

func TestMergeScanner_BotCommentsExcludedFromActivity(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	// Only bot comments exist — no human activity.
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{
				ID:        1,
				Author:    models.Author{Username: "ai-bot"},
				Body:      "Addressed in abc123.",
				Timestamp: time.Now(),
			},
		}, nil
	}

	labeled := false
	d.labeler.AddPRLabelFunc = func(_, _ string, _ int, _ string) error {
		labeled = true
		return nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	// No human comments at all — lastHumanCommentTime returns zero time,
	// which is before the idle threshold, so the PR is labeled idle.
	if !labeled {
		t.Error("expected PR to be labeled idle when only bot comments exist")
	}
	if submitted {
		t.Error("should not submit event for idle PR")
	}
}

// --- Known bots excluded from idle activity ---

func TestMergeScanner_KnownBotExcludedFromActivity(t *testing.T) {
	d := newMergeDeps()
	d.cfg.KnownBotUsernames = []string{"coderabbitai"}
	d.cfg.IgnoredUsernames = []string{"packit-as-a-service[bot]"}

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	// Only known bot and ignored user have recent comments.
	d.prs.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{
				ID:        1,
				Author:    models.Author{Username: "coderabbitai"},
				Body:      "LGTM",
				Timestamp: time.Now(),
			},
			{
				ID:        2,
				Author:    models.Author{Username: "packit-as-a-service[bot]"},
				Body:      "Build succeeded",
				Timestamp: time.Now(),
			},
		}, nil
	}

	labeled := false
	d.labeler.AddPRLabelFunc = func(_, _ string, _ int, _ string) error {
		labeled = true
		return nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if !labeled {
		t.Error("expected PR to be labeled idle when only known bots commented")
	}
	if submitted {
		t.Error("should not submit event for idle PR")
	}
}

// --- Fork workflow uses correct head format ---

func TestMergeScanner_ForkWorkflowHead(t *testing.T) {
	d := newMergeDeps()
	d.repos.ForkOwnerFunc = func(_ models.WorkItem) string {
		return "fork-user"
	}

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	var headUsed string
	d.prs.GetPRForBranchFunc = func(_, _, head string) (*models.PRDetails, error) {
		headUsed = head
		return &models.PRDetails{Number: 42, Branch: "ai-bot/PROJ-1"}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	expected := "fork-user:ai-bot/PROJ-1"
	if headUsed != expected {
		t.Errorf("expected head %q, got %q", expected, headUsed)
	}
}

// --- Lifecycle: double start returns error ---

func TestMergeScanner_DoubleStartReturnsError(t *testing.T) {
	d := newMergeDeps()
	d.searcher.SearchWorkItemsFunc = func(_ models.SearchCriteria) ([]models.WorkItem, error) {
		return []models.WorkItem{}, nil
	}

	s := d.scanner(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if err := s.Start(ctx); err == nil {
		t.Error("expected error on double start")
	}
}

// --- Duplicate job is handled gracefully ---

func TestMergeScanner_DuplicateJobHandled(t *testing.T) {
	d := newMergeDeps()

	mergeable := false
	d.mergeCheck.GetPRMergeabilityFunc = func(_, _ string, _ int) (*models.PRMergeState, error) {
		return &models.PRMergeState{Mergeable: &mergeable, BaseBranch: "main"}, nil
	}

	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		return nil, jobmanager.ErrDuplicateJob
	}

	s := d.scanner(t)
	runOneMergeScan(t, s) // Should not panic or error.
}

// --- No PR found for repo is handled gracefully ---

func TestMergeScanner_NoPRFound(t *testing.T) {
	d := newMergeDeps()
	d.prs.GetPRForBranchFunc = func(_, _, _ string) (*models.PRDetails, error) {
		return nil, nil
	}

	submitted := false
	d.submitter.SubmitFunc = func(_ jobmanager.Event) (*jobmanager.Job, error) {
		submitted = true
		return &jobmanager.Job{}, nil
	}

	s := d.scanner(t)
	runOneMergeScan(t, s)

	if submitted {
		t.Error("should not submit event when no PR exists")
	}
}

// --- Test helpers ---

type mergeDeps struct {
	searcher   *scannertest.StubIssueSearcher
	submitter  *scannertest.StubJobSubmitter
	prs        *scannertest.StubPRFetcher
	repos      *scannertest.StubRepoLocator
	mergeCheck *scannertest.StubMergeabilityChecker
	labeler    *scannertest.StubPRLabeler
	cfg        scanner.MergeScannerConfig
}

func newMergeDeps() *mergeDeps {
	return &mergeDeps{
		searcher: &scannertest.StubIssueSearcher{
			SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
				return []models.WorkItem{{Key: "PROJ-1"}}, nil
			},
		},
		submitter: &scannertest.StubJobSubmitter{},
		prs: &scannertest.StubPRFetcher{
			GetPRForBranchFunc: func(_, _, head string) (*models.PRDetails, error) {
				return &models.PRDetails{
					Number: 42, Branch: head,
					URL: "https://github.com/org/repo/pull/42",
				}, nil
			},
			GetPRCommentsFunc: func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
				return []models.PRComment{
					{
						ID:        1,
						Author:    models.Author{Username: "reviewer"},
						Body:      "Review comment",
						Timestamp: time.Now(),
					},
				}, nil
			},
		},
		repos: &scannertest.StubRepoLocator{
			LocateReposFunc: func(_ models.WorkItem) ([]models.RepoCoord, error) {
				return []models.RepoCoord{{Owner: "org", Repo: "repo"}}, nil
			},
		},
		mergeCheck: &scannertest.StubMergeabilityChecker{},
		labeler:    &scannertest.StubPRLabeler{},
		cfg: scanner.MergeScannerConfig{
			PollInterval: time.Hour,
			BotUsername:  "ai-bot",
			IdleDays:     7,
			IdleLabel:    "ai-bot/idle",
		},
	}
}

func (d *mergeDeps) scanner(t *testing.T) *scanner.MergeScanner {
	t.Helper()
	s, err := scanner.NewMergeScanner(
		d.searcher, d.submitter, d.prs, d.repos,
		d.mergeCheck, d.labeler, d.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runOneMergeScan(t *testing.T, s *scanner.MergeScanner) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	s.Stop()
}
