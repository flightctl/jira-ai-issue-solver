// Package recoverytest provides test doubles for the recovery package.
package recoverytest

import (
	"context"
	"errors"
	"time"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/recovery"
)

// Compile-time checks.
var (
	_ recovery.Runner           = (*StubRunner)(nil)
	_ recovery.IssueTracker     = (*StubIssueTracker)(nil)
	_ recovery.GitService       = (*StubGitService)(nil)
	_ recovery.WorkspaceCleaner = (*StubWorkspaceCleaner)(nil)
	_ recovery.ContainerCleaner = (*StubContainerCleaner)(nil)
	_ recovery.JobSubmitter     = (*StubJobSubmitter)(nil)
	_ recovery.ProjectResolver  = (*StubProjectResolver)(nil)
)

// StubRunner is a test double for [recovery.Runner].
type StubRunner struct {
	RunFunc func(ctx context.Context) error
}

func (s *StubRunner) Run(ctx context.Context) error {
	if s.RunFunc != nil {
		return s.RunFunc(ctx)
	}
	return nil
}

// StubIssueTracker is a test double for [recovery.IssueTracker].
type StubIssueTracker struct {
	SearchWorkItemsFunc  func(criteria models.SearchCriteria) ([]models.WorkItem, error)
	GetWorkItemFunc      func(key string) (*models.WorkItem, error)
	TransitionStatusFunc func(key, status string) error
	SetFieldValueFunc    func(key, field, value string) error
	AddCommentFunc       func(key, body string) error
}

func (s *StubIssueTracker) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if s.SearchWorkItemsFunc != nil {
		return s.SearchWorkItemsFunc(criteria)
	}
	return []models.WorkItem{}, nil
}

func (s *StubIssueTracker) GetWorkItem(key string) (*models.WorkItem, error) {
	if s.GetWorkItemFunc != nil {
		return s.GetWorkItemFunc(key)
	}
	return nil, nil
}

func (s *StubIssueTracker) TransitionStatus(key, status string) error {
	if s.TransitionStatusFunc != nil {
		return s.TransitionStatusFunc(key, status)
	}
	return nil
}

func (s *StubIssueTracker) SetFieldValue(key, field, value string) error {
	if s.SetFieldValueFunc != nil {
		return s.SetFieldValueFunc(key, field, value)
	}
	return nil
}

func (s *StubIssueTracker) AddComment(key, body string) error {
	if s.AddCommentFunc != nil {
		return s.AddCommentFunc(key, body)
	}
	return nil
}

// StubGitService is a test double for [recovery.GitService].
type StubGitService struct {
	GetPRForBranchFunc   func(owner, repo, head string) (*models.PRDetails, error)
	BranchHasCommitsFunc func(owner, repo, branch, base string) (bool, error)
	CreatePRFunc         func(params models.PRParams) (*models.PR, error)
}

func (s *StubGitService) GetPRForBranch(owner, repo, head string) (*models.PRDetails, error) {
	if s.GetPRForBranchFunc != nil {
		return s.GetPRForBranchFunc(owner, repo, head)
	}
	return nil, errors.New("no PR found")
}

func (s *StubGitService) BranchHasCommits(owner, repo, branch, base string) (bool, error) {
	if s.BranchHasCommitsFunc != nil {
		return s.BranchHasCommitsFunc(owner, repo, branch, base)
	}
	return false, nil
}

func (s *StubGitService) CreatePR(params models.PRParams) (*models.PR, error) {
	if s.CreatePRFunc != nil {
		return s.CreatePRFunc(params)
	}
	return &models.PR{}, nil
}

// StubWorkspaceCleaner is a test double for [recovery.WorkspaceCleaner].
type StubWorkspaceCleaner struct {
	CleanupByFilterFunc func(shouldRemove func(ticketKey string) bool) (int, error)
	CleanupStaleFunc    func(maxAge time.Duration) (int, error)
}

func (s *StubWorkspaceCleaner) CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error) {
	if s.CleanupByFilterFunc != nil {
		return s.CleanupByFilterFunc(shouldRemove)
	}
	return 0, nil
}

func (s *StubWorkspaceCleaner) CleanupStale(maxAge time.Duration) (int, error) {
	if s.CleanupStaleFunc != nil {
		return s.CleanupStaleFunc(maxAge)
	}
	return 0, nil
}

// StubContainerCleaner is a test double for [recovery.ContainerCleaner].
type StubContainerCleaner struct {
	CleanupOrphansFunc func(ctx context.Context, prefix string) error
}

func (s *StubContainerCleaner) CleanupOrphans(ctx context.Context, prefix string) error {
	if s.CleanupOrphansFunc != nil {
		return s.CleanupOrphansFunc(ctx, prefix)
	}
	return nil
}

// StubJobSubmitter is a test double for [recovery.JobSubmitter].
type StubJobSubmitter struct {
	SubmitFunc func(event jobmanager.Event) (*jobmanager.Job, error)
}

func (s *StubJobSubmitter) Submit(event jobmanager.Event) (*jobmanager.Job, error) {
	if s.SubmitFunc != nil {
		return s.SubmitFunc(event)
	}
	return &jobmanager.Job{}, nil
}

// StubProjectResolver is a test double for [recovery.ProjectResolver].
type StubProjectResolver struct {
	ResolveProjectFunc func(workItem models.WorkItem) (*models.ProjectSettings, error)
}

func (s *StubProjectResolver) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	if s.ResolveProjectFunc != nil {
		return s.ResolveProjectFunc(workItem)
	}
	return &models.ProjectSettings{}, nil
}
