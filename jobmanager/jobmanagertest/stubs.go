// Package jobmanagertest provides test doubles for the jobmanager package.
package jobmanagertest

import "jira-ai-issue-solver/jobmanager"

// Compile-time check that Stub implements jobmanager.Manager.
var _ jobmanager.Manager = (*Stub)(nil)

// Stub is a test double for [jobmanager.Manager].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	SubmitFunc     func(event jobmanager.Event) (*jobmanager.Job, error)
	CompleteFunc   func(jobID string, result jobmanager.JobResult) error
	FailFunc       func(jobID string, err error) error
	GetJobFunc     func(jobID string) (*jobmanager.Job, error)
	ActiveJobsFunc func() []*jobmanager.Job
}

func (s *Stub) Submit(event jobmanager.Event) (*jobmanager.Job, error) {
	if s.SubmitFunc != nil {
		return s.SubmitFunc(event)
	}
	return &jobmanager.Job{}, nil
}

func (s *Stub) Complete(jobID string, result jobmanager.JobResult) error {
	if s.CompleteFunc != nil {
		return s.CompleteFunc(jobID, result)
	}
	return nil
}

func (s *Stub) Fail(jobID string, err error) error {
	if s.FailFunc != nil {
		return s.FailFunc(jobID, err)
	}
	return nil
}

func (s *Stub) GetJob(jobID string) (*jobmanager.Job, error) {
	if s.GetJobFunc != nil {
		return s.GetJobFunc(jobID)
	}
	return nil, nil
}

func (s *Stub) ActiveJobs() []*jobmanager.Job {
	if s.ActiveJobsFunc != nil {
		return s.ActiveJobsFunc()
	}
	return nil
}
