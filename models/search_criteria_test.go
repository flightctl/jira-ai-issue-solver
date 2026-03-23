package models_test

import (
	"testing"

	"jira-ai-issue-solver/models"
)

func TestSearchCriteria_Validate(t *testing.T) {
	t.Run("accepts StatusByType only", func(t *testing.T) {
		c := models.SearchCriteria{
			StatusByType: map[string][]string{"Bug": {"Open"}},
		}
		if err := c.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("accepts Statuses only", func(t *testing.T) {
		c := models.SearchCriteria{
			Statuses: []string{"In Progress"},
		}
		if err := c.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("accepts empty criteria", func(t *testing.T) {
		c := models.SearchCriteria{}
		if err := c.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects both StatusByType and Statuses", func(t *testing.T) {
		c := models.SearchCriteria{
			StatusByType: map[string][]string{"Bug": {"Open"}},
			Statuses:     []string{"In Progress"},
		}
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); got != "SearchCriteria: StatusByType and Statuses are mutually exclusive" {
			t.Errorf("unexpected error message: %q", got)
		}
	})
}
