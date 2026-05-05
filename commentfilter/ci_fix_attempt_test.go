package commentfilter

import (
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
)

func TestCIFixAttemptMarker(t *testing.T) {
	failures := []models.CheckRunFailure{
		{ID: 300},
		{ID: 100},
		{ID: 200},
	}
	marker := CIFixAttemptMarker(failures, "abc123")

	if !strings.Contains(marker, "abc123") {
		t.Error("marker should contain commit SHA")
	}
	if !strings.Contains(marker, "<!-- ci-fix-attempt: 100,200,300 -->") {
		t.Errorf("IDs should be sorted: %s", marker)
	}
}

func TestCIFixAttemptMarker_SingleFailure(t *testing.T) {
	failures := []models.CheckRunFailure{{ID: 42}}
	marker := CIFixAttemptMarker(failures, "def456")

	if !strings.Contains(marker, "<!-- ci-fix-attempt: 42 -->") {
		t.Errorf("unexpected marker: %s", marker)
	}
}

func TestCountCIFixAttempts(t *testing.T) {
	comments := []models.PRComment{
		{Author: models.Author{Username: "bot"}, Body: "CI failures addressed in abc.\n<!-- ci-fix-attempt: 1,2 -->"},
		{Author: models.Author{Username: "reviewer"}, Body: "looks good"},
		{Author: models.Author{Username: "bot"}, Body: "CI failures addressed in def.\n<!-- ci-fix-attempt: 3 -->"},
		{Author: models.Author{Username: "bot"}, Body: "Addressed in xyz."},
	}

	count := CountCIFixAttempts(comments, "bot")
	if count != 2 {
		t.Errorf("expected 2 attempts, got %d", count)
	}
}

func TestCountCIFixAttempts_NoBotComments(t *testing.T) {
	comments := []models.PRComment{
		{Author: models.Author{Username: "reviewer"}, Body: "fix the build"},
	}

	count := CountCIFixAttempts(comments, "bot")
	if count != 0 {
		t.Errorf("expected 0 attempts, got %d", count)
	}
}

func TestCountCIFixAttempts_CaseInsensitive(t *testing.T) {
	comments := []models.PRComment{
		{Author: models.Author{Username: "Bot[bot]"}, Body: "<!-- ci-fix-attempt: 1 -->"},
	}

	count := CountCIFixAttempts(comments, "bot")
	if count != 1 {
		t.Errorf("expected 1 attempt, got %d", count)
	}
}
