package executor

import (
	"errors"
	"path/filepath"

	"go.uber.org/zap"

	"jira-ai-issue-solver/costtracker"
)

// errTicketCostCapExceeded is returned when a ticket's cumulative AI
// session cost has reached or exceeded the configured per-ticket cap.
// The Coordinator treats this as a regular failure, incrementing the
// retry count until retries are exhausted.
var errTicketCostCapExceeded = errors.New("per-ticket cost cap exceeded")

// ticketCostPath is the path, relative to the workspace root, where
// per-ticket cumulative cost is persisted.
const ticketCostPath = ".ai-session/ticket-cost.json"

// checkTicketCostCap returns true if the accumulated cost for the
// ticket has reached or exceeded the configured cap. Returns false
// when the cap is disabled (maxCost <= 0) or the cost file does not
// exist yet.
func (p *Pipeline) checkTicketCostCap(logger *zap.Logger, wsPath string, maxCost float64) bool {
	if maxCost <= 0 {
		return false
	}
	path := filepath.Join(wsPath, ticketCostPath)
	tracker := costtracker.NewTicketCostTracker(path, maxCost, logger)
	return tracker.Exceeded()
}

// ticketCostCapExceeded checks if the ticket's workspace exists and
// its accumulated cost has reached or exceeded the cap. Returns false
// when no workspace exists (first run) or the cap is disabled.
func (p *Pipeline) ticketCostCapExceeded(logger *zap.Logger, ticketKey string, maxCost float64) bool {
	wsPath, found := p.workspaces.Find(ticketKey)
	if !found {
		return false
	}
	return p.checkTicketCostCap(logger, wsPath, maxCost)
}

// recordTicketCost adds the session cost to the per-ticket cost file.
func (p *Pipeline) recordTicketCost(logger *zap.Logger, wsPath string, maxCost float64, cost float64) {
	if cost <= 0 {
		return
	}
	path := filepath.Join(wsPath, ticketCostPath)
	tracker := costtracker.NewTicketCostTracker(path, maxCost, logger)
	tracker.Record(cost)
}
