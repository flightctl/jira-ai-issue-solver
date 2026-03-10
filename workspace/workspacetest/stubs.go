// Package workspacetest provides test doubles for the workspace package.
package workspacetest

import (
	"time"

	"jira-ai-issue-solver/workspace"
)

// Compile-time check that Stub implements workspace.Manager.
var _ workspace.Manager = (*Stub)(nil)

// Stub is a test double for [workspace.Manager].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	CreateFunc          func(ticketKey, repoURL string) (string, error)
	FindFunc            func(ticketKey string) (string, bool)
	FindOrCreateFunc    func(ticketKey, repoURL string) (string, bool, error)
	CleanupFunc         func(ticketKey string) error
	CleanupStaleFunc    func(maxAge time.Duration) (int, error)
	CleanupByFilterFunc func(shouldRemove func(ticketKey string) bool) (int, error)
	ListFunc            func() ([]workspace.Info, error)
}

func (s *Stub) Create(ticketKey, repoURL string) (string, error) {
	if s.CreateFunc != nil {
		return s.CreateFunc(ticketKey, repoURL)
	}
	return "", nil
}

func (s *Stub) Find(ticketKey string) (string, bool) {
	if s.FindFunc != nil {
		return s.FindFunc(ticketKey)
	}
	return "", false
}

func (s *Stub) FindOrCreate(ticketKey, repoURL string) (string, bool, error) {
	if s.FindOrCreateFunc != nil {
		return s.FindOrCreateFunc(ticketKey, repoURL)
	}
	return "", false, nil
}

func (s *Stub) Cleanup(ticketKey string) error {
	if s.CleanupFunc != nil {
		return s.CleanupFunc(ticketKey)
	}
	return nil
}

func (s *Stub) CleanupStale(maxAge time.Duration) (int, error) {
	if s.CleanupStaleFunc != nil {
		return s.CleanupStaleFunc(maxAge)
	}
	return 0, nil
}

func (s *Stub) CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error) {
	if s.CleanupByFilterFunc != nil {
		return s.CleanupByFilterFunc(shouldRemove)
	}
	return 0, nil
}

func (s *Stub) List() ([]workspace.Info, error) {
	if s.ListFunc != nil {
		return s.ListFunc()
	}
	return []workspace.Info{}, nil
}
