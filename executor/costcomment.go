package executor

import (
	"fmt"
	"strings"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

const costCommentMarker = "<!-- AI-BOT-COST -->"

type costEntry struct {
	Label string
	Cost  float64
}

// formatCostComment renders a cost comment body from a list of entries.
func formatCostComment(entries []costEntry) string {
	var b strings.Builder
	b.WriteString(costCommentMarker)
	b.WriteString("\n**AI Session Costs**\n\n")
	b.WriteString("| Session | Cost |\n")
	b.WriteString("|---------|------|\n")

	var total float64
	for _, e := range entries {
		fmt.Fprintf(&b, "| %s | $%.2f |\n", e.Label, e.Cost)
		total += e.Cost
	}
	fmt.Fprintf(&b, "| **Total** | **$%.2f** |\n", total)

	return b.String()
}

// parseCostComment extracts cost entries from an existing cost comment.
// Returns nil if the body does not contain a parseable cost table.
func parseCostComment(body string) []costEntry {
	if !strings.Contains(body, costCommentMarker) {
		return nil
	}

	var entries []costEntry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}

		// Skip header, separator, and total rows.
		if strings.Contains(line, "---") ||
			strings.Contains(line, "Session") ||
			strings.Contains(line, "**Total**") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		label := strings.TrimSpace(parts[1])
		costStr := strings.TrimSpace(parts[2])
		costStr = strings.TrimPrefix(costStr, "$")

		var cost float64
		if _, err := fmt.Sscanf(costStr, "%f", &cost); err != nil {
			continue
		}
		entries = append(entries, costEntry{Label: label, Cost: cost})
	}

	return entries
}

// findCostComment returns the existing cost comment from a list of
// issue comments, or nil if none exists.
func findCostComment(comments []models.IssueComment) *models.IssueComment {
	for i := range comments {
		if strings.Contains(comments[i].Body, costCommentMarker) {
			return &comments[i]
		}
	}
	return nil
}

// nextFeedbackLabel derives the sequence label for a feedback entry
// by counting existing feedback entries.
func nextFeedbackLabel(entries []costEntry) string {
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Label, "Feedback") {
			count++
		}
	}
	return fmt.Sprintf("Feedback #%d", count+1)
}

// postOrUpdateCostComment posts or updates a cost comment on a PR.
// If label is "Feedback", the sequence number is auto-derived.
// Errors are logged but not propagated — cost comments are non-critical.
func (p *Pipeline) postOrUpdateCostComment(
	logger *zap.Logger,
	owner, repo string,
	prNumber int,
	cost float64,
	label string,
) {
	if cost <= 0 {
		return
	}

	comments, err := p.git.ListIssueComments(owner, repo, prNumber)
	if err != nil {
		logger.Warn("Failed to list PR comments for cost tracking",
			zap.String("owner", owner), zap.String("repo", repo),
			zap.Int("pr_number", prNumber), zap.Error(err))
		return
	}

	existing := findCostComment(comments)
	if existing != nil {
		entries := parseCostComment(existing.Body)
		if label == "Feedback" {
			label = nextFeedbackLabel(entries)
		}
		entries = append(entries, costEntry{Label: label, Cost: cost})
		body := formatCostComment(entries)

		if err := p.git.UpdateIssueComment(owner, repo, existing.ID, body); err != nil {
			logger.Warn("Failed to update cost comment",
				zap.String("owner", owner), zap.String("repo", repo),
				zap.Int("pr_number", prNumber), zap.Error(err))
		}
		return
	}

	if label == "Feedback" {
		label = "Feedback #1"
	}
	body := formatCostComment([]costEntry{{Label: label, Cost: cost}})

	if err := p.git.PostIssueComment(owner, repo, prNumber, body); err != nil {
		logger.Warn("Failed to post cost comment",
			zap.String("owner", owner), zap.String("repo", repo),
			zap.Int("pr_number", prNumber), zap.Error(err))
	}
}
