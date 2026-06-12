package executor_test

import (
	"fmt"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/models"
)

func TestSetFailureLabel(t *testing.T) {
	fl := models.FailureLabels{
		CIFailing: "ci-fail",
		Rejected:  "rejected",
		Blocked:   "blocked",
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
		if len(removed) != 2 {
			t.Fatalf("removed = %v, want 2 entries", removed)
		}
		wantRemoved := map[string]bool{"ci-fail": true, "rejected": true}
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
		if len(removed) != 3 {
			t.Errorf("removed = %v, want 3 entries", removed)
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
			CIFailing: "ci-fail",
			Rejected:  "rejected",
			Blocked:   "blocked",
		}
		var removed []string
		d := newTestDeps(t)
		d.tracker.RemoveLabelFunc = func(_, label string) error { removed = append(removed, label); return nil }

		p := d.pipeline(t)
		executor.ClearFailureLabels(p, zap.NewNop(), "TEST-1", fl)

		if len(removed) != 3 {
			t.Fatalf("removed = %v, want 3 entries", removed)
		}
		want := map[string]bool{"ci-fail": true, "rejected": true, "blocked": true}
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

func TestSetFailureLabel_ErrorsAreSwallowed(t *testing.T) {
	fl := models.FailureLabels{
		CIFailing: "ci-fail",
		Rejected:  "rejected",
		Blocked:   "blocked",
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
