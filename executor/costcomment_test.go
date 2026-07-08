package executor

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
)

func TestFormatCostComment(t *testing.T) {
	entries := []costEntry{
		{Label: "New ticket", Cost: 4.32},
		{Label: "Feedback (1)", Cost: 1.15},
	}

	got := formatCostComment(entries)

	if !strings.Contains(got, costCommentMarker) {
		t.Error("should contain cost comment marker")
	}
	if !strings.Contains(got, "New ticket") {
		t.Error("should contain first entry label")
	}
	if !strings.Contains(got, "$4.32") {
		t.Error("should contain first entry cost")
	}
	if !strings.Contains(got, "Feedback (1)") {
		t.Error("should contain second entry label")
	}
	if !strings.Contains(got, "$1.15") {
		t.Error("should contain second entry cost")
	}
	if !strings.Contains(got, "**$5.47**") {
		t.Error("should contain correct total")
	}
}

func TestFormatCostComment_SingleEntry(t *testing.T) {
	entries := []costEntry{
		{Label: "New ticket", Cost: 0.50},
	}

	got := formatCostComment(entries)

	if !strings.Contains(got, "**$0.50**") {
		t.Error("total should equal single entry cost")
	}
}

func TestParseCostComment(t *testing.T) {
	body := costCommentMarker + `
**AI Session Costs**

| Session | Cost |
|---------|------|
| New ticket | $4.32 |
| Feedback (1) | $1.15 |
| **Total** | **$5.47** |
`

	entries := parseCostComment(body)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Label != "New ticket" || entries[0].Cost != 4.32 {
		t.Errorf("first entry = %+v, want {New ticket, 4.32}", entries[0])
	}
	if entries[1].Label != "Feedback (1)" || entries[1].Cost != 1.15 {
		t.Errorf("second entry = %+v, want {Feedback (1), 1.15}", entries[1])
	}
}

func TestParseCostComment_NoMarker(t *testing.T) {
	entries := parseCostComment("just a regular comment")
	if entries != nil {
		t.Error("should return nil for non-cost comment")
	}
}

