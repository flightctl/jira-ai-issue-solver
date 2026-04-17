// Package scannertest provides test doubles for the scanner package.
package scannertest

import (
	"context"
	"time"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/scanner"
)

// Compile-time checks.
var (
	_ scanner.Scanner             = (*StubScanner)(nil)
	_ scanner.IssueSearcher       = (*StubIssueSearcher)(nil)
	_ scanner.JobSubmitter        = (*StubJobSubmitter)(nil)
	_ scanner.PRFetcher           = (*StubPRFetcher)(nil)
	_ scanner.RepoLocator         = (*StubRepoLocator)(nil)
	_ scanner.WorkspaceCleaner    = (*StubWorkspaceCleaner)(nil)
	_ scanner.TicketStatusChecker = (*StubTicketStatusChecker)(nil)
)

// StubScanner is a test double for [scanner.Scanner].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubScanner struct {
	StartFunc func(ctx context.Context) error
	StopFunc  func()
}

func (s *StubScanner) Start(ctx context.Context) error {
	if s.StartFunc != nil {
		return s.StartFunc(ctx)
	}
	return nil
}

func (s *StubScanner) Stop() {
	if s.StopFunc != nil {
		s.StopFunc()
	}
}

// StubIssueSearcher is a test double for [scanner.IssueSearcher].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns an empty slice.
type StubIssueSearcher struct {
	SearchWorkItemsFunc func(criteria models.SearchCriteria) ([]models.WorkItem, error)
}

func (s *StubIssueSearcher) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if s.SearchWorkItemsFunc != nil {
		return s.SearchWorkItemsFunc(criteria)
	}
	return []models.WorkItem{}, nil
}

// StubJobSubmitter is a test double for [scanner.JobSubmitter].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns a zero-value job.
type StubJobSubmitter struct {
	SubmitFunc func(event jobmanager.Event) (*jobmanager.Job, error)
}

func (s *StubJobSubmitter) Submit(event jobmanager.Event) (*jobmanager.Job, error) {
	if s.SubmitFunc != nil {
		return s.SubmitFunc(event)
	}
	return &jobmanager.Job{}, nil
}

// StubPRFetcher is a test double for [scanner.PRFetcher].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubPRFetcher struct {
	GetPRForBranchFunc func(owner, repo, head string) (*models.PRDetails, error)
	GetPRCommentsFunc  func(owner, repo string, number int, since time.Time) ([]models.PRComment, error)
}

func (s *StubPRFetcher) GetPRForBranch(owner, repo, head string) (*models.PRDetails, error) {
	if s.GetPRForBranchFunc != nil {
		return s.GetPRForBranchFunc(owner, repo, head)
	}
	return &models.PRDetails{}, nil
}

func (s *StubPRFetcher) GetPRComments(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
	if s.GetPRCommentsFunc != nil {
		return s.GetPRCommentsFunc(owner, repo, number, since)
	}
	return []models.PRComment{}, nil
}

// StubRepoLocator is a test double for [scanner.RepoLocator].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns empty strings.
type StubRepoLocator struct {
	LocateRepoFunc func(workItem models.WorkItem) (string, string, error)
	ForkOwnerFunc  func(workItem models.WorkItem) string
}

func (s *StubRepoLocator) LocateRepo(workItem models.WorkItem) (string, string, error) {
	if s.LocateRepoFunc != nil {
		return s.LocateRepoFunc(workItem)
	}
	return "", "", nil
}

func (s *StubRepoLocator) ForkOwner(workItem models.WorkItem) string {
	if s.ForkOwnerFunc != nil {
		return s.ForkOwnerFunc(workItem)
	}
	return ""
}

// StubWorkspaceCleaner is a test double for [scanner.WorkspaceCleaner].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubWorkspaceCleaner struct {
	CleanupByFilterFunc func(shouldRemove func(string) bool) (int, error)
}

func (s *StubWorkspaceCleaner) CleanupByFilter(shouldRemove func(string) bool) (int, error) {
	if s.CleanupByFilterFunc != nil {
		return s.CleanupByFilterFunc(shouldRemove)
	}
	return 0, nil
}

// StubTicketStatusChecker is a test double for [scanner.TicketStatusChecker].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns a zero-value WorkItem.
type StubTicketStatusChecker struct {
	GetWorkItemFunc func(key string) (*models.WorkItem, error)
}

func (s *StubTicketStatusChecker) GetWorkItem(key string) (*models.WorkItem, error) {
	if s.GetWorkItemFunc != nil {
		return s.GetWorkItemFunc(key)
	}
	return &models.WorkItem{}, nil
}
