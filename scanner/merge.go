package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Compile-time check that MergeScanner implements Scanner.
var _ Scanner = (*MergeScanner)(nil)

// MergeScannerConfig holds configuration for [MergeScanner].
type MergeScannerConfig struct {
	// Criteria defines the search query for "in review" tickets.
	Criteria models.SearchCriteria

	// PollInterval is the time between scan cycles.
	PollInterval time.Duration

	// BotUsername is the bot's GitHub username, used for branch
	// name construction and filtering bot comments from activity
	// detection.
	BotUsername string

	// IdleDays is the number of days without human PR comment
	// activity before a PR is considered idle. Idle unmergeable
	// PRs are labeled instead of merged. Zero disables idle
	// detection.
	IdleDays int

	// IdleLabel is the GitHub label applied to idle unmergeable
	// PRs. Removing the label re-activates auto-merging.
	IdleLabel string

	// IgnoredUsernames lists users whose comments are not
	// considered human activity for idle detection.
	IgnoredUsernames []string

	// KnownBotUsernames lists other bots whose comments are not
	// considered human activity for idle detection.
	KnownBotUsernames []string
}

// MergeScanner polls for tickets in "in review" status and checks
// whether their PRs are mergeable with the target branch. When a PR
// has merge conflicts, the scanner emits [jobmanager.JobTypeMerge]
// events. PRs without recent human activity are labeled as idle
// instead of merged.
type MergeScanner struct {
	searcher   IssueSearcher
	submitter  JobSubmitter
	prs        PRFetcher
	repos      RepoLocator
	mergeCheck MergeabilityChecker
	labeler    PRLabeler
	cfg        MergeScannerConfig
	logger     *zap.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewMergeScanner creates a MergeScanner with the given dependencies.
// Returns an error if any required parameter is invalid.
func NewMergeScanner(
	searcher IssueSearcher,
	submitter JobSubmitter,
	prs PRFetcher,
	repos RepoLocator,
	mergeCheck MergeabilityChecker,
	labeler PRLabeler,
	cfg MergeScannerConfig,
	logger *zap.Logger,
) (*MergeScanner, error) {
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
	if mergeCheck == nil {
		return nil, errors.New("mergeability checker must not be nil")
	}
	if labeler == nil {
		return nil, errors.New("PR labeler must not be nil")
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

	return &MergeScanner{
		searcher:   searcher,
		submitter:  submitter,
		prs:        prs,
		repos:      repos,
		mergeCheck: mergeCheck,
		labeler:    labeler,
		cfg:        cfg,
		logger:     logger,
	}, nil
}

// Start begins polling in a background goroutine.
func (s *MergeScanner) Start(ctx context.Context) error {
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
func (s *MergeScanner) Stop() {
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

func (s *MergeScanner) run(ctx context.Context) {
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

func (s *MergeScanner) scan(ctx context.Context) {
	items, err := s.searcher.SearchWorkItems(s.cfg.Criteria)
	if err != nil {
		s.logger.Error("Failed to search for in-review tickets", zap.Error(err))
		return
	}

	if len(items) == 0 {
		s.logger.Debug("No in-review tickets found")
		return
	}

	s.logger.Debug("Checking mergeability", zap.Int("tickets", len(items)))

	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if s.checkAndSubmit(item) {
			return
		}
	}
}

// checkAndSubmit checks a ticket's PRs for merge conflicts and
// submits a merge event if any PR is unmergeable. Returns true if
// the scan cycle should stop (circuit breaker open or shutdown).
func (s *MergeScanner) checkAndSubmit(item models.WorkItem) bool {
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

	if !s.hasUnmergeablePR(logger, repos, head) {
		return false
	}

	event := jobmanager.Event{
		Type:      jobmanager.JobTypeMerge,
		TicketKey: item.Key,
	}

	_, err = s.submitter.Submit(event)
	if err == nil {
		logger.Info("Submitted merge event")
		return false
	}

	switch {
	case errors.Is(err, jobmanager.ErrDuplicateJob):
		logger.Debug("Skipping duplicate merge")
	case errors.Is(err, jobmanager.ErrRetriesExhausted):
		logger.Debug("Skipping exhausted merge ticket")
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
		logger.Error("Failed to submit merge event", zap.Error(err))
	}

	return false
}

// hasUnmergeablePR checks all repos for PRs with merge conflicts.
// Applies idle detection: if the PR has no recent human activity,
// it is labeled and skipped. Returns true if any PR is unmergeable
// and active.
func (s *MergeScanner) hasUnmergeablePR(
	logger *zap.Logger,
	repos []models.RepoCoord,
	head string,
) bool {
	for _, r := range repos {
		pr, err := s.prs.GetPRForBranch(r.Owner, r.Repo, head)
		if err != nil {
			logger.Debug("No PR found for repo, skipping",
				zap.String("repo", r.Owner+"/"+r.Repo))
			continue
		}

		state, err := s.mergeCheck.GetPRMergeability(r.Owner, r.Repo, pr.Number)
		if err != nil {
			logger.Warn("Failed to check mergeability",
				zap.String("repo", r.Owner+"/"+r.Repo),
				zap.Error(err))
			continue
		}

		if state.Mergeable == nil {
			logger.Debug("Mergeability unknown, skipping until next cycle",
				zap.String("repo", r.Owner+"/"+r.Repo))
			continue
		}

		if *state.Mergeable {
			continue
		}

		// PR is unmergeable — check idle status.
		if s.isIdlePR(logger, r.Owner, r.Repo, pr) {
			continue
		}

		logger.Info("Found unmergeable PR",
			zap.String("repo", r.Owner+"/"+r.Repo),
			zap.Int("pr", pr.Number))
		return true
	}

	return false
}

// isIdlePR checks whether a PR should be skipped due to inactivity.
// Returns true if the PR is idle (labeled or newly labeled). Returns
// false if idle detection is disabled, the PR is active, or on error.
func (s *MergeScanner) isIdlePR(
	logger *zap.Logger,
	owner, repo string,
	pr *models.PRDetails,
) bool {
	if s.cfg.IdleDays <= 0 || s.cfg.IdleLabel == "" {
		return false
	}

	hasLabel, err := s.labeler.HasPRLabel(owner, repo, pr.Number, s.cfg.IdleLabel)
	if err != nil {
		logger.Warn("Failed to check idle label, treating as active",
			zap.String("repo", owner+"/"+repo),
			zap.Error(err))
		return false
	}
	if hasLabel {
		logger.Debug("PR has idle label, skipping",
			zap.String("repo", owner+"/"+repo),
			zap.Int("pr", pr.Number))
		return true
	}

	comments, err := s.prs.GetPRComments(owner, repo, pr.Number, time.Time{})
	if err != nil {
		logger.Warn("Failed to fetch PR comments for idle check, treating as active",
			zap.String("repo", owner+"/"+repo),
			zap.Error(err))
		return false
	}

	lastHuman := lastHumanCommentTime(comments, s.cfg.BotUsername,
		s.cfg.KnownBotUsernames, s.cfg.IgnoredUsernames)
	idleThreshold := time.Now().AddDate(0, 0, -s.cfg.IdleDays)

	// A PR is idle when either no human has ever commented
	// (zero time) or the last human comment is older than the
	// threshold.
	idle := lastHuman.IsZero() || lastHuman.Before(idleThreshold)
	if idle {
		logger.Info("PR is idle, adding label",
			zap.String("repo", owner+"/"+repo),
			zap.Int("pr", pr.Number),
			zap.Time("last_human_comment", lastHuman))
		if err := s.labeler.AddPRLabel(owner, repo, pr.Number, s.cfg.IdleLabel); err != nil {
			logger.Warn("Failed to add idle label, treating as active",
				zap.String("repo", owner+"/"+repo),
				zap.Error(err))
			return false
		}
		return true
	}

	return false
}

// lastHumanCommentTime returns the timestamp of the most recent PR
// comment not authored by the bot or any known bot/ignored user.
// Returns the zero time if no human comments exist.
func lastHumanCommentTime(
	comments []models.PRComment,
	botUsername string,
	knownBots, ignoredUsers []string,
) time.Time {
	excluded := make(map[string]bool)
	excluded[strings.ToLower(strings.TrimSuffix(botUsername, "[bot]"))] = true
	for _, u := range knownBots {
		excluded[strings.ToLower(strings.TrimSuffix(u, "[bot]"))] = true
	}
	for _, u := range ignoredUsers {
		excluded[strings.ToLower(strings.TrimSuffix(u, "[bot]"))] = true
	}

	var latest time.Time
	for _, c := range comments {
		normAuthor := strings.ToLower(strings.TrimSuffix(c.Author.Username, "[bot]"))
		if excluded[normAuthor] {
			continue
		}
		if c.Timestamp.After(latest) {
			latest = c.Timestamp
		}
	}
	return latest
}
