package scanner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"jira-ai-issue-solver/models"
)

func TestNewWorkspaceCleanupScanner_Validation(t *testing.T) {
	validCfg := WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"Open": true},
	}
	logger := zaptest.NewLogger(t)
	ws := &stubWorkspaceCleaner{}
	tr := &stubTicketChecker{}

	tests := []struct {
		name       string
		workspaces WorkspaceCleaner
		tracker    TicketStatusChecker
		cfg        WorkspaceCleanupConfig
		logger     *zap.Logger
		wantErr    string
	}{
		{
			name:       "nil workspace cleaner",
			workspaces: nil,
			tracker:    tr,
			cfg:        validCfg,
			logger:     logger,
			wantErr:    "workspace cleaner must not be nil",
		},
		{
			name:       "nil ticket checker",
			workspaces: ws,
			tracker:    nil,
			cfg:        validCfg,
			logger:     logger,
			wantErr:    "ticket status checker must not be nil",
		},
		{
			name:       "zero poll interval",
			workspaces: ws,
			tracker:    tr,
			cfg: WorkspaceCleanupConfig{
				PollInterval:   0,
				ActiveStatuses: map[string]bool{"Open": true},
			},
			logger:  logger,
			wantErr: "poll interval must be positive",
		},
		{
			name:       "empty active statuses",
			workspaces: ws,
			tracker:    tr,
			cfg: WorkspaceCleanupConfig{
				PollInterval:   time.Minute,
				ActiveStatuses: map[string]bool{},
			},
			logger:  logger,
			wantErr: "active statuses must not be empty",
		},
		{
			name:       "nil logger",
			workspaces: ws,
			tracker:    tr,
			cfg:        validCfg,
			logger:     nil,
			wantErr:    "logger must not be nil",
		},
		{
			name:       "valid",
			workspaces: ws,
			tracker:    tr,
			cfg:        validCfg,
			logger:     logger,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewWorkspaceCleanupScanner(tt.workspaces, tt.tracker, tt.cfg, tt.logger)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s == nil {
				t.Fatal("expected non-nil scanner")
			}
		})
	}
}

func TestWorkspaceCleanupScanner_RemovesTerminalWorkspaces(t *testing.T) {
	statuses := map[string]string{
		"PROJ-1": "Done",
		"PROJ-2": "In Progress",
		"PROJ-3": "Closed",
	}

	tracker := &stubTicketChecker{
		items: statuses,
	}

	var removedKeys []string
	ws := &stubWorkspaceCleaner{
		tickets: []string{"PROJ-1", "PROJ-2", "PROJ-3"},
		onRemove: func(key string) {
			removedKeys = append(removedKeys, key)
		},
	}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"In Progress": true, "Open": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	s.scan(context.Background())

	if len(removedKeys) != 2 {
		t.Fatalf("expected 2 removals, got %d: %v", len(removedKeys), removedKeys)
	}
	// PROJ-1 (Done) and PROJ-3 (Closed) should be removed.
	for _, key := range removedKeys {
		if key == "PROJ-2" {
			t.Errorf("PROJ-2 (In Progress) should not have been removed")
		}
	}
}

func TestWorkspaceCleanupScanner_KeepsActiveWorkspaces(t *testing.T) {
	tracker := &stubTicketChecker{
		items: map[string]string{
			"PROJ-1": "Open",
			"PROJ-2": "In Progress",
			"PROJ-3": "Code Review",
		},
	}

	var removeCount int
	ws := &stubWorkspaceCleaner{
		tickets: []string{"PROJ-1", "PROJ-2", "PROJ-3"},
		onRemove: func(_ string) {
			removeCount++
		},
	}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"Open": true, "In Progress": true, "Code Review": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	s.scan(context.Background())

	if removeCount != 0 {
		t.Errorf("expected 0 removals, got %d", removeCount)
	}
}

func TestWorkspaceCleanupScanner_RemovesDeletedTickets(t *testing.T) {
	tracker := &stubTicketChecker{
		items: map[string]string{
			"PROJ-1": "In Progress",
		},
		errKeys: map[string]bool{"PROJ-2": true},
	}

	var removedKeys []string
	ws := &stubWorkspaceCleaner{
		tickets: []string{"PROJ-1", "PROJ-2"},
		onRemove: func(key string) {
			removedKeys = append(removedKeys, key)
		},
	}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"In Progress": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	s.scan(context.Background())

	if len(removedKeys) != 1 || removedKeys[0] != "PROJ-2" {
		t.Errorf("expected [PROJ-2] removed, got %v", removedKeys)
	}
}

func TestWorkspaceCleanupScanner_CleanupByFilterError(t *testing.T) {
	ws := &stubWorkspaceCleaner{
		err: fmt.Errorf("disk error"),
	}
	tracker := &stubTicketChecker{}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"Open": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic; error is logged.
	s.scan(context.Background())
}

func TestWorkspaceCleanupScanner_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var checkedKeys []string
	tracker := &stubTicketChecker{
		items: map[string]string{"PROJ-1": "Done"},
		onGet: func(key string) {
			checkedKeys = append(checkedKeys, key)
		},
	}

	ws := &stubWorkspaceCleaner{
		tickets: []string{"PROJ-1"},
	}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   time.Minute,
		ActiveStatuses: map[string]bool{"Open": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	s.scan(ctx)

	if len(checkedKeys) != 0 {
		t.Errorf("expected no tickets checked after cancellation, got %v", checkedKeys)
	}
}

func TestWorkspaceCleanupScanner_StartStop(t *testing.T) {
	ws := &stubWorkspaceCleaner{}
	tracker := &stubTicketChecker{}

	s, err := NewWorkspaceCleanupScanner(ws, tracker, WorkspaceCleanupConfig{
		PollInterval:   50 * time.Millisecond,
		ActiveStatuses: map[string]bool{"Open": true},
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Starting again should fail.
	if err := s.Start(context.Background()); err == nil {
		t.Error("expected error on double start")
	}

	s.Stop()

	// Stop is idempotent.
	s.Stop()
}

// --- test helpers ---

type stubWorkspaceCleaner struct {
	tickets  []string
	err      error
	onRemove func(string)
}

func (s *stubWorkspaceCleaner) CleanupByFilter(shouldRemove func(string) bool) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	removed := 0
	for _, key := range s.tickets {
		if shouldRemove(key) {
			removed++
			if s.onRemove != nil {
				s.onRemove(key)
			}
		}
	}
	return removed, nil
}

type stubTicketChecker struct {
	items   map[string]string
	errKeys map[string]bool
	onGet   func(string)
}

func (s *stubTicketChecker) GetWorkItem(key string) (*models.WorkItem, error) {
	if s.onGet != nil {
		s.onGet(key)
	}
	if s.errKeys[key] {
		return nil, fmt.Errorf("ticket %s not found", key)
	}
	status, ok := s.items[key]
	if !ok {
		return &models.WorkItem{Key: key}, nil
	}
	return &models.WorkItem{Key: key, Status: status}, nil
}
