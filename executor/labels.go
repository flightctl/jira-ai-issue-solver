package executor

import (
	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// setFailureLabel applies the given failure label to a ticket and
// removes the other two configured failure labels (mutual exclusivity).
// If targetLabel is empty, only clears the others. All operations are
// best-effort: errors are logged but never propagated.
func (p *Pipeline) setFailureLabel(
	logger *zap.Logger,
	ticketKey string,
	fl models.FailureLabels,
	targetLabel string,
) {
	for _, label := range fl.All() {
		if label != "" && label != targetLabel {
			if err := p.tracker.RemoveLabel(ticketKey, label); err != nil {
				logger.Debug("Failed to remove failure label",
					zap.String("label", label), zap.Error(err))
			}
		}
	}

	if targetLabel != "" {
		if err := p.tracker.AddLabel(ticketKey, targetLabel); err != nil {
			logger.Warn("Failed to add failure label",
				zap.String("label", targetLabel), zap.Error(err))
		}
	}
}

// setLifecycleLabel applies the given lifecycle label to a ticket and
// removes the other configured lifecycle labels (mutual exclusivity).
// If targetLabel is empty, only clears the others. All operations are
// best-effort: errors are logged but never propagated.
func (p *Pipeline) setLifecycleLabel(
	logger *zap.Logger,
	ticketKey string,
	ll models.LifecycleLabels,
	targetLabel string,
) {
	for _, label := range ll.All() {
		if label != "" && label != targetLabel {
			if err := p.tracker.RemoveLabel(ticketKey, label); err != nil {
				logger.Debug("Failed to remove lifecycle label",
					zap.String("label", label), zap.Error(err))
			}
		}
	}

	if targetLabel != "" {
		if err := p.tracker.AddLabel(ticketKey, targetLabel); err != nil {
			logger.Warn("Failed to add lifecycle label",
				zap.String("label", targetLabel), zap.Error(err))
		}
	}
}

// clearFailureLabels removes all configured failure labels from a
// ticket. Called on pipeline success paths to clean up labels from
// prior failed attempts. All operations are best-effort.
func (p *Pipeline) clearFailureLabels(
	logger *zap.Logger,
	ticketKey string,
	fl models.FailureLabels,
) {
	for _, label := range fl.All() {
		if label != "" {
			if err := p.tracker.RemoveLabel(ticketKey, label); err != nil {
				logger.Debug("Failed to remove failure label",
					zap.String("label", label), zap.Error(err))
			}
		}
	}
}
