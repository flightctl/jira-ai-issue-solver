// Package resolvertest provides test doubles for the projectresolver
// package.
package resolvertest

import "jira-ai-issue-solver/models"

// Stub satisfies both ProjectResolver (executor, recovery) and
// RepoLocator (scanner) consumer-defined interfaces.
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	ResolveProjectFunc func(workItem models.WorkItem) (*models.ProjectSettings, error)
	LocateRepoFunc     func(workItem models.WorkItem) (string, string, error)
}

// ResolveProject delegates to ResolveProjectFunc if set, otherwise
// returns a zero-value ProjectSettings.
func (s *Stub) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	if s.ResolveProjectFunc != nil {
		return s.ResolveProjectFunc(workItem)
	}
	return &models.ProjectSettings{}, nil
}

// LocateRepo delegates to LocateRepoFunc if set, otherwise returns
// empty strings.
func (s *Stub) LocateRepo(workItem models.WorkItem) (string, string, error) {
	if s.LocateRepoFunc != nil {
		return s.LocateRepoFunc(workItem)
	}
	return "", "", nil
}
