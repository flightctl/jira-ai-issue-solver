package executor

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// setFailureLabel applies the given failure label to a ticket and
// removes the other configured failure labels (mutual exclusivity).
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

// validateForkMode checks that fork-mode projects have a resolved
// GitHub username for the ticket assignee. Returns an error and
// applies the ForkUserMissing label when the mapping is missing.
// No-op for direct-mode projects.
func (p *Pipeline) validateForkMode(
	logger *zap.Logger,
	ticketKey string,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
) error {
	if !settings.ForkMode || settings.GitHubUsername != "" {
		return nil
	}

	assigneeDesc := "unassigned"
	if workItem.Assignee != nil {
		assigneeDesc = workItem.Assignee.Email
	}

	if settings.FailureLabels.ForkUserMissing != "" {
		p.setFailureLabel(logger, ticketKey, settings.FailureLabels, settings.FailureLabels.ForkUserMissing)
	}

	if !settings.DisableErrorComments {
		detail := fmt.Sprintf(
			"fork mode requires assignee GitHub mapping: ticket assignee %s has no entry in jira.assignee_to_github_username",
			assigneeDesc,
		)
		comment := fmt.Sprintf(
			"%s Fork mode validation failed\n\nError: %s\n\nAdd the assignee's GitHub username to jira.assignee_to_github_username in the bot configuration.",
			statusCommentMarker, detail,
		)
		if err := p.tracker.AddComment(ticketKey, comment); err != nil {
			logger.Warn("Failed to post fork-mode error comment", zap.Error(err))
		}
	}

	return errors.New("fork mode requires assignee GitHub mapping: ticket assignee has no entry in jira.assignee_to_github_username")
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
