// Package costtrackertest provides test doubles for the costtracker package.
package costtrackertest

import "jira-ai-issue-solver/costtracker"

// Compile-time check that Stub implements costtracker.Tracker.
var _ costtracker.Tracker = (*Stub)(nil)

// Stub is a test double for [costtracker.Tracker].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type Stub struct {
	RecordFunc         func(amount float64)
	DailyTotalFunc     func() float64
	BudgetExceededFunc func() bool
}

func (s *Stub) Record(amount float64) {
	if s.RecordFunc != nil {
		s.RecordFunc(amount)
	}
}

func (s *Stub) DailyTotal() float64 {
	if s.DailyTotalFunc != nil {
		return s.DailyTotalFunc()
	}
	return 0
}

func (s *Stub) BudgetExceeded() bool {
	if s.BudgetExceededFunc != nil {
		return s.BudgetExceededFunc()
	}
	return false
}
