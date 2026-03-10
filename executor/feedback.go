package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/commentfilter"
	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
)

func (p *Pipeline) executeFeedback(ctx context.Context, job *jobmanager.Job) (result jobmanager.JobResult, retErr error) {
	logger := p.logger.With(
		zap.String("ticket", job.TicketKey),
		zap.String("job_id", job.ID),
		zap.Int("attempt", job.AttemptNum),
	)
	logger.Info("Starting feedback pipeline")

	// --- Step 1: Fetch work item ---
	workItem, err := p.tracker.GetWorkItem(job.TicketKey)
	if err != nil {
		return result, fmt.Errorf("get work item: %w", err)
	}

	// --- Step 2: Resolve project settings ---
	settings, err := p.projects.ResolveProject(*workItem)
	if err != nil {
		return result, fmt.Errorf("resolve project: %w", err)
	}

	// Track container for cleanup.
	var ctr *container.Container

	defer func() {
		if ctr != nil {
			if stopErr := p.containers.Stop(context.Background(), ctr); stopErr != nil {
				logger.Warn("Failed to stop container", zap.Error(stopErr))
			}
		}
		// On failure post error comment (but do NOT revert status --
		// the ticket stays "in review").
		if retErr != nil {
			p.handleFeedbackFailure(logger, job.TicketKey, settings, retErr)
		}
	}()

	// --- Step 3: Find PR by branch ---
	branchName := fmt.Sprintf("%s/%s", p.cfg.BotUsername, job.TicketKey)
	prDetails, err := p.git.GetPRForBranch(settings.Owner, settings.Repo, branchName)
	if err != nil {
		return result, fmt.Errorf("find PR for branch %s: %w", branchName, err)
	}

	// --- Step 4: Find or create workspace (self-healing) ---
	wsPath, reused, err := p.workspaces.FindOrCreate(job.TicketKey, settings.CloneURL)
	if err != nil {
		return result, fmt.Errorf("prepare workspace: %w", err)
	}
	logger.Info("Workspace ready",
		zap.String("path", wsPath),
		zap.Bool("reused", reused))

	// --- Step 5: Switch to branch and sync with remote ---
	if err := p.git.SwitchBranch(wsPath, branchName); err != nil {
		return result, fmt.Errorf("switch to branch: %w", err)
	}
	if err := p.git.SyncWithRemote(wsPath, branchName); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 6: Fetch and categorize comments ---
	allComments, err := p.git.GetPRComments(
		settings.Owner, settings.Repo, prDetails.Number, time.Time{})
	if err != nil {
		return result, fmt.Errorf("get PR comments: %w", err)
	}

	// Apply bot-loop prevention before categorization.
	filtered := commentfilter.Filter(allComments, p.commentFilterConfig())
	newComments, addressedComments := CategorizeComments(filtered, p.cfg.BotUsername)

	if len(newComments) == 0 {
		logger.Info("No new comments to address")
		return result, nil
	}

	// --- Step 7: Write feedback task file ---
	if err := p.taskWriter.WriteFeedbackTask(
		*prDetails, newComments, addressedComments, wsPath); err != nil {
		return result, fmt.Errorf("write task file: %w", err)
	}

	// --- Step 8: Determine AI provider ---
	provider := p.resolveProvider(settings)

	// --- Step 9: Load repo config ---
	repoCfg, err := repoconfig.Load(wsPath)
	if err != nil {
		logger.Warn("Failed to load repo config, using defaults", zap.Error(err))
		repoCfg = repoconfig.Default()
	}

	// --- Step 10: Write wrapper script ---
	sp := buildScriptParams(provider, repoCfg)
	if err := writeRunScript(wsPath, sp); err != nil {
		return result, fmt.Errorf("write run script: %w", err)
	}

	// --- Step 11: Resolve and start container ---
	ctr, err = p.startContainer(ctx, logger, wsPath, provider)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	// --- Step 12: Execute AI agent ---
	execCtx := ctx
	if p.cfg.SessionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, p.cfg.SessionTimeout)
		defer cancel()
	}

	_, exitCode, execErr := p.containers.Exec(
		execCtx, ctr, []string{"bash", "/workspace/.ai-bot/run.sh"})
	if execErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("job cancelled: %w", ctx.Err())
		}
		logger.Warn("AI agent exec failed", zap.Error(execErr))
	}

	session := readSessionOutput(wsPath)
	result.CostUSD = session.CostUSD

	if execErr != nil {
		if execCtx.Err() != nil {
			return result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return result, fmt.Errorf("AI session failed: %w", execErr)
	}

	// --- Step 13: Check for changes ---
	hasChanges, err := p.git.HasChanges(wsPath)
	if err != nil {
		return result, fmt.Errorf("check changes: %w", err)
	}
	if !hasChanges {
		return result, fmt.Errorf("AI produced no changes (exit code: %d)", exitCode)
	}

	// --- Step 14: Commit via GitHub API ---
	commitMsg := fmt.Sprintf("%s: address PR feedback", job.TicketKey)
	sha, err := p.git.CommitChanges(
		settings.Owner, settings.Repo, branchName,
		commitMsg, wsPath, workItem.Assignee,
	)
	if err != nil {
		return result, fmt.Errorf("commit changes: %w", err)
	}

	// --- Step 15: Post-commit sync ---
	if err := p.git.SyncWithRemote(wsPath, branchName); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 16: Reply to addressed comments ---
	shortSHA := sha
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	for _, c := range newComments {
		replyBody := fmt.Sprintf("Addressed in %s.", shortSHA)
		if err := p.git.ReplyToComment(
			settings.Owner, settings.Repo, prDetails.Number, c.ID, replyBody); err != nil {
			logger.Warn("Failed to reply to comment",
				zap.Int64("comment_id", c.ID),
				zap.Error(err))
		}
	}

	result.PRURL = prDetails.URL
	result.PRNumber = prDetails.Number
	// Repo-config draft setting is not consulted here (hardcoded false)
	// because the PR already exists — its draft status is not ours to change.
	result.ValidationPassed = !shouldCreateDraft(session, exitCode, false)

	logger.Info("Feedback processed",
		zap.String("url", prDetails.URL),
		zap.Int("number", prDetails.Number),
		zap.Int("new_comments_addressed", len(newComments)))

	return result, nil
}

