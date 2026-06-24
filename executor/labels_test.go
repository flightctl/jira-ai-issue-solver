package executor_test

import (
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/models"
)

func TestSetFailureLabel(t *testing.T) {
	fl := models.FailureLabels{
		CIFailing:       "ci-fail",
		Rejected:        "rejected",
		Blocked:         "blocked",
		ForkUserMissing: "fork-missing",
	}

	t.Run("adds target and removes others", func(t *testing.T) {
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", fl, "blocked")

		if len(added) != 1 || added[0] != "blocked" {
			t.Errorf("added = %v, want [blocked]", added)
		}
		if len(removed) != 3 {
			t.Fatalf("removed = %v, want 3 entries", removed)
		}
		wantRemoved := map[string]bool{"ci-fail": true, "rejected": true, "fork-missing": true}
		for _, l := range removed {
			if !wantRemoved[l] {
				t.Errorf("unexpected removal of %q", l)
			}
		}
	})

	t.Run("empty target only removes others", func(t *testing.T) {
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", fl, "")

		if len(added) != 0 {
			t.Errorf("added = %v, want empty", added)
		}
		if len(removed) != 4 {
			t.Errorf("removed = %v, want 4 entries", removed)
		}
	})

	t.Run("skips empty labels in config", func(t *testing.T) {
		partial := models.FailureLabels{Blocked: "blocked"}
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", partial, "blocked")

		if len(added) != 1 || added[0] != "blocked" {
			t.Errorf("added = %v, want [blocked]", added)
		}
		if len(removed) != 0 {
			t.Errorf("removed = %v, want empty (no other labels configured)", removed)
		}
	})

	t.Run("noop when all labels empty", func(t *testing.T) {
		empty := models.FailureLabels{}
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", empty, "")

		if len(added) != 0 {
			t.Errorf("added = %v, want empty", added)
		}
		if len(removed) != 0 {
			t.Errorf("removed = %v, want empty", removed)
		}
	})
}

func TestClearFailureLabels(t *testing.T) {
	t.Run("removes all configured labels", func(t *testing.T) {
		fl := models.FailureLabels{
			CIFailing:       "ci-fail",
			Rejected:        "rejected",
			Blocked:         "blocked",
			ForkUserMissing: "fork-missing",
		}
		var removed []string
		d := newTestDeps(t)
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.ClearFailureLabels(p, zap.NewNop(), "TEST-1", fl)

		if len(removed) != 4 {
			t.Fatalf("removed = %v, want 4 entries", removed)
		}
		want := map[string]bool{"ci-fail": true, "rejected": true, "blocked": true, "fork-missing": true}
		for _, l := range removed {
			if !want[l] {
				t.Errorf("unexpected removal of %q", l)
			}
		}
	})

	t.Run("skips empty labels", func(t *testing.T) {
		fl := models.FailureLabels{Blocked: "blocked"}
		var removed []string
		d := newTestDeps(t)
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.ClearFailureLabels(p, zap.NewNop(), "TEST-1", fl)

		if len(removed) != 1 || removed[0] != "blocked" {
			t.Errorf("removed = %v, want [blocked]", removed)
		}
	})
}

func TestSetLifecycleLabel(t *testing.T) {
	ll := models.LifecycleLabels{
		Queued: "jira-autofix",
		Review: "jira-autofix-review",
		Merged: "jira-autofix-merged",
	}

	t.Run("adds target and removes others", func(t *testing.T) {
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", ll, "jira-autofix-review")

		if len(added) != 1 || added[0] != "jira-autofix-review" {
			t.Errorf("added = %v, want [jira-autofix-review]", added)
		}
		if len(removed) != 2 {
			t.Fatalf("removed = %v, want 2 entries", removed)
		}
		wantRemoved := map[string]bool{"jira-autofix": true, "jira-autofix-merged": true}
		for _, l := range removed {
			if !wantRemoved[l] {
				t.Errorf("unexpected removal of %q", l)
			}
		}
	})

	t.Run("review removes queued", func(t *testing.T) {
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", ll, ll.Review)

		if len(added) != 1 || added[0] != "jira-autofix-review" {
			t.Errorf("added = %v, want [jira-autofix-review]", added)
		}
		removedSet := make(map[string]bool, len(removed))
		for _, l := range removed {
			removedSet[l] = true
		}
		if !removedSet["jira-autofix"] {
			t.Error("expected queued label to be removed")
		}
	})

	t.Run("merged removes review and queued", func(t *testing.T) {
		var removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, _ string) error { return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", ll, ll.Merged)

		removedSet := make(map[string]bool, len(removed))
		for _, l := range removed {
			removedSet[l] = true
		}
		if !removedSet["jira-autofix"] {
			t.Error("expected queued label to be removed")
		}
		if !removedSet["jira-autofix-review"] {
			t.Error("expected review label to be removed")
		}
	})

	t.Run("skips empty labels in config", func(t *testing.T) {
		partial := models.LifecycleLabels{Review: "review"}
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", partial, "review")

		if len(added) != 1 || added[0] != "review" {
			t.Errorf("added = %v, want [review]", added)
		}
		if len(removed) != 0 {
			t.Errorf("removed = %v, want empty", removed)
		}
	})

	t.Run("noop when all labels empty", func(t *testing.T) {
		empty := models.LifecycleLabels{}
		var added, removed []string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", empty, "")

		if len(added) != 0 {
			t.Errorf("added = %v, want empty", added)
		}
		if len(removed) != 0 {
			t.Errorf("removed = %v, want empty", removed)
		}
	})

	t.Run("errors are swallowed", func(t *testing.T) {
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, _ string) error { return fmt.Errorf("add failed") }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return fmt.Errorf("remove failed") }

		p := d.pipeline(t)
		executor.SetLifecycleLabel(p, zap.NewNop(), "TEST-1", ll, "jira-autofix-review")
	})
}

