package scanner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Compile-time check that WorkItemScanner implements Scanner.
var _ Scanner = (*WorkItemScanner)(nil)

// WorkItemScannerConfig holds configuration for [WorkItemScanner].
type WorkItemScannerConfig struct {
	// Criteria defines the search query for discovering new tickets.
	Criteria models.SearchCriteria

	// PollInterval is the time between scan cycles.
	PollInterval time.Duration
}

// WorkItemScanner polls the issue tracker for tickets matching the
// configured criteria and emits [jobmanager.JobTypeNewTicket] events.
type WorkItemScanner struct {
	searcher      IssueSearcher
	submitter     JobSubmitter
	retryResetter RetryResetter
	labelRemover  LabelRemover
	retryLabel    string
	cfg           WorkItemScannerConfig
	logger        *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewWorkItemScanner creates a WorkItemScanner with the given
// dependencies. Returns an error if any required parameter is invalid.
//
// The retryResetter, labelRemover, and retryLabel parameters enable
// the retry label feature. When all are set, the scanner resets
// retry counts and resubmits tickets that carry the configured label.
// Pass nil, nil, "" to disable the feature.
func NewWorkItemScanner(
	searcher IssueSearcher,
	submitter JobSubmitter,
	retryResetter RetryResetter,
	labelRemover LabelRemover,
	retryLabel string,
	cfg WorkItemScannerConfig,
	logger *zap.Logger,
) (*WorkItemScanner, error) {
	if searcher == nil {
		return nil, errors.New("issue searcher must not be nil")
	}
	if submitter == nil {
		return nil, errors.New("job submitter must not be nil")
	}
	if cfg.PollInterval <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	return &WorkItemScanner{
		searcher:      searcher,
		submitter:     submitter,
		retryResetter: retryResetter,
		labelRemover:  labelRemover,
		retryLabel:    retryLabel,
		cfg:           cfg,
		logger:        logger,
	}, nil
}

// Start begins polling in a background goroutine.
func (s *WorkItemScanner) Start(ctx context.Context) error {
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
func (s *WorkItemScanner) Stop() {
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

func (s *WorkItemScanner) run(ctx context.Context) {
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

func (s *WorkItemScanner) scan(ctx context.Context) {
	items, err := s.searcher.SearchWorkItems(s.cfg.Criteria)
	if err != nil {
		s.logger.Error("Failed to search for work items", zap.Error(err))
		return
	}

	if len(items) == 0 {
		s.logger.Debug("No work items found")
		return
	}

	s.logger.Info("Found work items", zap.Int("count", len(items)))

	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if s.submitEvent(item) {
			return
		}
	}
}

// submitEvent emits a new ticket event. Returns true if the scan
// cycle should stop (circuit breaker open or shutdown).
func (s *WorkItemScanner) submitEvent(item models.WorkItem) bool {
	event := jobmanager.Event{
		Type:      jobmanager.JobTypeNewTicket,
		TicketKey: item.Key,
	}

	_, err := s.submitter.Submit(event)
	if err == nil {
		s.logger.Info("Submitted new ticket event",
			zap.String("ticket", item.Key))
		return false
	}

	switch {
	case errors.Is(err, jobmanager.ErrDuplicateJob):
		s.logger.Debug("Skipping duplicate",
			zap.String("ticket", item.Key))
	case errors.Is(err, jobmanager.ErrRetriesExhausted):
		if s.handleRetryLabel(item) {
			return false
		}
		s.logger.Debug("Skipping exhausted ticket",
			zap.String("ticket", item.Key))
	case errors.Is(err, jobmanager.ErrCircuitOpen):
		s.logger.Warn("Circuit breaker open, stopping scan cycle")
		return true
	case errors.Is(err, jobmanager.ErrBudgetExceeded):
		s.logger.Warn("Daily budget exceeded, stopping scan cycle")
		return true
	case errors.Is(err, jobmanager.ErrShutdown):
		s.logger.Info("Job manager shut down, stopping scan cycle")
		return true
	default:
		s.logger.Error("Failed to submit event",
			zap.String("ticket", item.Key),
			zap.Error(err))
	}

	return false
}

// handleRetryLabel checks whether an exhausted ticket has the retry
// label. If so, it resets the retry count, removes the label, and
// resubmits the ticket. Returns true if the ticket was resubmitted.
func (s *WorkItemScanner) handleRetryLabel(item models.WorkItem) bool {
	if s.retryLabel == "" || s.labelRemover == nil || s.retryResetter == nil {
		return false
	}

	hasLabel := slices.ContainsFunc(item.Labels, func(l string) bool {
		return strings.EqualFold(l, s.retryLabel)
	})
	if !hasLabel {
		return false
	}

	s.logger.Info("Retry label detected, resetting retry count",
		zap.String("ticket", item.Key),
		zap.String("label", s.retryLabel))

	if err := s.retryResetter.ResetRetries(item.Key); err != nil {
		s.logger.Error("Failed to reset retries",
			zap.String("ticket", item.Key),
			zap.Error(err))
		return false
	}

	if err := s.labelRemover.RemoveLabel(item.Key, s.retryLabel); err != nil {
		s.logger.Error("Failed to remove retry label, skipping resubmit to avoid retry loop",
			zap.String("ticket", item.Key),
			zap.Error(err))
		return false
	}

	event := jobmanager.Event{
		Type:      jobmanager.JobTypeNewTicket,
		TicketKey: item.Key,
	}
	if _, err := s.submitter.Submit(event); err != nil {
		s.logger.Error("Failed to resubmit after retry reset",
			zap.String("ticket", item.Key),
			zap.Error(err))
		return false
	}

	s.logger.Info("Resubmitted ticket after retry reset",
		zap.String("ticket", item.Key))
	return true
}