// CategorizeComments separates PR comments into new (requiring action)
// and addressed (bot has already replied). Bot's own comments are
// excluded from both lists.
//
// A comment is considered "addressed" when the bot has posted a reply
// to it (identified by a bot comment whose InReplyTo matches the
// comment's ID). All other non-bot comments are "new".
//
// Both returned slices are non-nil (empty slices, not nil).
func CategorizeComments(comments []models.PRComment, botUsername string) (newComments, addressed []models.PRComment) {
	normBot := normalizeUsername(botUsername)

	// Find which comment IDs the bot has replied to.
	botRepliedTo := make(map[int64]bool)
	for _, c := range comments {
		if normalizeUsername(c.Author.Username) == normBot && c.InReplyTo != 0 {
			botRepliedTo[c.InReplyTo] = true
		}
	}

	// Categorize non-bot comments.
	for _, c := range comments {
		if normalizeUsername(c.Author.Username) == normBot {
			continue
		}
		if botRepliedTo[c.ID] {
			addressed = append(addressed, c)
		} else {
			newComments = append(newComments, c)
		}
	}

	// Normalize nil slices.
	if newComments == nil {
		newComments = []models.PRComment{}
	}
	if addressed == nil {
		addressed = []models.PRComment{}
	}

	return newComments, addressed
}

func (p *Pipeline) commentFilterConfig() commentfilter.Config {
	return commentfilter.Config{
		BotUsername:       p.cfg.BotUsername,
		IgnoredUsernames:  p.cfg.IgnoredUsernames,
		KnownBotUsernames: p.cfg.KnownBotUsernames,
		MaxThreadDepth:    p.cfg.MaxThreadDepth,
	}
}

// handleFeedbackFailure posts an error comment on feedback failure.
// Unlike [Pipeline.handleFailure] for new tickets, feedback failures
// do not revert the ticket status (it stays "in review").
func (p *Pipeline) handleFeedbackFailure(
	logger *zap.Logger,
	ticketKey string,
	settings *ProjectSettings,
	jobErr error,
) {
	if settings.DisableErrorComments {
		return
	}

	comment := fmt.Sprintf("AI feedback processing failed: %s", jobErr.Error())
	if err := p.tracker.AddComment(ticketKey, comment); err != nil {
		logger.Error("Failed to post error comment", zap.Error(err))
	}
}

// normalizeUsername strips the GitHub [bot] suffix and lowercases
// for case-insensitive comparison. Matches the normalization used
// by [commentfilter.Filter].
func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSuffix(s, "[bot]"))
}