func TestValidateForkMode(t *testing.T) {
	t.Run("passes when fork mode disabled", func(t *testing.T) {
		d := newTestDeps(t)
		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{ForkMode: false, GitHubUsername: ""},
		)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("passes when fork mode disabled even with username", func(t *testing.T) {
		d := newTestDeps(t)
		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{ForkMode: false, GitHubUsername: "alice"},
		)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("passes when fork mode enabled with username", func(t *testing.T) {
		d := newTestDeps(t)
		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{ForkMode: true, GitHubUsername: "alice"},
		)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("fails when fork mode enabled without username and unassigned", func(t *testing.T) {
		var added []string
		var commentBody string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return nil }
		d.tracker.AddCommentFunc = func(_, body string) error { commentBody = body; return nil }

		fl := models.FailureLabels{ForkUserMissing: "fork-missing"}
		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{ForkMode: true, GitHubUsername: "", FailureLabels: fl},
		)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "fork mode requires assignee GitHub mapping") {
			t.Errorf("error = %q, want it to mention fork mode requirement", err.Error())
		}
		if strings.Contains(err.Error(), "unassigned") {
			t.Error("error should not contain assignee details (PII redaction)")
		}
		if len(added) != 1 || added[0] != "fork-missing" {
			t.Errorf("added = %v, want [fork-missing]", added)
		}
		if !strings.Contains(commentBody, "[AI-BOT-STATUS]") {
			t.Errorf("comment body = %q, want it to contain [AI-BOT-STATUS]", commentBody)
		}
		if !strings.Contains(commentBody, "unassigned") {
			t.Errorf("comment body = %q, want it to mention 'unassigned'", commentBody)
		}
	})

	t.Run("fails when fork mode enabled with assignee not in mapping", func(t *testing.T) {
		var added []string
		var commentBody string
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return nil }
		d.tracker.AddCommentFunc = func(_, body string) error { commentBody = body; return nil }

		fl := models.FailureLabels{ForkUserMissing: "fork-missing"}
		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{
				Key:      "TEST-1",
				Assignee: &models.Author{Email: "bob@example.com"},
			},
			&models.ProjectSettings{ForkMode: true, GitHubUsername: "", FailureLabels: fl},
		)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "fork mode requires assignee GitHub mapping") {
			t.Errorf("error = %q, want it to mention fork mode requirement", err.Error())
		}
		if strings.Contains(err.Error(), "bob@example.com") {
			t.Error("error should not contain assignee email (PII redaction)")
		}
		if len(added) != 1 || added[0] != "fork-missing" {
			t.Errorf("added = %v, want [fork-missing]", added)
		}
		if !strings.Contains(commentBody, "[AI-BOT-STATUS]") {
			t.Errorf("comment body = %q, want it to contain [AI-BOT-STATUS]", commentBody)
		}
	})

	t.Run("no label applied when fork_user_missing label not configured", func(t *testing.T) {
		var added []string
		var commentPosted bool
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, label string) error { added = append(added, label); return nil }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return nil }
		d.tracker.AddCommentFunc = func(_, _ string) error { commentPosted = true; return nil }

		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{ForkMode: true, GitHubUsername: "", FailureLabels: models.FailureLabels{}},
		)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if len(added) != 0 {
			t.Errorf("added = %v, want empty (label not configured)", added)
		}
		if !commentPosted {
			t.Error("expected status comment to be posted even when label not configured")
		}
	})

	t.Run("skips comment when error comments disabled", func(t *testing.T) {
		var commentPosted bool
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, _ string) error { return nil }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return nil }
		d.tracker.AddCommentFunc = func(_, _ string) error { commentPosted = true; return nil }

		p := d.pipeline(t)

		err := executor.ValidateForkMode(p, zap.NewNop(), "TEST-1",
			&models.WorkItem{Key: "TEST-1"},
			&models.ProjectSettings{
				ForkMode:             true,
				GitHubUsername:       "",
				DisableErrorComments: true,
			},
		)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if commentPosted {
			t.Error("comment should not be posted when DisableErrorComments is true")
		}
	})
}

func TestSetFailureLabel_ErrorsAreSwallowed(t *testing.T) {
	fl := models.FailureLabels{
		CIFailing:       "ci-fail",
		Rejected:        "rejected",
		Blocked:         "blocked",
		ForkUserMissing: "fork-missing",
	}

	t.Run("AddLabel error does not propagate", func(t *testing.T) {
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, _ string) error { return fmt.Errorf("add failed") }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return nil }

		p := d.pipeline(t)
		// Should not panic or propagate the error.
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", fl, "blocked")
	})

	t.Run("RemoveLabel error does not propagate", func(t *testing.T) {
		d := newTestDeps(t)
		d.tracker.AddLabelFunc = func(_, _ string) error { return nil }
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return fmt.Errorf("remove failed") }

		p := d.pipeline(t)
		executor.SetFailureLabel(p, zap.NewNop(), "TEST-1", fl, "blocked")
	})

	t.Run("ClearFailureLabels swallows errors", func(t *testing.T) {
		d := newTestDeps(t)
		d.tracker.RemoveLabelFunc = func(_, _ string) error { return fmt.Errorf("remove failed") }

		p := d.pipeline(t)
		executor.ClearFailureLabels(p, zap.NewNop(), "TEST-1", fl)
	})
}
