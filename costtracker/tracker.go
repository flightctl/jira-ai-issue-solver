// Package costtracker provides AI session cost tracking with daily
// budget enforcement.
//
// A [Tracker] accumulates costs over the course of a calendar day and
// can be queried to determine whether a configured budget has been
// exceeded. Costs are persisted to disk so totals survive process
// restarts. The daily total resets automatically when the date
// changes.
package costtracker

// Tracker tracks AI session costs for budget enforcement.
type Tracker interface {
	// Record adds the given amount to the daily total and persists
	// the updated total to disk. Negative amounts are ignored.
	Record(amount float64)

	// DailyTotal returns the current day's accumulated cost.
	DailyTotal() float64

	// BudgetExceeded reports whether the daily total has reached
	// or exceeded the configured budget. Always returns false when
	// no budget is configured (maxBudget <= 0).
	BudgetExceeded() bool
}
