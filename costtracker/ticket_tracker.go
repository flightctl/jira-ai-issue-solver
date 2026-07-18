package costtracker

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

// ticketCostRecord is the on-disk representation of a ticket's
// cumulative cost.
type ticketCostRecord struct {
	TotalUSD float64 `json:"total_usd"`
}

// TicketCostTracker persists cumulative AI session costs for a single
// ticket to a JSON file. Unlike [FileTracker], costs accumulate
// indefinitely (no daily reset) and the tracker is tied to the
// workspace lifecycle.
type TicketCostTracker struct {
	path   string
	maxCap float64
	total  float64
	logger *zap.Logger
}

// NewTicketCostTracker creates a tracker that persists costs to the
// given path and enforces maxCap as a per-ticket limit in USD. A
// maxCap of zero or negative disables cap enforcement. The
// constructor loads any existing state from disk; missing or corrupt
// files start at zero.
func NewTicketCostTracker(path string, maxCap float64, logger *zap.Logger) *TicketCostTracker {
	t := &TicketCostTracker{
		path:   path,
		maxCap: maxCap,
		logger: logger,
	}
	t.loadFromDisk()
	return t
}

// Record adds the given amount to the cumulative total and persists
// the updated total to disk. Non-positive, NaN, and infinite amounts
// are ignored.
func (t *TicketCostTracker) Record(amount float64) {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return
	}

	t.total += amount
	t.writeToDisk()

	t.logger.Debug("Ticket cost recorded",
		zap.Float64("amount", amount),
		zap.Float64("ticket_total", t.total))
}

// Total returns the cumulative cost for this ticket.
func (t *TicketCostTracker) Total() float64 {
	return t.total
}

// Exceeded reports whether the cumulative cost has reached or
// exceeded the configured cap. Always returns false when no cap is
// configured (maxCap <= 0).
func (t *TicketCostTracker) Exceeded() bool {
	if t.maxCap <= 0 {
		return false
	}
	return t.total >= t.maxCap
}

// loadFromDisk reads the cost record from the JSON file. Missing or
// corrupt files are handled gracefully: the tracker starts at zero
// and a warning is logged for corrupt files.
func (t *TicketCostTracker) loadFromDisk() {
	data, err := os.ReadFile(t.path) // #nosec G304 -- path is caller-controlled workspace file
	if err != nil {
		if !os.IsNotExist(err) {
			t.logger.Warn("failed to read ticket cost file, starting fresh",
				zap.String("path", t.path),
				zap.Error(err))
		}
		return
	}

	var rec ticketCostRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.logger.Warn("corrupt ticket cost file, starting fresh",
			zap.String("path", t.path),
			zap.Error(err))
		return
	}

	if math.IsNaN(rec.TotalUSD) || math.IsInf(rec.TotalUSD, 0) || rec.TotalUSD < 0 {
		t.logger.Warn("invalid total in ticket cost file, starting fresh",
			zap.String("path", t.path),
			zap.Float64("total_usd", rec.TotalUSD))
		return
	}

	t.total = rec.TotalUSD
}

// writeToDisk persists the current cost record to the JSON file,
// creating parent directories as needed. Write failures are logged
// but do not lose in-memory state.
func (t *TicketCostTracker) writeToDisk() {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o750); err != nil {
		t.logger.Warn("failed to create ticket cost directory",
			zap.String("path", t.path),
			zap.Error(err))
		return
	}

	rec := ticketCostRecord{TotalUSD: t.total}
	data, err := json.Marshal(rec)
	if err != nil {
		t.logger.Warn("failed to marshal ticket cost record",
			zap.Error(err))
		return
	}

	if err := os.WriteFile(t.path, data, 0o600); err != nil {
		t.logger.Warn("failed to write ticket cost file",
			zap.String("path", t.path),
			zap.Error(err))
	}
}
