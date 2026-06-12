// Package trackertest provides test doubles for the tracker package.
package trackertest

import (
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/tracker"
)

// Compile-time check that Stub implements tracker.IssueTracker.
var _ tracker.IssueTracker = (*Stub)(nil)

// Stub is a test double for [tracker.IssueTracker].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	SearchWorkItemsFunc    func(criteria models.SearchCriteria) ([]models.WorkItem, error)
	GetWorkItemFunc        func(key string) (*models.WorkItem, error)
	TransitionStatusFunc   func(key, status string) error
	AddCommentFunc         func(key, body string) error
	GetCommentsFunc        func(key string) ([]models.Comment, error)
	UpdateCommentFunc      func(key, commentID, body string) error
	DeleteCommentFunc      func(key, commentID string) error
	AddLabelFunc           func(key, label string) error
	RemoveLabelFunc        func(key, label string) error
	SetFieldValueFunc      func(key, field, value string) error
	DownloadAttachmentFunc func(url string) ([]byte, error)
}

func (s *Stub) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if s.SearchWorkItemsFunc != nil {
		return s.SearchWorkItemsFunc(criteria)
	}
	return []models.WorkItem{}, nil
}

func (s *Stub) GetWorkItem(key string) (*models.WorkItem, error) {
	if s.GetWorkItemFunc != nil {
		return s.GetWorkItemFunc(key)
	}
	return nil, nil
}

func (s *Stub) TransitionStatus(key, status string) error {
	if s.TransitionStatusFunc != nil {
		return s.TransitionStatusFunc(key, status)
	}
	return nil
}

func (s *Stub) AddComment(key, body string) error {
	if s.AddCommentFunc != nil {
		return s.AddCommentFunc(key, body)
	}
	return nil
}

func (s *Stub) GetComments(key string) ([]models.Comment, error) {
	if s.GetCommentsFunc != nil {
		return s.GetCommentsFunc(key)
	}
	return []models.Comment{}, nil
}

func (s *Stub) UpdateComment(key, commentID, body string) error {
	if s.UpdateCommentFunc != nil {
		return s.UpdateCommentFunc(key, commentID, body)
	}
	return nil
}

func (s *Stub) DeleteComment(key, commentID string) error {
	if s.DeleteCommentFunc != nil {
		return s.DeleteCommentFunc(key, commentID)
	}
	return nil
}

func (s *Stub) AddLabel(key, label string) error {
	if s.AddLabelFunc != nil {
		return s.AddLabelFunc(key, label)
	}
	return nil
}

func (s *Stub) RemoveLabel(key, label string) error {
	if s.RemoveLabelFunc != nil {
		return s.RemoveLabelFunc(key, label)
	}
	return nil
}

func (s *Stub) SetFieldValue(key, field, value string) error {
	if s.SetFieldValueFunc != nil {
		return s.SetFieldValueFunc(key, field, value)
	}
	return nil
}

func (s *Stub) DownloadAttachment(url string) ([]byte, error) {
	if s.DownloadAttachmentFunc != nil {
		return s.DownloadAttachmentFunc(url)
	}
	return nil, nil
}