func TestParseCostComment_EmptyTable(t *testing.T) {
	body := costCommentMarker + "\n**AI Session Costs**\n\n| Session | Cost |\n|---------|------|\n| **Total** | **$0.00** |\n"

	entries := parseCostComment(body)

	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestFormatThenParse_Roundtrip(t *testing.T) {
	original := []costEntry{
		{Label: "New ticket", Cost: 4.32},
		{Label: "Feedback (1)", Cost: 1.15},
		{Label: "Feedback (2)", Cost: 0.89},
	}

	body := formatCostComment(original)
	parsed := parseCostComment(body)

	if len(parsed) != len(original) {
		t.Fatalf("roundtrip: got %d entries, want %d", len(parsed), len(original))
	}
	for i, e := range parsed {
		if e.Label != original[i].Label {
			t.Errorf("entry %d label = %q, want %q", i, e.Label, original[i].Label)
		}
		if e.Cost != original[i].Cost {
			t.Errorf("entry %d cost = %v, want %v", i, e.Cost, original[i].Cost)
		}
	}
}

func TestFormatThenParse_Roundtrip_WithRetriesAndSuffixes(t *testing.T) {
	original := []costEntry{
		{Label: "New ticket", Cost: 3.99},
		{Label: "Feedback (1)", Cost: 0.56},
		{Label: "Feedback (2) (no changes)", Cost: 0.48},
		{Label: "Feedback (2) retry 1 (no changes)", Cost: 4.14},
		{Label: "Feedback (2) retry 2 (unable)", Cost: 1.83},
		{Label: "Feedback (3) (no changes)", Cost: 2.34},
	}

	body := formatCostComment(original)
	parsed := parseCostComment(body)

	if len(parsed) != len(original) {
		t.Fatalf("roundtrip: got %d entries, want %d", len(parsed), len(original))
	}
	for i, e := range parsed {
		if e.Label != original[i].Label {
			t.Errorf("entry %d label = %q, want %q", i, e.Label, original[i].Label)
		}
		if e.Cost != original[i].Cost {
			t.Errorf("entry %d cost = %v, want %v", i, e.Cost, original[i].Cost)
		}
	}
}

func TestFindCostComment(t *testing.T) {
	comments := []models.IssueComment{
		{ID: 1, Body: "Some other comment"},
		{ID: 2, Body: costCommentMarker + "\n**AI Session Costs**"},
		{ID: 3, Body: "Another comment"},
	}

	found := findCostComment(comments)

	if found == nil {
		t.Fatal("should find cost comment")
	}
	if found.ID != 2 {
		t.Errorf("found ID = %d, want 2", found.ID)
	}
}

func TestFindCostComment_NotPresent(t *testing.T) {
	comments := []models.IssueComment{
		{ID: 1, Body: "Some other comment"},
	}

	if findCostComment(comments) != nil {
		t.Error("should return nil when no cost comment exists")
	}
}

func TestFindCostComment_EmptyList(t *testing.T) {
	if findCostComment([]models.IssueComment{}) != nil {
		t.Error("should return nil for empty list")
	}
}

func TestCountFeedbackRounds(t *testing.T) {
	tests := []struct {
		name    string
		entries []costEntry
		want    int
	}{
		{
			name:    "no feedback entries",
			entries: []costEntry{{Label: "New ticket", Cost: 1}},
			want:    0,
		},
		{
			name: "one round",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
			},
			want: 1,
		},
		{
			name: "retries do not count as rounds",
			entries: []costEntry{
				{Label: "Feedback (1) (no changes)", Cost: 1},
				{Label: "Feedback (1) retry 1 (no changes)", Cost: 1},
				{Label: "Feedback (1) retry 2 (unable)", Cost: 1},
			},
			want: 1,
		},
		{
			name: "multiple rounds with retries",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
				{Label: "Feedback (2) (no changes)", Cost: 1},
				{Label: "Feedback (2) retry 1 (no changes)", Cost: 1},
				{Label: "Feedback (3)", Cost: 1},
			},
			want: 3,
		},
		{
			name:    "nil entries",
			entries: nil,
			want:    0,
		},
		{
			name: "error entries do not count as rounds",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
				{Label: "Feedback (error)", Cost: 1},
				{Label: "Feedback (2)", Cost: 1},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countFeedbackRounds(tt.entries)
			if got != tt.want {
				t.Errorf("countFeedbackRounds() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFeedbackLabel(t *testing.T) {
	tests := []struct {
		name       string
		entries    []costEntry
		attemptNum int
		suffix     string
		want       string
	}{
		{
			name:       "first attempt, no existing entries",
			entries:    nil,
			attemptNum: 1,
			want:       "Feedback (1)",
		},
		{
			name: "first attempt, one existing round",
			entries: []costEntry{
				{Label: "New ticket", Cost: 1},
				{Label: "Feedback (1)", Cost: 1},
			},
			attemptNum: 1,
			want:       "Feedback (2)",
		},
		{
			name: "first attempt with suffix",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
			},
			attemptNum: 1,
			suffix:     " (no changes)",
			want:       "Feedback (2) (no changes)",
		},
		{
			name: "retry of current round",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
				{Label: "Feedback (2) (no changes)", Cost: 1},
			},
			attemptNum: 2,
			suffix:     " (no changes)",
			want:       "Feedback (2) retry 1 (no changes)",
		},
		{
			name: "third retry",
			entries: []costEntry{
				{Label: "Feedback (1)", Cost: 1},
				{Label: "Feedback (2) (no changes)", Cost: 1},
				{Label: "Feedback (2) retry 1 (no changes)", Cost: 1},
				{Label: "Feedback (2) retry 2 (no changes)", Cost: 1},
			},
			attemptNum: 4,
			suffix:     " (unable)",
			want:       "Feedback (2) retry 3 (unable)",
		},
		{
			name:       "retry with no existing entries defaults to round 1",
			entries:    nil,
			attemptNum: 2,
			suffix:     " (no changes)",
			want:       "Feedback (1) retry 1 (no changes)",
		},
		{
			name: "retry with no suffix",
			entries: []costEntry{
				{Label: "Feedback (1) (no changes)", Cost: 1},
			},
			attemptNum: 2,
			want:       "Feedback (1) retry 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feedbackLabel(tt.entries, tt.attemptNum, tt.suffix)
			if got != tt.want {
				t.Errorf("feedbackLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFeedbackCostLabel(t *testing.T) {
	tests := []struct {
		name         string
		commitErr    error
		commitCount  int
		finalAttempt bool
		want         string
	}{
		{
			name:      "no-changes error",
			commitErr: fmt.Errorf("AI produced no changes (exit code: 0)"),
			want:      "Feedback (no changes)",
		},
		{
			name:      "no-committable-changes error",
			commitErr: fmt.Errorf("AI produced no committable changes (exit code: 0)"),
			want:      "Feedback (no changes)",
		},
		{
			name:      "infrastructure error",
			commitErr: errors.New("commit changes for svc-a: API rate limit"),
			want:      "Feedback (error)",
		},
		{
			name:         "final attempt with no commits",
			commitCount:  0,
			finalAttempt: true,
			want:         "Feedback (unable)",
		},
		{
			name:        "no changes, not final",
			commitCount: 0,
			want:        "Feedback (no changes)",
		},
		{
			name:        "success with commits",
			commitCount: 2,
			want:        "Feedback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feedbackCostLabel(tt.commitErr, tt.commitCount, tt.finalAttempt)
			if got != tt.want {
				t.Errorf("feedbackCostLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
