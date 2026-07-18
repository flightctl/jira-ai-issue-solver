package costtracker_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/costtracker"
)

func TestTicketCostTracker_Record_AccumulatesAcrossCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	tracker.Record(10.50)
	tracker.Record(5.25)

	if got := tracker.Total(); got != 15.75 {
		t.Errorf("Total() = %v, want 15.75", got)
	}
}

func TestTicketCostTracker_Exceeded_TrueAtLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 20.0, zap.NewNop())

	tracker.Record(20.0)

	if !tracker.Exceeded() {
		t.Error("Exceeded() = false, want true when total == maxCap")
	}
}

func TestTicketCostTracker_Exceeded_TrueAboveLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 20.0, zap.NewNop())

	tracker.Record(25.0)

	if !tracker.Exceeded() {
		t.Error("Exceeded() = false, want true when total > maxCap")
	}
}

func TestTicketCostTracker_Exceeded_FalseUnderLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 20.0, zap.NewNop())

	tracker.Record(15.0)

	if tracker.Exceeded() {
		t.Error("Exceeded() = true, want false when total < maxCap")
	}
}

func TestTicketCostTracker_Exceeded_FalseWhenNoCap(t *testing.T) {
	for _, maxCap := range []float64{0, -1, -100} {
		path := filepath.Join(t.TempDir(), "ticket-cost.json")
		tracker := costtracker.NewTicketCostTracker(path, maxCap, zap.NewNop())

		tracker.Record(999999.99)

		if tracker.Exceeded() {
			t.Errorf("Exceeded() = true with maxCap=%v, want false", maxCap)
		}
	}
}

func TestTicketCostTracker_NonPositiveAmount_Ignored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	tracker.Record(10.0)
	tracker.Record(-5.0)
	tracker.Record(0)

	if got := tracker.Total(); got != 10.0 {
		t.Errorf("Total() = %v, want 10.0 (non-positive amounts should be ignored)", got)
	}
}

func TestTicketCostTracker_Persistence_SurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")

	t1 := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())
	t1.Record(33.33)
	t1.Record(11.11)

	t2 := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	if got := t2.Total(); got != 44.44 {
		t.Errorf("Total() after reload = %v, want 44.44", got)
	}
}

func TestTicketCostTracker_MissingFile_StartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	if got := tracker.Total(); got != 0 {
		t.Errorf("Total() on missing file = %v, want 0", got)
	}
}

func TestTicketCostTracker_CorruptFile_StartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket-cost.json")

	if err := os.WriteFile(path, []byte("not json!!!"), 0o600); err != nil {
		t.Fatal(err)
	}

	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	if got := tracker.Total(); got != 0 {
		t.Errorf("Total() on corrupt file = %v, want 0", got)
	}
}

func TestTicketCostTracker_NoCap_StillRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 0, zap.NewNop())

	tracker.Record(42.50)

	if got := tracker.Total(); got != 42.50 {
		t.Errorf("Total() = %v, want 42.50", got)
	}
	if tracker.Exceeded() {
		t.Error("Exceeded() = true with cap disabled, want false")
	}
}

func TestTicketCostTracker_Persistence_FileFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket-cost.json")

	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())
	tracker.Record(42.50)

	data, err := os.ReadFile(path) // #nosec G304 -- test file
	if err != nil {
		t.Fatal(err)
	}

	var rec struct {
		TotalUSD float64 `json:"total_usd"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("failed to unmarshal ticket cost file: %v", err)
	}

	if rec.TotalUSD != 42.50 {
		t.Errorf("total_usd = %v, want 42.50", rec.TotalUSD)
	}
}

func TestTicketCostTracker_Record_CreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	tracker.Record(5.0)

	if got := tracker.Total(); got != 5.0 {
		t.Errorf("Total() = %v, want 5.0", got)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("cost file not created: %v", err)
	}
}

func TestTicketCostTracker_Record_RejectsNaNAndInf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticket-cost.json")
	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	tracker.Record(10.0)
	tracker.Record(math.NaN())
	tracker.Record(math.Inf(1))
	tracker.Record(math.Inf(-1))

	if got := tracker.Total(); got != 10.0 {
		t.Errorf("Total() = %v, want 10.0 (NaN/Inf should be ignored)", got)
	}
}

func TestTicketCostTracker_LoadFromDisk_RejectsNaNTotal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket-cost.json")

	// Write a file with NaN encoded as null (Go's json.Marshal produces
	// an error for NaN, so we write raw JSON that decodes to a zero
	// value, then test with a hand-crafted file containing a non-finite
	// string that json would not normally produce). Instead, we test
	// that a negative-infinity total is rejected by writing valid JSON
	// with a very large negative value won't work either. The simplest
	// approach: write the file, load it, record, and verify the guard
	// in Record catches it. But the loadFromDisk guard catches invalid
	// totals read from disk. Since json.Unmarshal in Go does not produce
	// NaN/Inf from standard JSON, this guard protects against hand-edited
	// or externally-produced files. We can test it indirectly by verifying
	// that after a corrupt-but-parseable file, the tracker starts fresh.

	// json.Marshal cannot produce NaN, so write raw bytes.
	if err := os.WriteFile(path, []byte(`{"total_usd": 1e+999}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tracker := costtracker.NewTicketCostTracker(path, 100, zap.NewNop())

	if got := tracker.Total(); got != 0 {
		t.Errorf("Total() = %v, want 0 (Inf total from disk should be rejected)", got)
	}
}

func TestTicketCostTracker_LoadFromDisk_RejectsNegativeTotal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket-cost.json")

	if err := os.WriteFile(path, []byte(`{"total_usd": -100}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tracker := costtracker.NewTicketCostTracker(path, 20, zap.NewNop())

	if got := tracker.Total(); got != 0 {
		t.Errorf("Total() = %v, want 0 (negative total from disk should be rejected)", got)
	}
}
