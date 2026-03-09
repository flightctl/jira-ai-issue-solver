package mocks

import (
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/tracker"
)

// Compile-time check that MockIssueTracker implements tracker.IssueTracker.
var _ tracker.IssueTracker = (*MockIssueTracker)(nil)

// MockIssueTracker is a test double for tracker.IssueTracker.
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type MockIssueTracker struct {
	SearchWorkItemsFunc  func(criteria models.SearchCriteria) ([]models.WorkItem, error)
	GetWorkItemFunc      func(key string) (*models.WorkItem, error)
	TransitionStatusFunc func(key, status string) error
	AddCommentFunc       func(key, body string) error
	GetFieldValueFunc    func(key, field string) (string, error)
	SetFieldValueFunc    func(key, field, value string) error
}

func (m *MockIssueTracker) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if m.SearchWorkItemsFunc != nil {
		return m.SearchWorkItemsFunc(criteria)
	}
	return []models.WorkItem{}, nil
}

func (m *MockIssueTracker) GetWorkItem(key string) (*models.WorkItem, error) {
	if m.GetWorkItemFunc != nil {
		return m.GetWorkItemFunc(key)
	}
	return nil, nil
}

func (m *MockIssueTracker) TransitionStatus(key, status string) error {
	if m.TransitionStatusFunc != nil {
		return m.TransitionStatusFunc(key, status)
	}
	return nil
}

func (m *MockIssueTracker) AddComment(key, body string) error {
	if m.AddCommentFunc != nil {
		return m.AddCommentFunc(key, body)
	}
	return nil
}

func (m *MockIssueTracker) GetFieldValue(key, field string) (string, error) {
	if m.GetFieldValueFunc != nil {
		return m.GetFieldValueFunc(key, field)
	}
	return "", nil
}

func (m *MockIssueTracker) SetFieldValue(key, field, value string) error {
	if m.SetFieldValueFunc != nil {
		return m.SetFieldValueFunc(key, field, value)
	}
	return nil
}
