package recovery

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
)

// Compile-time check that StartupRunner implements Runner.
var _ Runner = (*StartupRunner)(nil)

const defaultContainerPrefix = "ai-bot-"

// StartupRunner implements the crash recovery and startup orchestration
// sequence. It queries Jira and GitHub to detect interrupted work and
// takes appropriate corrective action.
type StartupRunner struct {
	tracker    IssueTracker
	git        GitService
	workspaces WorkspaceCleaner
	containers ContainerCleaner
	jobs       JobSubmitter
	projects   ProjectResolver
	cfg        Config
	logger     *zap.Logger
}

// NewStartupRunner creates a StartupRunner with the given dependencies.
// Returns an error if any required parameter is invalid.
func NewStartupRunner(
	cfg Config,
	tracker IssueTracker,
	git GitService,
	workspaces WorkspaceCleaner,
	containers ContainerCleaner,
	jobs JobSubmitter,
	projects ProjectResolver,
	logger *zap.Logger,
) (*StartupRunner, error) {
	if cfg.BotUsername == "" {
		return nil, errors.New("bot username must not be empty")
	}
	if tracker == nil {
		return nil, errors.New("issue tracker must not be nil")
	}
	if git == nil {
		return nil, errors.New("git service must not be nil")
	}
	if workspaces == nil {
		return nil, errors.New("workspace cleaner must not be nil")
	}
	if containers == nil {
		return nil, errors.New("container cleaner must not be nil")
	}
	if jobs == nil {
		return nil, errors.New("job submitter must not be nil")
	}
	if projects == nil {
		return nil, errors.New("project resolver must not be nil")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	return &StartupRunner{
		tracker:    tracker,
		git:        git,
		workspaces: workspaces,
		containers: containers,
		jobs:       jobs,
		projects:   projects,
		cfg:        cfg,
		logger:     logger,
	}, nil
}

// Run executes the startup recovery sequence. All steps are best-effort:
// errors are logged but do not prevent subsequent steps from running.
// Returns nil unconditionally — the caller should not block startup on
// recovery failures.
func (r *StartupRunner) Run(ctx context.Context) error {
	r.logger.Info("Starting crash recovery")

	r.cleanOrphanedContainers(ctx)

	if ctx.Err() != nil {
		r.logger.Info("Recovery interrupted after container cleanup", zap.Error(ctx.Err()))
		return nil
	}

	r.recoverStuckTickets(ctx)

	if ctx.Err() != nil {
		r.logger.Info("Recovery interrupted after ticket recovery", zap.Error(ctx.Err()))
		return nil
	}

	r.cleanTerminalWorkspaces(ctx)

	if ctx.Err() != nil {
		r.logger.Info("Recovery interrupted after terminal cleanup", zap.Error(ctx.Err()))
		return nil
	}

	r.cleanStaleWorkspaces()

	r.logger.Info("Crash recovery complete")
	return nil
}

// cleanOrphanedContainers removes containers left by a prior crash.
func (r *StartupRunner) cleanOrphanedContainers(ctx context.Context) {
	prefix := r.cfg.ContainerPrefix
	if prefix == "" {
		prefix = defaultContainerPrefix
	}

	if err := r.containers.CleanupOrphans(ctx, prefix); err != nil {
		r.logger.Warn("Failed to clean orphaned containers", zap.Error(err))
		return
	}
	r.logger.Info("Orphaned container cleanup complete")
}

// recoverStuckTickets finds tickets stuck in "in progress" and resolves
// each one based on the state of its GitHub PR and branch.
func (r *StartupRunner) recoverStuckTickets(ctx context.Context) {
	items, err := r.tracker.SearchWorkItems(r.cfg.InProgressCriteria)
	if err != nil {
		r.logger.Warn("Failed to search for stuck tickets", zap.Error(err))
		return
	}

	if len(items) == 0 {
		r.logger.Info("No stuck tickets found")
		return
	}

	r.logger.Info("Found stuck tickets", zap.Int("count", len(items)))

	for _, item := range items {
		if ctx.Err() != nil {
			r.logger.Info("Recovery interrupted", zap.Error(ctx.Err()))
			return
		}
		r.recoverTicket(item)
	}
}

// recoverTicket determines what was interrupted for a single stuck
// ticket and takes the appropriate corrective action.
func (r *StartupRunner) recoverTicket(item models.WorkItem) {
	logger := r.logger.With(zap.String("ticket", item.Key))

	settings, err := r.projects.ResolveProject(item)
	if err != nil {
		logger.Warn("Failed to resolve project, skipping", zap.Error(err))
		return
	}

	branchName := fmt.Sprintf("%s/%s", r.cfg.BotUsername, item.Key)

	// Check for an existing PR.
	pr, err := r.git.GetPRForBranch(settings.Owner, settings.Repo, settings.PRHead(branchName))
	if err == nil && pr != nil {
		// Case 1: PR exists but ticket still "In Progress".
		// The status transition was interrupted — complete it.
		logger.Info("Found PR for stuck ticket, completing transition",
			zap.String("case", "pr_exists"),
			zap.String("pr_url", pr.URL),
			zap.Int("pr_number", pr.Number))
		r.completeTransition(logger, item.Key, settings, pr.URL)
		return
	}

	// No PR found. Check if the branch has commits beyond base.
	hasCommits, err := r.git.BranchHasCommits(
		settings.CommitOwner(), settings.Repo, branchName, settings.BaseBranch)
	if err != nil {
		// Can't determine branch state. Revert to todo so it can be
		// retried cleanly by the normal pipeline.
		logger.Warn("Failed to check branch commits, reverting to todo",
			zap.Error(err))
		r.revertAndRequeue(logger, item, settings)
		return
	}

	if hasCommits {
		// Case 2: Branch has commits but no PR.
		// AI work completed but the bot crashed before creating the PR.
		logger.Info("Found commits without PR, creating PR directly",
			zap.String("case", "commits_no_pr"))
		r.createPRFromCommits(logger, item, settings, branchName)
		return
	}

	// Case 3: No PR and no branch commits.
	// The job was interrupted mid-execution. Revert and re-queue.
	logger.Info("No PR and no commits, reverting to todo and re-queuing",
		zap.String("case", "no_pr_no_commits"))
	r.revertAndRequeue(logger, item, settings)
}

// completeTransition finishes the interrupted status transition by
// setting the PR URL on the ticket and transitioning to "in review".
func (r *StartupRunner) completeTransition(
	logger *zap.Logger,
	ticketKey string,
	settings *models.ProjectSettings,
	prURL string,
) {
	if settings.PRURLFieldName != "" {
		if err := r.tracker.SetFieldValue(ticketKey, settings.PRURLFieldName, prURL); err != nil {
			logger.Warn("Failed to set PR URL field", zap.Error(err))
		}
	} else {
		comment := fmt.Sprintf("[AI-BOT-PR] %s", prURL)
		if err := r.tracker.AddComment(ticketKey, comment); err != nil {
			logger.Warn("Failed to add PR URL comment", zap.Error(err))
		}
	}

	if err := r.tracker.TransitionStatus(ticketKey, settings.InReviewStatus); err != nil {
		logger.Warn("Failed to transition to in-review", zap.Error(err))
	}
}

// createPRFromCommits creates a PR from existing branch commits and
// completes the transition. On failure, leaves the ticket in-progress
// and adds a comment for manual intervention (to avoid data loss from
// requeuing over existing commits).
func (r *StartupRunner) createPRFromCommits(
	logger *zap.Logger,
	item models.WorkItem,
	settings *models.ProjectSettings,
	branchName string,
) {
	title, body := buildRecoveryPRContent(item)

	var assignees []string
	if settings.GitHubUsername != "" {
		assignees = []string{settings.GitHubUsername}
	}

	pr, err := r.git.CreatePR(models.PRParams{
		Owner:     settings.Owner,
		Repo:      settings.Repo,
		Title:     title,
		Body:      body,
		Head:      settings.PRHead(branchName),
		Base:      settings.BaseBranch,
		Assignees: assignees,
	})
	if err != nil {
		logger.Error("Failed to create PR from commits; leaving ticket in-progress for manual intervention",
			zap.String("branch", branchName),
			zap.Error(err))
		comment := fmt.Sprintf(
			"[AI-BOT-RECOVERY] PR creation failed during crash recovery. "+
				"Branch %q has commits. Manual PR creation required. Error: %v",
			branchName, err)
		if commentErr := r.tracker.AddComment(item.Key, comment); commentErr != nil {
			logger.Warn("Failed to add recovery comment", zap.Error(commentErr))
		}
		return
	}

	logger.Info("PR created from recovered commits",
		zap.String("pr_url", pr.URL),
		zap.Int("pr_number", pr.Number))
	r.completeTransition(logger, item.Key, settings, pr.URL)
}

// buildRecoveryPRContent generates PR title and body for recovered
// commits. Security-level tickets get redacted content.
func buildRecoveryPRContent(item models.WorkItem) (title, body string) {
	if item.HasSecurityLevel() {
		title = fmt.Sprintf("%s: Security fix", item.Key)
		body = fmt.Sprintf(
			"Security fix for %s.\n\nDetails redacted due to security level.",
			item.Key)
		return title, body
	}

	title = fmt.Sprintf("%s: %s", item.Key, item.Summary)
	body = fmt.Sprintf("Resolves %s\n\n## Summary\n%s", item.Key, item.Summary)
	if item.Description != "" {
		body += fmt.Sprintf("\n\n## Description\n%s", item.Description)
	}
	return title, body
}

// revertAndRequeue reverts the ticket to "todo" and submits it for
// re-execution via the job manager.
func (r *StartupRunner) revertAndRequeue(
	logger *zap.Logger,
	item models.WorkItem,
	settings *models.ProjectSettings,
) {
	if err := r.tracker.TransitionStatus(item.Key, settings.TodoStatus); err != nil {
		logger.Warn("Failed to revert ticket to todo", zap.Error(err))
	}

	_, err := r.jobs.Submit(jobmanager.Event{
		Type:      jobmanager.JobTypeNewTicket,
		TicketKey: item.Key,
	})
	if err != nil {
		logger.Warn("Failed to re-queue ticket", zap.Error(err))
	}
}

// cleanTerminalWorkspaces removes workspaces for tickets that are no
// longer in an active state.
func (r *StartupRunner) cleanTerminalWorkspaces(ctx context.Context) {
	if len(r.cfg.ActiveStatuses) == 0 {
		r.logger.Debug("No active statuses configured, skipping terminal workspace cleanup")
		return
	}

	cleaned, err := r.workspaces.CleanupByFilter(func(ticketKey string) bool {
		if ctx.Err() != nil {
			return false // Stop removing workspaces during shutdown.
		}
		item, err := r.tracker.GetWorkItem(ticketKey)
		if err != nil {
			// Can't determine status (possibly deleted ticket).
			// Remove the workspace.
			r.logger.Debug("Workspace ticket not found, marking for removal",
				zap.String("ticket", ticketKey),
				zap.Error(err))
			return true
		}
		return !r.cfg.ActiveStatuses[item.Status]
	})
	if err != nil {
		r.logger.Warn("Failed to clean terminal workspaces", zap.Error(err))
		return
	}
	if cleaned > 0 {
		r.logger.Info("Cleaned terminal workspaces", zap.Int("count", cleaned))
	}
}

// cleanStaleWorkspaces removes workspaces older than the configured TTL.
func (r *StartupRunner) cleanStaleWorkspaces() {
	if r.cfg.WorkspaceTTL <= 0 {
		return
	}

	cleaned, err := r.workspaces.CleanupStale(r.cfg.WorkspaceTTL)
	if err != nil {
		r.logger.Warn("Failed to clean stale workspaces", zap.Error(err))
		return
	}
	if cleaned > 0 {
		r.logger.Info("Cleaned stale workspaces", zap.Int("count", cleaned))
	}
}
