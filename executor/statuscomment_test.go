package executor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"jira-ai-issue-solver/models"
)

func TestFormatStatusComment(t *testing.T) {
	now := time.Date(2026, 5, 5, 14, 30, 0, 0, time.UTC)
	jobErr := errors.New("container exited with code 1")

	t.Run("includes marker and attempt info", func(t *testing.T) {
		body := formatStatusComment(2, 3, "ai-retry", jobErr, now)

		for _, want := range []string{
			statusCommentMarker,
			"attempt 2 of 4",
			"container exited with code 1",
			"2026-05-05T14:30:00Z",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got:\n%s", want, body)
			}
		}
	})

	t.Run("negative maxRetries omits total", func(t *testing.T) {
		body := formatStatusComment(3, -1, "", jobErr, now)

		if !strings.Contains(body, "attempt 3)") {
			t.Errorf("body should show attempt without total, got:\n%s", body)
		}
		if strings.Contains(body, " of ") {
			t.Errorf("body should not contain 'of' when retries are unlimited, got:\n%s", body)
		}
	})

	t.Run("retry hint shown when exhausted", func(t *testing.T) {
		body := formatStatusComment(4, 3, "ai-retry", jobErr, now)

		want := `add the label "ai-retry"`
		if !strings.Contains(body, want) {
			t.Errorf("body missing retry hint %q, got:\n%s", want, body)
		}
	})

	t.Run("no retry hint before exhaustion", func(t *testing.T) {
		body := formatStatusComment(2, 3, "ai-retry", jobErr, now)

		if strings.Contains(body, "add the label") {
			t.Errorf("body should not contain retry hint before exhaustion, got:\n%s", body)
		}
	})

	t.Run("no retry hint when label empty", func(t *testing.T) {
		body := formatStatusComment(4, 3, "", jobErr, now)

		if strings.Contains(body, "add the label") {
			t.Errorf("body should not contain retry hint when label is empty, got:\n%s", body)
		}
	})
}

func TestFindStatusComment(t *testing.T) {
	t.Run("returns matching comment", func(t *testing.T) {
		comments := []models.Comment{
			{ID: "1", Body: "Some other comment"},
			{ID: "2", Body: statusCommentMarker + " AI processing failed (attempt 1 of 3)\n\nError: boom"},
			{ID: "3", Body: "Another comment"},
		}

		got := findStatusComment(comments)
		if got == nil {
			t.Fatal("expected to find status comment")
		}
		if got.ID != "2" {
			t.Errorf("got comment ID %q, want %q", got.ID, "2")
		}
	})

	t.Run("returns nil when no marker", func(t *testing.T) {
		comments := []models.Comment{
			{ID: "1", Body: "Normal comment"},
			{ID: "2", Body: "[AI-BOT-PR] https://github.com/org/repo/pull/1"},
		}

		if got := findStatusComment(comments); got != nil {
			t.Errorf("expected nil, got comment ID %q", got.ID)
		}
	})

	t.Run("returns nil for empty list", func(t *testing.T) {
		if got := findStatusComment([]models.Comment{}); got != nil {
			t.Errorf("expected nil, got comment ID %q", got.ID)
		}
	})
}
