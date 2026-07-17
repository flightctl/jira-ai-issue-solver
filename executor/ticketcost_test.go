package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/workspace/workspacetest"
)

func TestCheckTicketCostCap_ExceededWhenOverLimit(t *testing.T) {
	wsPath := t.TempDir()
	costDir := filepath.Join(wsPath, ".ai-session")
	if err := os.MkdirAll(costDir, 0o750); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]float64{"total_usd": 25.0})
	if err := os.WriteFile(filepath.Join(costDir, "ticket-cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{}
	if !p.checkTicketCostCap(zap.NewNop(), wsPath, 20.0) {
		t.Error("checkTicketCostCap() = false, want true when cost exceeds cap")
	}
}

func TestCheckTicketCostCap_NotExceededWhenUnderLimit(t *testing.T) {
	wsPath := t.TempDir()
	costDir := filepath.Join(wsPath, ".ai-session")
	if err := os.MkdirAll(costDir, 0o750); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]float64{"total_usd": 15.0})
	if err := os.WriteFile(filepath.Join(costDir, "ticket-cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{}
	if p.checkTicketCostCap(zap.NewNop(), wsPath, 20.0) {
		t.Error("checkTicketCostCap() = true, want false when cost is under cap")
	}
}

func TestCheckTicketCostCap_FalseWhenNoCostFile(t *testing.T) {
	wsPath := t.TempDir()

	p := &Pipeline{}
	if p.checkTicketCostCap(zap.NewNop(), wsPath, 20.0) {
		t.Error("checkTicketCostCap() = true, want false when no cost file exists")
	}
}

func TestCheckTicketCostCap_FalseWhenCapDisabled(t *testing.T) {
	wsPath := t.TempDir()
	costDir := filepath.Join(wsPath, ".ai-session")
	if err := os.MkdirAll(costDir, 0o750); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]float64{"total_usd": 100.0})
	if err := os.WriteFile(filepath.Join(costDir, "ticket-cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{}
	for _, cap := range []float64{0, -1} {
		if p.checkTicketCostCap(zap.NewNop(), wsPath, cap) {
			t.Errorf("checkTicketCostCap() = true with cap=%v, want false (disabled)", cap)
		}
	}
}

func TestRecordTicketCost_WritesCostFile(t *testing.T) {
	wsPath := t.TempDir()

	p := &Pipeline{}
	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 5.50)

	path := filepath.Join(wsPath, ticketCostPath)
	data, err := os.ReadFile(path) // #nosec G304 -- test file
	if err != nil {
		t.Fatalf("cost file not created: %v", err)
	}

	var rec struct {
		TotalUSD float64 `json:"total_usd"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if rec.TotalUSD != 5.50 {
		t.Errorf("total_usd = %v, want 5.50", rec.TotalUSD)
	}
}

func TestRecordTicketCost_AccumulatesAcrossCalls(t *testing.T) {
	wsPath := t.TempDir()

	p := &Pipeline{}
	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 5.50)
	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 3.25)

	path := filepath.Join(wsPath, ticketCostPath)
	data, err := os.ReadFile(path) // #nosec G304 -- test file
	if err != nil {
		t.Fatal(err)
	}

	var rec struct {
		TotalUSD float64 `json:"total_usd"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.TotalUSD != 8.75 {
		t.Errorf("total_usd = %v, want 8.75", rec.TotalUSD)
	}
}

func TestRecordTicketCost_SkipsNonPositiveCost(t *testing.T) {
	wsPath := t.TempDir()

	p := &Pipeline{}
	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 0)
	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, -1.0)

	path := filepath.Join(wsPath, ticketCostPath)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cost file should not be created for zero/negative cost")
	}
}

func TestRecordThenCheck_Roundtrip(t *testing.T) {
	wsPath := t.TempDir()
	p := &Pipeline{}

	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 10.0)
	if p.checkTicketCostCap(zap.NewNop(), wsPath, 20.0) {
		t.Error("should not exceed cap after $10 on $20 cap")
	}

	p.recordTicketCost(zap.NewNop(), wsPath, 20.0, 10.0)
	if !p.checkTicketCostCap(zap.NewNop(), wsPath, 20.0) {
		t.Error("should exceed cap after $20 on $20 cap")
	}
}

func TestTicketCostCapExceeded_WorkspaceExists_CapExceeded(t *testing.T) {
	wsPath := t.TempDir()
	costDir := filepath.Join(wsPath, ".ai-session")
	if err := os.MkdirAll(costDir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]float64{"total_usd": 25.0})
	if err := os.WriteFile(filepath.Join(costDir, "ticket-cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{
		workspaces: &workspacetest.Stub{
			FindFunc: func(ticketKey string) (string, bool) {
				return wsPath, true
			},
		},
	}

	if !p.ticketCostCapExceeded(zap.NewNop(), "PROJ-1", 20.0) {
		t.Error("should return true when workspace exists and cap exceeded")
	}
}

func TestTicketCostCapExceeded_WorkspaceNotFound(t *testing.T) {
	p := &Pipeline{
		workspaces: &workspacetest.Stub{
			FindFunc: func(ticketKey string) (string, bool) {
				return "", false
			},
		},
	}

	if p.ticketCostCapExceeded(zap.NewNop(), "PROJ-1", 20.0) {
		t.Error("should return false when no workspace exists")
	}
}

func TestTicketCostCapExceeded_WorkspaceExists_UnderCap(t *testing.T) {
	wsPath := t.TempDir()
	costDir := filepath.Join(wsPath, ".ai-session")
	if err := os.MkdirAll(costDir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]float64{"total_usd": 10.0})
	if err := os.WriteFile(filepath.Join(costDir, "ticket-cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{
		workspaces: &workspacetest.Stub{
			FindFunc: func(ticketKey string) (string, bool) {
				return wsPath, true
			},
		},
	}

	if p.ticketCostCapExceeded(zap.NewNop(), "PROJ-1", 20.0) {
		t.Error("should return false when under cap")
	}
}
