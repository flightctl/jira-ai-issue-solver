package scanner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/commentfilter"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Compile-time check that FeedbackScanner implements Scanner.
var _ Scanner = (*FeedbackScanner)(nil)

// FeedbackScannerConfig holds configuration for [FeedbackScanner].
type FeedbackScannerConfig struct {
	// Criteria defines the search query for "in review" tickets.
	Criteria models.SearchCriteria

	// PollInterval is the time between scan cycles.
	PollInterval time.Duration

	// BotUsername is the bot's GitHub username, used for branch
	// name construction and comment filtering.
	BotUsername string

	// IgnoredUsernames lists users whose comments are skipped
	// entirely.
	IgnoredUsernames []string

	// KnownBotUsernames lists other bots for loop prevention.
	KnownBotUsernames []string

	// MaxThreadDepth limits bot replies per thread. Zero or
	// negative means no limit.
	MaxThreadDepth int
}

// FeedbackScanner polls for tickets in "in review" status and checks
// GitHub for actionable PR comments. Applies bot-loop prevention via
// [commentfilter.HasNewActionable] before emitting events.
type FeedbackScanner struct {
	searcher  IssueSearcher
	submitter JobSubmitter
	prs       PRFetcher
	repos     RepoLocator
	cfg       FeedbackScannerConfig
	logger    *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewFeedbackScanner creates a FeedbackScanner with the given
// dependencies. Returns an error if any required parameter is invalid.
func NewFeedbackScanner(
	searcher IssueSearcher,
	submitter JobSubmitter,
	prs PRFetcher,
	repos RepoLocator,
	cfg FeedbackScannerConfig,
	logger *zap.Logger,
) (*FeedbackScanner, error) {
	if searcher == nil {
		return nil, errors.New("issue searcher must not be nil")
	}
	if submitter == nil {
		return nil, errors.New("job submitter must not be nil")
	}
	if prs == nil {
		return nil, errors.New("PR fetcher must not be nil")
	}
	if repos == nil {
		return nil, errors.New("repo locator must not be nil")
	}
	if cfg.PollInterval <= 0 {
		return nil, errors.New("poll interval must be positive")
	}
	if cfg.BotUsername == "" {
		return nil, errors.New("bot username must not be empty")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	return &FeedbackScanner{
		searcher:  searcher,
		submitter: submitter,
		prs:       prs,
		repos:     repos,
		cfg:       cfg,
		logger:    logger,
	}, nil
}

// Start begins polling in a background goroutine.
func (s *FeedbackScanner) Start(ctx context.Context) error {
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
func (s *FeedbackScanner) Stop() {
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

func (s *FeedbackScanner) run(ctx context.Context) {
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

func (s *FeedbackScanner) scan(ctx context.Context) {
	items, err := s.searcher.SearchWorkItems(s.cfg.Criteria)
	if err != nil {
		s.logger.Error("Failed to search for in-review tickets", zap.Error(err))
		return
	}

	if len(items) == 0 {
		s.logger.Debug("No in-review tickets found")
		return
	}

	s.logger.Info("Found in-review tickets", zap.Int("count", len(items)))

	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if s.checkAndSubmit(item) {
			return
		}
	}
}

// checkAndSubmit checks a ticket for actionable PR comments across all
// repos in its workspace and submits a feedback event if found. Returns
// true if the scan cycle should stop (circuit breaker open or shutdown).
func (s *FeedbackScanner) checkAndSubmit(item models.WorkItem) bool {
	logger := s.logger.With(zap.String("ticket", item.Key))

	repos, err := s.repos.LocateRepos(item)
	if err != nil {
		logger.Warn("Failed to locate repos, skipping", zap.Error(err))
		return false
	}

	branchName := fmt.Sprintf("%s/%s", s.cfg.BotUsername, item.Key)
	head := branchName
	if forkOwner := s.repos.ForkOwner(item); forkOwner != "" {
		head = forkOwner + ":" + branchName
	}

	if !s.hasActionableComments(logger, repos, head) {
		return false
	}

	event := jobmanager.Event{
		Type:      jobmanager.JobTypeFeedback,
		TicketKey: item.Key,
	}

	_, err = s.submitter.Submit(event)
	if err == nil {
		logger.Info("Submitted feedback event")
		return false
	}

	switch {
	case errors.Is(err, jobmanager.ErrDuplicateJob):
		logger.Debug("Skipping duplicate feedback")
	case errors.Is(err, jobmanager.ErrRetriesExhausted):
		logger.Debug("Skipping exhausted ticket")
	case errors.Is(err, jobmanager.ErrCircuitOpen):
		logger.Warn("Circuit breaker open, stopping scan cycle")
		return true
	case errors.Is(err, jobmanager.ErrBudgetExceeded):
		logger.Warn("Daily budget exceeded, stopping scan cycle")
		return true
	case errors.Is(err, jobmanager.ErrShutdown):
		logger.Info("Job manager shut down, stopping scan cycle")
		return true
	default:
		logger.Error("Failed to submit feedback event", zap.Error(err))
	}

	return false
}

// hasActionableComments checks all repos for PRs with actionable
// review comments. Returns true as soon as any repo has actionable
// comments.
func (s *FeedbackScanner) hasActionableComments(
	logger *zap.Logger,
	repos []struct{ Owner, Repo string },
	head string,
) bool {
	for _, r := range repos {
		pr, err := s.prs.GetPRForBranch(r.Owner, r.Repo, head)
		if err != nil {
			logger.Debug("No PR found for repo, skipping",
				zap.String("repo", r.Owner+"/"+r.Repo))
			continue
		}

		comments, err := s.prs.GetPRComments(r.Owner, r.Repo, pr.Number, time.Time{})
		if err != nil {
			logger.Warn("Failed to fetch PR comments",
				zap.String("repo", r.Owner+"/"+r.Repo),
				zap.Error(err))
			continue
		}

		if commentfilter.HasNewActionable(comments, s.filterConfig()) {
			return true
		}
	}

	logger.Debug("No actionable comments")
	return false
}

func (s *FeedbackScanner) filterConfig() commentfilter.Config {
	return commentfilter.Config{
		BotUsername:       s.cfg.BotUsername,
		IgnoredUsernames:  s.cfg.IgnoredUsernames,
		KnownBotUsernames: s.cfg.KnownBotUsernames,
		MaxThreadDepth:    s.cfg.MaxThreadDepth,
	}
}
