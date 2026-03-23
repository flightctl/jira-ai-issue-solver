package scanner_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/scanner"
	"jira-ai-issue-solver/scanner/scannertest"
)

// --- Construction validation ---

func TestNewWorkItemScanner_Validation(t *testing.T) {
	validCfg := scanner.WorkItemScannerConfig{
		PollInterval: time.Minute,
	}

	tests := []struct {
		name      string
		searcher  scanner.IssueSearcher
		submitter scanner.JobSubmitter
		cfg       scanner.WorkItemScannerConfig
		logger    *zap.Logger
		wantErr   string
	}{
		{
			name:      "nil searcher",
			searcher:  nil,
			submitter: &scannertest.StubJobSubmitter{},
			cfg:       validCfg,
			logger:    zap.NewNop(),
			wantErr:   "issue searcher",
		},
		{
			name:      "nil submitter",
			searcher:  &scannertest.StubIssueSearcher{},
			submitter: nil,
			cfg:       validCfg,
			logger:    zap.NewNop(),
			wantErr:   "job submitter",
		},
		{
			name:      "zero poll interval",
			searcher:  &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			cfg:       scanner.WorkItemScannerConfig{PollInterval: 0},
			logger:    zap.NewNop(),
			wantErr:   "poll interval",
		},
		{
			name:      "nil logger",
			searcher:  &scannertest.StubIssueSearcher{},
			submitter: &scannertest.StubJobSubmitter{},
			cfg:       validCfg,
			logger:    nil,
			wantErr:   "logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scanner.NewWorkItemScanner(tt.searcher, tt.submitter, tt.cfg, tt.logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewWorkItemScanner_ValidConfig(t *testing.T) {
	s, err := scanner.NewWorkItemScanner(
		&scannertest.StubIssueSearcher{},
		&scannertest.StubJobSubmitter{},
		scanner.WorkItemScannerConfig{PollInterval: time.Minute},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil scanner")
	}
}

// --- Emits events for matching tickets ---

func TestWorkItemScanner_EmitsEvents(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{
				{Key: "PROJ-1"},
				{Key: "PROJ-2"},
			}, nil
		},
	}

	var mu sync.Mutex
	var submitted []jobmanager.Event
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(event jobmanager.Event) (*jobmanager.Job, error) {
			mu.Lock()
			submitted = append(submitted, event)
			mu.Unlock()
			return &jobmanager.Job{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if len(submitted) != 2 {
		t.Fatalf("submitted %d events, want 2", len(submitted))
	}
	if submitted[0].TicketKey != "PROJ-1" {
		t.Errorf("event[0].TicketKey = %q, want PROJ-1", submitted[0].TicketKey)
	}
	if submitted[1].TicketKey != "PROJ-2" {
		t.Errorf("event[1].TicketKey = %q, want PROJ-2", submitted[1].TicketKey)
	}
	for _, e := range submitted {
		if e.Type != jobmanager.JobTypeNewTicket {
			t.Errorf("event type = %q, want new_ticket", e.Type)
		}
	}
}

// --- No events when no tickets ---

func TestWorkItemScanner_NoEventsWhenEmpty(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{}, nil
		},
	}

	submitCalled := false
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(_ jobmanager.Event) (*jobmanager.Job, error) {
			submitCalled = true
			return &jobmanager.Job{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	if submitCalled {
		t.Error("Submit should not be called when no tickets found")
	}
}

// --- Duplicate job silently skipped ---

func TestWorkItemScanner_DuplicateJobSkipped(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{{Key: "PROJ-1"}, {Key: "PROJ-2"}}, nil
		},
	}

	var mu sync.Mutex
	var callCount int
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(event jobmanager.Event) (*jobmanager.Job, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			if event.TicketKey == "PROJ-1" {
				return nil, jobmanager.ErrDuplicateJob
			}
			return &jobmanager.Job{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	// Both tickets attempted (duplicate doesn't stop the cycle).
	if callCount != 2 {
		t.Errorf("Submit called %d times, want 2", callCount)
	}
}

// --- Circuit open stops scan cycle ---

func TestWorkItemScanner_CircuitOpenStopsCycle(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{
				{Key: "PROJ-1"},
				{Key: "PROJ-2"},
				{Key: "PROJ-3"},
			}, nil
		},
	}

	var mu sync.Mutex
	var callCount int
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(_ jobmanager.Event) (*jobmanager.Job, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return nil, jobmanager.ErrCircuitOpen
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	// Circuit open on first ticket should stop processing remaining.
	if callCount != 1 {
		t.Errorf("Submit called %d times, want 1 (circuit open stops cycle)", callCount)
	}
}

// --- Budget exceeded stops scan cycle ---

func TestWorkItemScanner_BudgetExceededStopsCycle(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{
				{Key: "PROJ-1"},
				{Key: "PROJ-2"},
				{Key: "PROJ-3"},
			}, nil
		},
	}

	var mu sync.Mutex
	var callCount int
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(_ jobmanager.Event) (*jobmanager.Job, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return nil, jobmanager.ErrBudgetExceeded
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("Submit called %d times, want 1 (budget exceeded stops cycle)", callCount)
	}
}

// --- Search error continues to next cycle ---

