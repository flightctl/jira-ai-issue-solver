package executor

import (
	"testing"

	"jira-ai-issue-solver/models"
)

func TestFilterIgnoredChecks(t *testing.T) {
	failures := []models.CheckRunFailure{
		{Name: "lint"},
		{Name: "License/CLA"},
		{Name: "test"},
	}

	filtered := filterIgnoredChecks(failures, []string{"license/cla"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(filtered))
	}
	if filtered[0].Name != "lint" || filtered[1].Name != "test" {
		t.Errorf("unexpected result: %v", filtered)
	}
}

func TestFilterIgnoredChecks_EmptyIgnoreList(t *testing.T) {
	failures := []models.CheckRunFailure{{Name: "lint"}}
	filtered := filterIgnoredChecks(failures, nil)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(filtered))
	}
}

func TestFilterIgnoredChecks_AllIgnored(t *testing.T) {
	failures := []models.CheckRunFailure{{Name: "lint"}}
	filtered := filterIgnoredChecks(failures, []string{"lint"})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 failures, got %d", len(filtered))
	}
}

func TestFilterPreExistingFailures(t *testing.T) {
	prFailures := []models.CheckRunFailure{
		{Name: "lint"},
		{Name: "test"},
		{Name: "build"},
	}
	baseFailures := []models.CheckRunFailure{
		{Name: "lint"},
	}

	filtered := filterPreExistingFailures(prFailures, baseFailures)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(filtered))
	}
	if filtered[0].Name != "test" || filtered[1].Name != "build" {
		t.Errorf("unexpected: %v", filtered)
	}
}

func TestFilterPreExistingFailures_NoBaseFailures(t *testing.T) {
	prFailures := []models.CheckRunFailure{{Name: "lint"}}
	filtered := filterPreExistingFailures(prFailures, nil)
	if len(filtered) != 1 {
		t.Errorf("expected 1, got %d", len(filtered))
	}
}

func TestFilterPreExistingFailures_AllPreExisting(t *testing.T) {
	prFailures := []models.CheckRunFailure{{Name: "lint"}}
	baseFailures := []models.CheckRunFailure{{Name: "lint"}}
	filtered := filterPreExistingFailures(prFailures, baseFailures)
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

func TestSortCheckRunFailures(t *testing.T) {
	failures := []models.CheckRunFailure{
		{Name: "lint"},
		{Name: "build"},
		{Name: "test"},
	}
	sortCheckRunFailures(failures)
	if failures[0].Name != "build" || failures[1].Name != "lint" || failures[2].Name != "test" {
		t.Errorf("unexpected order: %v", failures)
	}
}
