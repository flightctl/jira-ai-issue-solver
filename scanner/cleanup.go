package scanner

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Compile-time check that WorkspaceCleanupScanner implements Scanner.
var _ Scanner = (*WorkspaceCleanupScanner)(nil)

// WorkspaceCleanupConfig holds configuration for [WorkspaceCleanupScanner].
type WorkspaceCleanupConfig struct {
	// PollInterval is the time between cleanup cycles.
	PollInterval time.Duration

	// ActiveStatuses is the set of statuses that indicate a ticket may
	// still need its workspace (todo, in_progress, in_review across all
	// projects and ticket types). Workspaces for tickets whose status is
	// not in this set are removed.
	ActiveStatuses map[string]bool
}

// WorkspaceCleanupScanner periodically removes workspaces for tickets
// that are no longer in an active status. On each cycle it iterates
// existing workspace directories, checks each ticket's current status
// via the issue tracker, and removes workspaces whose ticket has moved
// to a terminal state (or been deleted).
type WorkspaceCleanupScanner struct {
	workspaces WorkspaceCleaner
	tracker    TicketStatusChecker
	cfg        WorkspaceCleanupConfig
	logger     *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewWorkspaceCleanupScanner creates a WorkspaceCleanupScanner with the
// given dependencies. Returns an error if any required parameter is
// invalid.
func NewWorkspaceCleanupScanner(
	workspaces WorkspaceCleaner,
	tracker TicketStatusChecker,
	cfg WorkspaceCleanupConfig,
	logger *zap.Logger,
) (*WorkspaceCleanupScanner, error) {
	if workspaces == nil {
		return nil, errors.New("workspace cleaner must not be nil")
	}
	if tracker == nil {
		return nil, errors.New("ticket status checker must not be nil")
	}
	if cfg.PollInterval <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	if len(cfg.ActiveStatuses) == 0 {
		return nil, errors.New("active statuses must not be empty")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	return &WorkspaceCleanupScanner{
		workspaces: workspaces,
		tracker:    tracker,
		cfg:        cfg,
		logger:     logger,
	}, nil
}

// Start begins polling in a background goroutine.
func (s *WorkspaceCleanupScanner) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return errors.New("scanner already running")
	}

	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx)
	return nil
}

// Stop cancels polling and blocks until the goroutine exits.
func (s *WorkspaceCleanupScanner) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *WorkspaceCleanupScanner) run(ctx context.Context) {
	defer close(s.done)

	s.scan(ctx)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *WorkspaceCleanupScanner) scan(ctx context.Context) {
	cleaned, err := s.workspaces.CleanupByFilter(func(ticketKey string) bool {
		if ctx.Err() != nil {
			return false
		}
		item, err := s.tracker.GetWorkItem(ticketKey)
		if err != nil {
			s.logger.Debug("Workspace ticket not found, marking for removal",
				zap.String("ticket", ticketKey),
				zap.Error(err))
			return true
		}
		return !s.cfg.ActiveStatuses[item.Status]
	})
	if err != nil {
		s.logger.Warn("Failed to clean workspaces", zap.Error(err))
		return
	}
	if cleaned > 0 {
		s.logger.Info("Cleaned terminal workspaces", zap.Int("count", cleaned))
	}
}
