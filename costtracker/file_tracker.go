package costtracker

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Compile-time check that FileTracker implements Tracker.
var _ Tracker = (*FileTracker)(nil)

// dateFormat is the Go reference-time layout used for daily cost
// file dates.
const dateFormat = "2006-01-02"

// costRecord is the on-disk representation of a daily cost total.
type costRecord struct {
	Date     string  `json:"date"`
	TotalUSD float64 `json:"total_usd"`
}

// FileTracker persists daily AI cost totals to a JSON file and
// enforces an optional budget limit.
type FileTracker struct {
	mu        sync.Mutex
	path      string
	maxBudget float64
	total     float64
	date      string
	clockFunc func() time.Time
	logger    *zap.Logger
}

// NewFileTracker creates a FileTracker that persists costs to the
// given path and enforces maxBudget as a daily limit in USD. A
// maxBudget of zero or negative disables budget enforcement. The
// constructor loads any existing state from disk and resets the total
// if the stored date differs from today.
func NewFileTracker(path string, maxBudget float64, logger *zap.Logger) (*FileTracker, error) {
	return NewFileTrackerWithClock(path, maxBudget, time.Now, logger)
}

// NewFileTrackerWithClock is like [NewFileTracker] but accepts a
// custom clock function for testing.
func NewFileTrackerWithClock(path string, maxBudget float64, clock func() time.Time, logger *zap.Logger) (*FileTracker, error) {
	ft := &FileTracker{
		path:      path,
		maxBudget: maxBudget,
		clockFunc: clock,
		logger:    logger,
	}

	ft.loadFromDisk()
	ft.resetIfStale()

	return ft, nil
}

// Record adds the given amount to the daily total and persists the
// updated total to disk. Negative amounts are ignored.
func (ft *FileTracker) Record(amount float64) {
	if amount < 0 {
		return
	}

	ft.mu.Lock()
	defer ft.mu.Unlock()

	ft.resetIfStale()
	ft.total += amount
	ft.writeToDisk()

	ft.logger.Debug("Cost recorded",
		zap.Float64("amount", amount),
		zap.Float64("daily_total", ft.total))
}

// DailyTotal returns the current day's accumulated cost.
func (ft *FileTracker) DailyTotal() float64 {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	ft.resetIfStale()
	return ft.total
}

// BudgetExceeded reports whether the daily total has reached or
// exceeded the configured budget. Always returns false when no
// budget is configured (maxBudget <= 0).
func (ft *FileTracker) BudgetExceeded() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	if ft.maxBudget <= 0 {
		return false
	}

	ft.resetIfStale()
	return ft.total >= ft.maxBudget
}

// today returns the current date string using the tracker's clock.
func (ft *FileTracker) today() string {
	return ft.clockFunc().Format(dateFormat)
}

// resetIfStale clears the in-memory total when the date has changed.
// Must be called with ft.mu held.
func (ft *FileTracker) resetIfStale() {
	today := ft.today()
	if ft.date != today {
		ft.date = today
		ft.total = 0
	}
}

// loadFromDisk reads the cost record from the JSON file. Missing or
// corrupt files are handled gracefully: the tracker starts fresh and
// a warning is logged.
func (ft *FileTracker) loadFromDisk() {
	data, err := os.ReadFile(ft.path) // #nosec G304 -- path is caller-controlled workspace file
	if err != nil {
		if !os.IsNotExist(err) {
			ft.logger.Warn("failed to read cost file, starting fresh",
				zap.String("path", ft.path),
				zap.Error(err))
		}
		return
	}

	var rec costRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		ft.logger.Warn("corrupt cost file, starting fresh",
			zap.String("path", ft.path),
			zap.Error(err))
		return
	}

	ft.date = rec.Date
	ft.total = rec.TotalUSD
}

// writeToDisk persists the current cost record to the JSON file.
// Write failures are logged but do not lose in-memory state.
// Must be called with ft.mu held.
func (ft *FileTracker) writeToDisk() {
	rec := costRecord{
		Date:     ft.date,
		TotalUSD: ft.total,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		ft.logger.Warn("failed to marshal cost record",
			zap.Error(err))
		return
	}

	if err := os.WriteFile(ft.path, data, 0o600); err != nil {
		ft.logger.Warn("failed to write cost file",
			zap.String("path", ft.path),
			zap.Error(err))
	}
}
