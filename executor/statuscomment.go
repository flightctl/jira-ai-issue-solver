package executor

import (
	"fmt"
	"strings"
	"time"

	"jira-ai-issue-solver/models"
)

const statusCommentMarker = "[AI-BOT-STATUS]"

// formatStatusComment builds a failure status comment body.
// When maxRetries is negative, retry limits are disabled and the
// attempt count is shown without a total.
func formatStatusComment(attempt, maxRetries int, err error, now time.Time) string {
	var b strings.Builder
	b.WriteString(statusCommentMarker)

	if maxRetries < 0 {
		fmt.Fprintf(&b, " AI processing failed (attempt %d)", attempt)
	} else {
		fmt.Fprintf(&b, " AI processing failed (attempt %d of %d)", attempt, maxRetries+1)
	}

	fmt.Fprintf(&b, "\n\nError: %s", err.Error())
	fmt.Fprintf(&b, "\n\nLast attempted: %s", now.UTC().Format(time.RFC3339))

	return b.String()
}

// findStatusComment returns the first comment whose body contains
// the status marker, or nil if none exists.
func findStatusComment(comments []models.Comment) *models.Comment {
	for i := range comments {
		if strings.Contains(comments[i].Body, statusCommentMarker) {
			return &comments[i]
		}
	}
	return nil
}