func TestWorkItemScanner_SearchErrorContinues(t *testing.T) {
	var searchCount int32
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			count := atomic.AddInt32(&searchCount, 1)
			if count == 1 {
				return nil, errors.New("network error")
			}
			return []models.WorkItem{{Key: "PROJ-1"}}, nil
		},
	}

	submitted := make(chan jobmanager.Event, 1)
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(event jobmanager.Event) (*jobmanager.Job, error) {
			select {
			case submitted <- event:
			default:
			}
			return &jobmanager.Job{}, nil
		},
	}

	// Use short interval so second scan fires after first fails.
	s, err := scanner.NewWorkItemScanner(searcher, submitter,
		scanner.WorkItemScannerConfig{PollInterval: 10 * time.Millisecond},
		zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Wait for second scan to succeed.
	select {
	case e := <-submitted:
		if e.TicketKey != "PROJ-1" {
			t.Errorf("ticket = %q, want PROJ-1", e.TicketKey)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event after search error")
	}

	s.Stop()
}

// --- Retries exhausted silently skipped ---

func TestWorkItemScanner_RetriesExhaustedSkipped(t *testing.T) {
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			return []models.WorkItem{{Key: "PROJ-1"}, {Key: "PROJ-2"}}, nil
		},
	}

	var mu sync.Mutex
	var callCount int
	submitter := &scannertest.StubJobSubmitter{
		SubmitFunc: func(event jobmanager.Event) (*jobmanager.Job, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			if event.TicketKey == "PROJ-1" {
				return nil, jobmanager.ErrRetriesExhausted
			}
			return &jobmanager.Job{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, submitter)
	runOneScan(t, s)

	mu.Lock()
	defer mu.Unlock()
	// Both tickets attempted (exhausted doesn't stop the cycle).
	if callCount != 2 {
		t.Errorf("Submit called %d times, want 2", callCount)
	}
}

// --- Start/Stop lifecycle ---

func TestWorkItemScanner_StartWhileRunning(t *testing.T) {
	s := newWorkItemScanner(t, &scannertest.StubIssueSearcher{}, &scannertest.StubJobSubmitter{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Second Start should fail.
	if err := s.Start(ctx); err == nil {
		t.Fatal("expected error on double start")
	}

	s.Stop()
}

func TestWorkItemScanner_StopWithoutStart(t *testing.T) {
	s := newWorkItemScanner(t, &scannertest.StubIssueSearcher{}, &scannertest.StubJobSubmitter{})

	// Should not panic.
	s.Stop()
}

func TestWorkItemScanner_RestartAfterStop(t *testing.T) {
	var mu sync.Mutex
	var scanCount int
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			mu.Lock()
			scanCount++
			mu.Unlock()
			return []models.WorkItem{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, &scannertest.StubJobSubmitter{})

	// First Start/Stop cycle.
	runOneScan(t, s)

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

func TestWorkItemScanner_StopBlocksUntilDone(t *testing.T) {
	scanned := make(chan struct{}, 1)
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(_ models.SearchCriteria) ([]models.WorkItem, error) {
			select {
			case scanned <- struct{}{}:
			default:
			}
			return []models.WorkItem{}, nil
		},
	}

	s := newWorkItemScanner(t, searcher, &scannertest.StubJobSubmitter{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Wait for first scan.
	select {
	case <-scanned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scan")
	}

	// Stop should block until goroutine exits and return.
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return")
	}
}

// --- Passes configured criteria ---

func TestWorkItemScanner_PassesCriteria(t *testing.T) {
	expectedCriteria := models.SearchCriteria{
		ProjectKeys: []string{"PROJ"},
		StatusByType: map[string][]string{
			"Bug": {"Open"},
		},
	}

	var receivedCriteria models.SearchCriteria
	searcher := &scannertest.StubIssueSearcher{
		SearchWorkItemsFunc: func(criteria models.SearchCriteria) ([]models.WorkItem, error) {
			receivedCriteria = criteria
			return []models.WorkItem{}, nil
		},
	}

	cfg := scanner.WorkItemScannerConfig{
		Criteria:     expectedCriteria,
		PollInterval: time.Hour,
	}

	s, err := scanner.NewWorkItemScanner(searcher, &scannertest.StubJobSubmitter{}, cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	runOneScan(t, s)

	if len(receivedCriteria.ProjectKeys) != 1 || receivedCriteria.ProjectKeys[0] != "PROJ" {
		t.Errorf("criteria.ProjectKeys = %v, want [PROJ]", receivedCriteria.ProjectKeys)
	}
	if statuses, ok := receivedCriteria.StatusByType["Bug"]; !ok || len(statuses) != 1 || statuses[0] != "Open" {
		t.Errorf("criteria.StatusByType = %v, want Bug→[Open]", receivedCriteria.StatusByType)
	}
}

// --- helpers ---

func newWorkItemScanner(t *testing.T, searcher scanner.IssueSearcher, submitter scanner.JobSubmitter) *scanner.WorkItemScanner {
	t.Helper()
	s, err := scanner.NewWorkItemScanner(searcher, submitter,
		scanner.WorkItemScannerConfig{PollInterval: time.Hour},
		zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// runOneScan starts the scanner, waits for the immediate first scan
// to complete, then stops. Uses the long poll interval (1 hour) set
// by newWorkItemScanner to ensure only one scan cycle runs.
//
// The 50ms sleep is sufficient because all stubs complete
// synchronously (no real I/O). Tests that need tighter
// synchronization use channel-based patterns directly (see
// TestWorkItemScanner_SearchErrorContinues).
func runOneScan(t *testing.T, s *scanner.WorkItemScanner) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	s.Stop()
}
