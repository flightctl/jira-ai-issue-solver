package costtracker_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/costtracker"
)

// fixedClock returns a clock function that always returns the given time.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestRecord_AccumulatesAcrossCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(10.50)
	ft.Record(5.25)
	ft.Record(4.25)

	got := ft.DailyTotal()
	if got != 20.0 {
		t.Errorf("DailyTotal() = %v, want 20.0", got)
	}
}

func TestDailyTotal_ReturnsCorrectTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 0, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	if got := ft.DailyTotal(); got != 0 {
		t.Errorf("DailyTotal() on fresh tracker = %v, want 0", got)
	}

	ft.Record(42.50)
	if got := ft.DailyTotal(); got != 42.50 {
		t.Errorf("DailyTotal() = %v, want 42.50", got)
	}
}

func TestBudgetExceeded_TrueAtLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 50.0, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(50.0)

	if !ft.BudgetExceeded() {
		t.Error("BudgetExceeded() = false, want true when total == maxBudget")
	}
}

func TestBudgetExceeded_TrueAboveLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 50.0, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(60.0)

	if !ft.BudgetExceeded() {
		t.Error("BudgetExceeded() = false, want true when total > maxBudget")
	}
}

func TestBudgetExceeded_FalseUnderLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 50.0, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(25.0)

	if ft.BudgetExceeded() {
		t.Error("BudgetExceeded() = true, want false when total < maxBudget")
	}
}

func TestBudgetExceeded_FalseWhenNoBudget(t *testing.T) {
	for _, maxBudget := range []float64{0, -1, -100} {
		path := filepath.Join(t.TempDir(), "cost.json")
		clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

		ft, err := costtracker.NewFileTrackerWithClock(path, maxBudget, clock, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}

		ft.Record(999999.99)

		if ft.BudgetExceeded() {
			t.Errorf("BudgetExceeded() = true with maxBudget=%v, want false", maxBudget)
		}
	}
}

func TestDateChange_ResetsTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")

	day1 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)

	currentTime := day1
	clock := func() time.Time { return currentTime }

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(75.0)
	if got := ft.DailyTotal(); got != 75.0 {
		t.Fatalf("DailyTotal() on day1 = %v, want 75.0", got)
	}

	// Advance to the next day.
	currentTime = day2

	if got := ft.DailyTotal(); got != 0 {
		t.Errorf("DailyTotal() on day2 = %v, want 0 (should reset)", got)
	}

	ft.Record(10.0)
	if got := ft.DailyTotal(); got != 10.0 {
		t.Errorf("DailyTotal() after record on day2 = %v, want 10.0", got)
	}
}

func TestNegativeAmount_Ignored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(10.0)
	ft.Record(-5.0)

	if got := ft.DailyTotal(); got != 10.0 {
		t.Errorf("DailyTotal() = %v, want 10.0 (negative should be ignored)", got)
	}
}

func TestPersistence_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft1, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft1.Record(33.33)
	ft1.Record(11.11)

	// Create a new tracker from the same file to simulate restart.
	ft2, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	want := 44.44
	if got := ft2.DailyTotal(); got != want {
		t.Errorf("DailyTotal() after restart = %v, want %v", got, want)
	}
}

func TestMissingFile_StartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	if got := ft.DailyTotal(); got != 0 {
		t.Errorf("DailyTotal() on missing file = %v, want 0", got)
	}
}

func TestCorruptFile_StartsFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")

	// Write invalid JSON.
	if err := os.WriteFile(path, []byte("not json!!!"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	if got := ft.DailyTotal(); got != 0 {
		t.Errorf("DailyTotal() on corrupt file = %v, want 0", got)
	}
}

func TestBudgetExceeded_ExactlyAtLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 25.0, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(12.50)
	ft.Record(12.50)

	if !ft.BudgetExceeded() {
		t.Error("BudgetExceeded() = false, want true when total exactly equals maxBudget")
	}
}

func TestPersistence_FileFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cost.json")
	clock := fixedClock(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	ft, err := costtracker.NewFileTrackerWithClock(path, 100, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(42.50)

	data, err := os.ReadFile(path) // #nosec G304 -- test file with controlled path
	if err != nil {
		t.Fatal(err)
	}

	var rec struct {
		Date     string  `json:"date"`
		TotalUSD float64 `json:"total_usd"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("failed to unmarshal cost file: %v", err)
	}

	if rec.Date != "2026-03-10" {
		t.Errorf("date = %q, want %q", rec.Date, "2026-03-10")
	}
	if rec.TotalUSD != 42.50 {
		t.Errorf("total_usd = %v, want 42.50", rec.TotalUSD)
	}
}

func TestDateChange_ResetsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")

	day1 := time.Date(2026, 3, 10, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 11, 0, 1, 0, 0, time.UTC)

	currentTime := day1
	clock := func() time.Time { return currentTime }

	ft, err := costtracker.NewFileTrackerWithClock(path, 50, clock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	ft.Record(45.0)

	// 45 < 50, so budget is not exceeded yet. We just want to
	// verify the total resets after the date change below.

	// Cross midnight.
	currentTime = day2

	if ft.BudgetExceeded() {
		t.Error("BudgetExceeded() = true after date change, want false (total should reset)")
	}

	if got := ft.DailyTotal(); got != 0 {
		t.Errorf("DailyTotal() after date change = %v, want 0", got)
	}
}
