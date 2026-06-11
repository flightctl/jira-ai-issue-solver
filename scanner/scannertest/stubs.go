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
	_ scanner.Scanner              = (*StubScanner)(nil)
	_ scanner.IssueSearcher        = (*StubIssueSearcher)(nil)
	_ scanner.JobSubmitter         = (*StubJobSubmitter)(nil)
	_ scanner.PRFetcher            = (*StubPRFetcher)(nil)
	_ scanner.RepoLocator          = (*StubRepoLocator)(nil)
	_ scanner.CIChecker            = (*StubCIChecker)(nil)
	_ scanner.WorkspaceCleaner     = (*StubWorkspaceCleaner)(nil)
	_ scanner.TicketStatusChecker  = (*StubTicketStatusChecker)(nil)
	_ scanner.LabelRemover         = (*StubLabelRemover)(nil)
	_ scanner.LabelManager         = (*StubLabelManager)(nil)
	_ scanner.FailureLabelResolver = (*StubFailureLabelResolver)(nil)
	_ scanner.RetryResetter        = (*StubRetryResetter)(nil)
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

// StubRetryResetter is a test double for [scanner.RetryResetter].
type StubRetryResetter struct {
	ResetRetriesFunc func(ticketKey string) error
}

func (s *StubRetryResetter) ResetRetries(ticketKey string) error {
	if s.ResetRetriesFunc != nil {
		return s.ResetRetriesFunc(ticketKey)
	}
	return nil
}

// StubPRFetcher is a test double for [scanner.PRFetcher].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubPRFetcher struct {
	GetPRForBranchFunc       func(owner, repo, head string) (*models.PRDetails, error)
	GetClosedPRForBranchFunc func(owner, repo, head string) (*models.PRDetails, error)
	GetPRCommentsFunc        func(owner, repo string, number int, since time.Time) ([]models.PRComment, error)
}

func (s *StubPRFetcher) GetPRForBranch(owner, repo, head string) (*models.PRDetails, error) {
	if s.GetPRForBranchFunc != nil {
		return s.GetPRForBranchFunc(owner, repo, head)
	}
	return &models.PRDetails{}, nil
}

func (s *StubPRFetcher) GetClosedPRForBranch(owner, repo, head string) (*models.PRDetails, error) {
	if s.GetClosedPRForBranchFunc != nil {
		return s.GetClosedPRForBranchFunc(owner, repo, head)
	}
	return nil, nil
}

func (s *StubPRFetcher) GetPRComments(owner, repo string, number int, since time.Time) ([]models.PRComment, error) {
	if s.GetPRCommentsFunc != nil {
		return s.GetPRCommentsFunc(owner, repo, number, since)
	}
	return []models.PRComment{}, nil
}

// StubRepoLocator is a test double for [scanner.RepoLocator].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns empty/zero values.
type StubRepoLocator struct {
	LocateRepoFunc  func(workItem models.WorkItem) (string, string, error)
	LocateReposFunc func(workItem models.WorkItem) ([]models.RepoCoord, error)
	ForkOwnerFunc   func(workItem models.WorkItem) string
}

func (s *StubRepoLocator) LocateRepo(workItem models.WorkItem) (string, string, error) {
	if s.LocateRepoFunc != nil {
		return s.LocateRepoFunc(workItem)
	}
	return "", "", nil
}

func (s *StubRepoLocator) LocateRepos(workItem models.WorkItem) ([]models.RepoCoord, error) {
	if s.LocateReposFunc != nil {
		return s.LocateReposFunc(workItem)
	}
	return []models.RepoCoord{}, nil
}

func (s *StubRepoLocator) ForkOwner(workItem models.WorkItem) string {
	if s.ForkOwnerFunc != nil {
		return s.ForkOwnerFunc(workItem)
	}
	return ""
}

// StubCIChecker is a test double for [scanner.CIChecker].
type StubCIChecker struct {
	ListCheckRunsForRefFunc func(owner, repo, ref string) ([]models.CheckRunFailure, bool, error)
}

func (s *StubCIChecker) ListCheckRunsForRef(owner, repo, ref string) ([]models.CheckRunFailure, bool, error) {
	if s.ListCheckRunsForRefFunc != nil {
		return s.ListCheckRunsForRefFunc(owner, repo, ref)
	}
	return []models.CheckRunFailure{}, true, nil
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

// StubLabelRemover is a test double for [scanner.LabelRemover].
type StubLabelRemover struct {
	RemoveLabelFunc func(key, label string) error
}

func (s *StubLabelRemover) RemoveLabel(key, label string) error {
	if s.RemoveLabelFunc != nil {
		return s.RemoveLabelFunc(key, label)
	}
	return nil
}

// StubLabelManager is a test double for [scanner.LabelManager].
type StubLabelManager struct {
	AddLabelFunc    func(key, label string) error
	RemoveLabelFunc func(key, label string) error
}

func (s *StubLabelManager) AddLabel(key, label string) error {
	if s.AddLabelFunc != nil {
		return s.AddLabelFunc(key, label)
	}
	return nil
}

func (s *StubLabelManager) RemoveLabel(key, label string) error {
	if s.RemoveLabelFunc != nil {
		return s.RemoveLabelFunc(key, label)
	}
	return nil
}

// StubFailureLabelResolver is a test double for
// [scanner.FailureLabelResolver].
type StubFailureLabelResolver struct {
	ResolveFailureLabelsFunc func(item models.WorkItem) models.FailureLabels
}

func (s *StubFailureLabelResolver) ResolveFailureLabels(item models.WorkItem) models.FailureLabels {
	if s.ResolveFailureLabelsFunc != nil {
		return s.ResolveFailureLabelsFunc(item)
	}
	return models.FailureLabels{}
}
