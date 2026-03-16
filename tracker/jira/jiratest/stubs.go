// Package jiratest provides test doubles for the jira package.
package jiratest

import (
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/tracker/jira"
)

// Compile-time check that Stub implements jira.JiraClient.
var _ jira.JiraClient = (*Stub)(nil)

// Stub is a test double for [jira.JiraClient].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	SearchTicketsFunc           func(jql string) (*models.JiraSearchResponse, error)
	GetTicketFunc               func(key string) (*models.JiraTicketResponse, error)
	GetTicketSecurityLevelFunc  func(key string) (*models.JiraSecurity, error)
	UpdateTicketStatusFunc      func(key string, status string) error
	AddCommentFunc              func(key string, comment string) error
	UpdateTicketFieldByNameFunc func(key string, fieldName string, value interface{}) error
	GetFieldIDByNameFunc        func(fieldName string) (string, error)
}

func (s *Stub) SearchTickets(jql string) (*models.JiraSearchResponse, error) {
	if s.SearchTicketsFunc != nil {
		return s.SearchTicketsFunc(jql)
	}
	return &models.JiraSearchResponse{}, nil
}

func (s *Stub) GetTicket(key string) (*models.JiraTicketResponse, error) {
	if s.GetTicketFunc != nil {
		return s.GetTicketFunc(key)
	}
	return nil, nil
}

func (s *Stub) GetTicketSecurityLevel(key string) (*models.JiraSecurity, error) {
	if s.GetTicketSecurityLevelFunc != nil {
		return s.GetTicketSecurityLevelFunc(key)
	}
	return nil, nil
}

func (s *Stub) UpdateTicketStatus(key string, status string) error {
	if s.UpdateTicketStatusFunc != nil {
		return s.UpdateTicketStatusFunc(key, status)
	}
	return nil
}

func (s *Stub) AddComment(key string, comment string) error {
	if s.AddCommentFunc != nil {
		return s.AddCommentFunc(key, comment)
	}
	return nil
}

func (s *Stub) UpdateTicketFieldByName(key string, fieldName string, value interface{}) error {
	if s.UpdateTicketFieldByNameFunc != nil {
		return s.UpdateTicketFieldByNameFunc(key, fieldName, value)
	}
	return nil
}

func (s *Stub) GetFieldIDByName(fieldName string) (string, error) {
	if s.GetFieldIDByNameFunc != nil {
		return s.GetFieldIDByNameFunc(fieldName)
	}
	return fieldName, nil
}
