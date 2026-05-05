package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	// IgnoredCheckNames lists check run names excluded from CI
	// failure detection (case-insensitive).
	IgnoredCheckNames []string

	// MaxCIFixAttempts limits CI fix attempts per PR. Zero
	// disables CI failure detection. Negative means unlimited.
	MaxCIFixAttempts int
}

// FeedbackScanner polls for tickets in "in review" status and checks
// GitHub for actionable PR comments. Applies bot-loop prevention via
// [commentfilter.HasNewActionable] before emitting events.
type FeedbackScanner struct {
	searcher  IssueSearcher
	submitter JobSubmitter
	prs       PRFetcher
	repos     RepoLocator
	ci        CIChecker
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
	ci CIChecker,
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
		ci:        ci,
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
// review comments or CI failures. Returns true as soon as any repo
// has actionable work.
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

		if s.hasActionableCIFailures(logger, r.Owner, r.Repo, pr, comments) {
			return true
		}
	}

	logger.Debug("No actionable feedback")
	return false
}

// hasActionableCIFailures checks whether a PR has CI failures that the
// bot should attempt to fix. Returns false if CI checking is disabled,
// checks are still running, all failures are ignored, or fix attempts
// are exhausted.
func (s *FeedbackScanner) hasActionableCIFailures(
	logger *zap.Logger,
	owner, repo string,
	pr *models.PRDetails,
	comments []models.PRComment,
) bool {
	if s.cfg.MaxCIFixAttempts == 0 || s.ci == nil {
		return false
	}
	if pr.HeadSHA == "" {
		return false
	}

	failures, allCompleted, err := s.ci.ListCheckRunsForRef(owner, repo, pr.HeadSHA)
	if err != nil {
		logger.Warn("Failed to check CI status",
			zap.String("repo", owner+"/"+repo),
			zap.Error(err))
		return false
	}
	if !allCompleted {
		logger.Debug("CI checks still running, skipping CI analysis",
			zap.String("repo", owner+"/"+repo))
		return false
	}

	filtered := filterIgnoredChecks(failures, s.cfg.IgnoredCheckNames)
	if len(filtered) == 0 {
		return false
	}

	if pr.BaseBranch != "" {
		baseFailures, _, baseErr := s.ci.ListCheckRunsForRef(owner, repo, pr.BaseBranch)
		if baseErr == nil {
			filtered = filterPreExistingFailures(filtered, baseFailures)
			if len(filtered) == 0 {
				return false
			}
		}
	}

	if s.cfg.MaxCIFixAttempts > 0 {
		attempts := commentfilter.CountCIFixAttempts(comments, s.cfg.BotUsername)
		if attempts >= s.cfg.MaxCIFixAttempts {
			logger.Debug("CI fix attempts exhausted",
				zap.String("repo", owner+"/"+repo),
				zap.Int("attempts", attempts),
				zap.Int("max", s.cfg.MaxCIFixAttempts))
			return false
		}
	}

	logger.Info("Found actionable CI failures",
		zap.String("repo", owner+"/"+repo),
		zap.Int("count", len(filtered)))
	return true
}

// filterPreExistingFailures removes failures whose check name also
// appears in base branch failures.
func filterPreExistingFailures(
	prFailures, baseFailures []models.CheckRunFailure,
) []models.CheckRunFailure {
	if len(baseFailures) == 0 {
		return prFailures
	}

	baseNames := make(map[string]bool, len(baseFailures))
	for _, f := range baseFailures {
		baseNames[f.Name] = true
	}

	var filtered []models.CheckRunFailure
	for _, f := range prFailures {
		if !baseNames[f.Name] {
			filtered = append(filtered, f)
		}
	}
	if filtered == nil {
		filtered = []models.CheckRunFailure{}
	}
	return filtered
}

// filterIgnoredChecks removes check runs whose names match the ignore
// list (case-insensitive).
func filterIgnoredChecks(
	failures []models.CheckRunFailure, ignored []string,
) []models.CheckRunFailure {
	if len(ignored) == 0 {
		return failures
	}

	ignoredSet := make(map[string]bool, len(ignored))
	for _, name := range ignored {
		ignoredSet[strings.ToLower(name)] = true
	}

	var filtered []models.CheckRunFailure
	for _, f := range failures {
		if !ignoredSet[strings.ToLower(f.Name)] {
			filtered = append(filtered, f)
		}
	}
	if filtered == nil {
		filtered = []models.CheckRunFailure{}
	}
	return filtered
}

func (s *FeedbackScanner) filterConfig() commentfilter.Config {
	return commentfilter.Config{
		BotUsername:       s.cfg.BotUsername,
		IgnoredUsernames:  s.cfg.IgnoredUsernames,
		KnownBotUsernames: s.cfg.KnownBotUsernames,
		MaxThreadDepth:    s.cfg.MaxThreadDepth,
	}
}
