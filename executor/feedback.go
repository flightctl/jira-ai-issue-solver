package executor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/commentfilter"
	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/taskfile"
	"jira-ai-issue-solver/workspace"
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

	defer func() {
		// On failure post error comment (but do NOT revert status --
		// the ticket stays "in review").
		if retErr != nil {
			p.handleFeedbackFailure(logger, job.TicketKey, settings, retErr)
		}
	}()

	if settings.IsMultiRepo() {
		return p.executeMultiRepoFeedback(ctx, job, logger, workItem, settings)
	}

	// Track container for cleanup.
	var ctr *container.Container

	defer func() {
		if ctr != nil {
			if stopErr := p.containers.Stop(context.Background(), ctr); stopErr != nil {
				logger.Warn("Failed to stop container", zap.Error(stopErr))
			}
		}
	}()

	// --- Step 3: Find PR by branch ---
	branchName := fmt.Sprintf("%s/%s", p.cfg.BotUsername, job.TicketKey)
	prDetails, err := p.git.GetPRForBranch(settings.Repos[0].Owner, settings.Repos[0].Repo, settings.PRHead(branchName))
	if err != nil {
		return result, fmt.Errorf("find PR for branch %s: %w", branchName, err)
	}

	// --- Step 4: Find or create workspace (self-healing) ---
	wsPath, reused, err := p.workspaces.FindOrCreate(job.TicketKey, settings.Repos[0].CloneURL)
	if err != nil {
		return result, fmt.Errorf("prepare workspace: %w", err)
	}
	logger.Info("Workspace ready",
		zap.String("path", wsPath),
		zap.Bool("reused", reused))

	// --- Step 4a: Set origin to fork and fetch ---
	if err := p.ensureForkRemote(wsPath, settings); err != nil {
		return result, err
	}

	// --- Step 5: Switch to branch and sync with remote ---
	if err := p.git.SwitchBranch(wsPath, branchName); err != nil {
		return result, fmt.Errorf("switch to branch: %w", err)
	}
	if err := p.git.SyncWithRemote(wsPath, branchName, nil); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 6: Fetch and categorize comments ---
	allComments, err := p.git.GetPRComments(
		settings.Repos[0].Owner, settings.Repos[0].Repo, prDetails.Number, time.Time{})
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

	// --- Step 7: Load repo config ---
	repoCfg, err := repoconfig.Load(wsPath)
	if err != nil {
		logger.Warn("Failed to load repo config, using defaults", zap.Error(err))
		repoCfg = repoconfig.Default()
	}

	// --- Step 8: Clone imports ---
	mergedImports, err := p.cloneImports(logger, wsPath, settings, repoCfg)
	if err != nil {
		return result, err
	}

	// --- Step 9: Download attachments, write issue and feedback task files ---
	if err := p.writeFeedbackFiles(
		logger, *workItem, *prDetails, newComments, addressedComments, wsPath, settings,
	); err != nil {
		return result, err
	}

	// --- Step 10: Determine AI provider ---
	provider := p.resolveProvider(settings)
	logger.Info("AI provider selected", zap.String("provider", provider))

	// --- Step 11: Build AI command ---
	sp := buildScriptParams(provider, p.cfg.DefaultClaudeModel, p.cfg.DefaultGeminiModel, repoCfg)
	execCommand := buildExecCommand(sp)

	// --- Step 12: Resolve and start container ---
	ctr, err = p.startContainer(ctx, wsPath, job.TicketKey, provider, settings)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	// --- Step 12a: Run import install commands inside container ---
	if err := p.runImportInstalls(ctx, logger, ctr, mergedImports); err != nil {
		return result, fmt.Errorf("import install: %w", err)
	}

	// --- Step 12b: Strip remote auth before AI execution ---
	if err := p.git.StripRemoteAuth(wsPath); err != nil {
		return result, fmt.Errorf("strip remote auth: %w", err)
	}
	authStripped := true
	defer func() {
		if authStripped {
			if restoreErr := p.git.RestoreRemoteAuth(wsPath, settings.CommitOwner(), settings.Repos[0].Repo); restoreErr != nil {
				logger.Warn("Failed to restore remote auth", zap.Error(restoreErr))
			}
		}
	}()

	// --- Step 13: Execute AI agent ---
	execCtx := ctx
	if p.cfg.SessionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, p.cfg.SessionTimeout)
		defer cancel()
	}

	_, exitCode, execErr := p.containers.Exec(
		execCtx, ctr, execCommand)
	if execErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("job cancelled: %w", ctx.Err())
		}
		logger.Warn("AI agent exec failed", zap.Error(execErr))
	}

	session := readSessionOutput(wsPath)
	p.applyCostEstimate(&session)

	logger.Info("AI session completed",
		zap.Int("exit_code", exitCode),
		zap.Float64("cost_usd", session.CostUSD),
		zap.Any("validation_passed", session.ValidationPassed),
		zap.String("summary", session.Summary))
	result.CostUSD = session.CostUSD

	// --- Step 13a: Restore remote auth ---
	// In fork mode, origin is set to the fork so that SyncWithRemote
	// fetches from the fork (where the API commit was created).
	if err := p.git.RestoreRemoteAuth(wsPath, settings.CommitOwner(), settings.Repos[0].Repo); err != nil {
		return result, fmt.Errorf("restore remote auth: %w", err)
	}
	authStripped = false

	if execErr != nil {
		if execCtx.Err() != nil {
			return result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return result, fmt.Errorf("AI session failed: %w", execErr)
	}

	// --- Step 14: Check for changes ---
	hasChanges, err := p.git.HasChanges(wsPath, settings.Repos[0].BaseBranch)
	if err != nil {
		return result, fmt.Errorf("check changes: %w", err)
	}
	if !hasChanges {
		return result, fmt.Errorf("AI produced no changes (exit code: %d)", exitCode)
	}

	// --- Step 15: Commit via GitHub API ---
	importExcludes := collectExcludes(mergedImports)
	commitMsg := fmt.Sprintf("%s: address PR feedback", job.TicketKey)
	sha, err := p.git.CommitChanges(
		settings.Repos[0].Owner, settings.CommitOwner(), settings.Repos[0].Repo, branchName,
		commitMsg, wsPath, settings.Repos[0].BaseBranch, workItem.Assignee, importExcludes,
	)
	if errors.Is(err, services.ErrNoChanges) {
		if p.isFinalAttempt(job.AttemptNum) {
			logger.Info("Final attempt produced no changes, posting unable-to-address replies")
			p.replyUnableToAddress(logger, settings, prDetails, newComments)
			return result, nil
		}
		return result, fmt.Errorf("AI produced no committable changes (exit code: %d)", exitCode)
	}
	if err != nil {
		return result, fmt.Errorf("commit changes: %w", err)
	}

	// --- Step 16: Post-commit sync ---
	if err := p.git.SyncWithRemote(wsPath, branchName, importExcludes); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 17: Reply to addressed comments ---
	aiResponses := readCommentResponses(wsPath)
	p.replyToComments(logger, settings, prDetails, newComments, sha, aiResponses)

	p.postOrUpdateCostComment(logger,
		settings.Repos[0].Owner, settings.Repos[0].Repo,
		prDetails.Number, result.CostUSD, "Feedback")

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

// repoPRInfo groups a repo's PR details and categorized comments for
// multi-repo feedback processing.
type repoPRInfo struct {
	repo     models.RepoSettings
	pr       *models.PRDetails
	repoCfg  *repoconfig.Config
	newCmts  []models.PRComment
	addrCmts []models.PRComment
}

// executeMultiRepoFeedback handles feedback for workspaces with
// multiple repositories. It finds PRs across all repos, aggregates
// comments, runs one AI session, then fans out commits and replies.
func (p *Pipeline) executeMultiRepoFeedback(
	ctx context.Context,
	job *jobmanager.Job,
	logger *zap.Logger,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
) (result jobmanager.JobResult, retErr error) {
	var ctr *container.Container

	defer func() {
		if ctr != nil {
			if stopErr := p.containers.Stop(context.Background(), ctr); stopErr != nil {
				logger.Warn("Failed to stop container", zap.Error(stopErr))
			}
		}
	}()

	branchName := fmt.Sprintf("%s/%s", p.cfg.BotUsername, job.TicketKey)
	head := settings.PRHead(branchName)

	// --- Step 3: Find PRs across all repos ---
	var repoInfos []repoPRInfo
	for _, repo := range settings.Repos {
		pr, err := p.git.GetPRForBranch(repo.Owner, repo.Repo, head)
		if err != nil {
			logger.Debug("No PR found for repo",
				zap.String("repo", repo.Name))
			continue
		}
		repoInfos = append(repoInfos, repoPRInfo{repo: repo, pr: pr})
	}
	if len(repoInfos) == 0 {
		return result, fmt.Errorf("no PRs found for branch %s in any repository", branchName)
	}

	// --- Step 4: Prepare multi-repo workspace ---
	repoEntries := make([]workspace.RepoEntry, len(settings.Repos))
	for i, r := range settings.Repos {
		repoEntries[i] = workspace.RepoEntry{Name: r.Name, URL: r.CloneURL}
	}
	wsPath, reused, err := p.workspaces.FindOrCreateMultiRepo(job.TicketKey, repoEntries)
	if err != nil {
		return result, fmt.Errorf("prepare workspace: %w", err)
	}
	logger.Info("Multi-repo workspace ready",
		zap.String("path", wsPath),
		zap.Bool("reused", reused))

	// --- Step 5: Per-repo branch setup ---
	if err := p.syncMultiRepoBranches(wsPath, branchName, settings); err != nil {
		return result, err
	}

	// --- Step 6: Fetch and categorize comments per repo ---
	allNew, allAddressed, err := p.fetchMultiRepoComments(repoInfos)
	if err != nil {
		return result, err
	}
	if len(allNew) == 0 {
		logger.Info("No new comments to address across any repo")
		return result, nil
	}

	// --- Step 7: Load repo configs and merge imports ---
	repoConfigs := make([]*repoconfig.Config, len(settings.Repos))
	for i, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		cfg, err := repoconfig.Load(repoDir)
		if err != nil {
			logger.Warn("Failed to load repo config, using defaults",
				zap.String("repo", repo.Name), zap.Error(err))
			cfg = repoconfig.Default()
		}
		repoConfigs[i] = cfg
		// Attach repo config to any matching repoInfo.
		for j := range repoInfos {
			if repoInfos[j].repo.Name == repo.Name {
				repoInfos[j].repoCfg = cfg
			}
		}
	}

	mergedImports := mergeMultiRepoImports(settings, repoConfigs)
	if err := p.cloneImportEntries(logger, wsPath, mergedImports); err != nil {
		return result, err
	}

	// --- Step 8: Write issue and feedback task files ---
	if err := p.writeMultiRepoFeedbackFiles(
		logger, *workItem, repoInfos[0].pr, allNew, allAddressed, wsPath, settings,
	); err != nil {
		return result, err
	}

	// --- Step 9: Provider, command, container ---
	provider := p.resolveProvider(settings)
	sp := buildScriptParams(provider, p.cfg.DefaultClaudeModel, p.cfg.DefaultGeminiModel, repoConfigs[0])
	execCommand := buildExecCommand(sp)

	ctr, err = p.startContainer(ctx, wsPath, job.TicketKey, provider, settings)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	if err := p.runImportInstalls(ctx, logger, ctr, mergedImports); err != nil {
		return result, fmt.Errorf("import install: %w", err)
	}

	// --- Step 10: Strip remote auth per repo ---
	for _, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		if err := p.git.StripRemoteAuth(repoDir); err != nil {
			return result, fmt.Errorf("strip remote auth for %s: %w", repo.Name, err)
		}
	}
	authStripped := true
	defer func() {
		if authStripped {
			for _, repo := range settings.Repos {
				repoDir := filepath.Join(wsPath, repo.Name)
				if restoreErr := p.git.RestoreRemoteAuth(
					repoDir, settings.CommitOwnerFor(repo), repo.Repo); restoreErr != nil {
					logger.Warn("Failed to restore remote auth",
						zap.String("repo", repo.Name), zap.Error(restoreErr))
				}
			}
		}
	}()

	// --- Step 11: Execute AI agent ---
	execCtx := ctx
	if p.cfg.SessionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, p.cfg.SessionTimeout)
		defer cancel()
	}

	_, exitCode, execErr := p.containers.Exec(execCtx, ctr, execCommand)
	if execErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("job cancelled: %w", ctx.Err())
		}
		logger.Warn("AI agent exec failed", zap.Error(execErr))
	}

	session := readSessionOutput(wsPath)
	p.applyCostEstimate(&session)
	logger.Info("AI session completed",
		zap.Int("exit_code", exitCode),
		zap.Float64("cost_usd", session.CostUSD),
		zap.Any("validation_passed", session.ValidationPassed),
		zap.String("summary", session.Summary))
	result.CostUSD = session.CostUSD

	// --- Step 11a: Restore remote auth per repo ---
	for _, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		if err := p.git.RestoreRemoteAuth(
			repoDir, settings.CommitOwnerFor(repo), repo.Repo); err != nil {
			return result, fmt.Errorf("restore remote auth for %s: %w", repo.Name, err)
		}
	}
	authStripped = false

	if execErr != nil {
		if execCtx.Err() != nil {
			return result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return result, fmt.Errorf("AI session failed: %w", execErr)
	}

	// --- Steps 12-14: Check changes, commit, reply ---
	importExcludes := collectExcludes(mergedImports)
	committed, err := p.commitMultiRepoFeedback(logger, commitMultiRepoParams{
		settings:     settings,
		workItem:     workItem,
		wsPath:       wsPath,
		branchName:   branchName,
		ticketKey:    job.TicketKey,
		excludes:     importExcludes,
		exitCode:     exitCode,
		finalAttempt: p.isFinalAttempt(job.AttemptNum),
		repoInfos:    repoInfos,
	})
	if err != nil {
		return result, err
	}
	if !committed {
		return result, nil
	}

	// Post cost on the first PR only to avoid double-counting.
	p.postOrUpdateCostComment(logger,
		repoInfos[0].repo.Owner, repoInfos[0].repo.Repo,
		repoInfos[0].pr.Number, result.CostUSD, "Feedback")

	result.PRURL = repoInfos[0].pr.URL
	result.PRNumber = repoInfos[0].pr.Number
	result.ValidationPassed = !shouldCreateDraft(session, exitCode, false)

	logger.Info("Multi-repo feedback processed",
		zap.Int("repos_with_prs", len(repoInfos)),
		zap.Int("new_comments_addressed", len(allNew)))

	return result, nil
}

// syncMultiRepoBranches sets up fork remotes, switches to the branch,
// and syncs with the remote for each repo in the workspace.
func (p *Pipeline) syncMultiRepoBranches(
	wsPath, branchName string,
	settings *models.ProjectSettings,
) error {
	for _, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		if err := p.ensureForkRemoteForRepo(repoDir, settings, repo); err != nil {
			return err
		}
		if err := p.git.SwitchBranch(repoDir, branchName); err != nil {
			return fmt.Errorf("switch to branch in %s: %w", repo.Name, err)
		}
		if err := p.git.SyncWithRemote(repoDir, branchName, nil); err != nil {
			return fmt.Errorf("sync with remote for %s: %w", repo.Name, err)
		}
	}
	return nil
}

// fetchMultiRepoComments fetches and categorizes PR comments across
// all repos with PRs. Updates each repoPRInfo with its categorized
// comments and returns the aggregated new and addressed lists.
func (p *Pipeline) fetchMultiRepoComments(
	repoInfos []repoPRInfo,
) (allNew, allAddressed []models.PRComment, err error) {
	for i := range repoInfos {
		ri := &repoInfos[i]
		comments, err := p.git.GetPRComments(
			ri.repo.Owner, ri.repo.Repo, ri.pr.Number, time.Time{})
		if err != nil {
			return nil, nil, fmt.Errorf("get PR comments for %s: %w", ri.repo.Name, err)
		}
		filtered := commentfilter.Filter(comments, p.commentFilterConfig())
		ri.newCmts, ri.addrCmts = CategorizeComments(filtered, p.cfg.BotUsername)
		allNew = append(allNew, ri.newCmts...)
		allAddressed = append(allAddressed, ri.addrCmts...)
	}
	return allNew, allAddressed, nil
}

// writeMultiRepoFeedbackFiles downloads attachments and writes the
// issue and feedback task files for a multi-repo workspace.
func (p *Pipeline) writeMultiRepoFeedbackFiles(
	logger *zap.Logger,
	workItem models.WorkItem,
	pr *models.PRDetails,
	newComments, addressedComments []models.PRComment,
	wsPath string,
	settings *models.ProjectSettings,
) error {
	downloaded, err := p.downloadAttachments(logger, workItem, wsPath)
	if err != nil {
		return fmt.Errorf("download attachments: %w", err)
	}
	if err := p.taskWriter.WriteIssue(workItem, wsPath, downloaded); err != nil {
		return fmt.Errorf("write issue file: %w", err)
	}

	repoContexts := make([]taskfile.RepoContext, len(settings.Repos))
	for i, repo := range settings.Repos {
		repoContexts[i] = taskfile.RepoContext{
			Name:                     repo.Name,
			Dir:                      filepath.Join(wsPath, repo.Name),
			OverrideInstructions:     repo.Instructions,
			OverrideFeedbackWorkflow: repo.FeedbackWorkflow,
		}
	}
	if err := p.taskWriter.WriteMultiRepoFeedbackTask(
		*pr, newComments, addressedComments, wsPath, repoContexts,
	); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}
	return nil
}

// writeFeedbackFiles downloads attachments and writes the issue and
// feedback task files for a single-repo feedback pipeline run.
func (p *Pipeline) writeFeedbackFiles(
	logger *zap.Logger,
	workItem models.WorkItem,
	prDetails models.PRDetails,
	newComments, addressedComments []models.PRComment,
	wsPath string,
	settings *models.ProjectSettings,
) error {
	downloaded, err := p.downloadAttachments(logger, workItem, wsPath)
	if err != nil {
		return fmt.Errorf("download attachments: %w", err)
	}
	if err := p.taskWriter.WriteIssue(workItem, wsPath, downloaded); err != nil {
		return fmt.Errorf("write issue file: %w", err)
	}
	if err := p.taskWriter.WriteFeedbackTask(
		prDetails, newComments, addressedComments, wsPath,
		settings.Repos[0].Instructions, settings.Repos[0].FeedbackWorkflow,
	); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}
	return nil
}

type commitMultiRepoParams struct {
	settings     *models.ProjectSettings
	workItem     *models.WorkItem
	wsPath       string
	branchName   string
	ticketKey    string
	excludes     []string
	exitCode     int
	finalAttempt bool
	repoInfos    []repoPRInfo
}

// commitMultiRepoFeedback checks for changes across repos, commits
// them, syncs with remotes, and replies to PR comments. Returns true
// if commits were made, false if no changes (final-attempt replies
// are posted and nil error returned in that case).
func (p *Pipeline) commitMultiRepoFeedback(
	logger *zap.Logger,
	params commitMultiRepoParams,
) (bool, error) {
	// Check for any changes across repos.
	anyChanges := false
	for _, repo := range params.settings.Repos {
		repoDir := filepath.Join(params.wsPath, repo.Name)
		has, err := p.git.HasChanges(repoDir, repo.BaseBranch)
		if err != nil {
			return false, fmt.Errorf("check changes for %s: %w", repo.Name, err)
		}
		if has {
			anyChanges = true
			break
		}
	}
	if !anyChanges {
		if params.finalAttempt {
			logger.Info("Final attempt produced no changes, posting unable-to-address replies")
			for _, ri := range params.repoInfos {
				p.replyToCommentsOnRepo(logger, ri.repo.Owner, ri.repo.Repo,
					ri.pr, ri.newCmts, "unable")
			}
			return false, nil
		}
		return false, fmt.Errorf("AI produced no changes (exit code: %d)", params.exitCode)
	}

	// Commit per repo.
	commitMsg := fmt.Sprintf("%s: address PR feedback", params.ticketKey)
	var commitSHA string

	for _, repo := range params.settings.Repos {
		repoDir := filepath.Join(params.wsPath, repo.Name)
		has, _ := p.git.HasChanges(repoDir, repo.BaseBranch)
		if !has {
			continue
		}

		sha, err := p.git.CommitChanges(
			repo.Owner, params.settings.CommitOwnerFor(repo), repo.Repo, params.branchName,
			commitMsg, repoDir, repo.BaseBranch, params.workItem.Assignee, params.excludes,
		)
		if errors.Is(err, services.ErrNoChanges) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("commit changes for %s: %w", repo.Name, err)
		}
		if commitSHA == "" {
			commitSHA = sha
		}

		if err := p.git.SyncWithRemote(repoDir, params.branchName, params.excludes); err != nil {
			return false, fmt.Errorf("sync with remote for %s: %w", repo.Name, err)
		}
	}

	if commitSHA == "" {
		if params.finalAttempt {
			logger.Info("Final attempt produced no committable changes, posting unable-to-address replies")
			for _, ri := range params.repoInfos {
				p.replyToCommentsOnRepo(logger, ri.repo.Owner, ri.repo.Repo,
					ri.pr, ri.newCmts, "unable")
			}
			return false, nil
		}
		return false, fmt.Errorf("AI produced no committable changes (exit code: %d)", params.exitCode)
	}

	// Reply to comments.
	aiResponses := readCommentResponses(params.wsPath)
	shortSHA := commitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	for _, ri := range params.repoInfos {
		p.replyToCommentsOnRepo(logger, ri.repo.Owner, ri.repo.Repo,
			ri.pr, ri.newCmts, shortSHA, aiResponses)
	}

	return true, nil
}

// ensureForkRemoteForRepo sets a repo's origin to the assignee's fork
// and fetches its refs. No-op when fork mode is inactive.
func (p *Pipeline) ensureForkRemoteForRepo(
	repoDir string,
	settings *models.ProjectSettings,
	repo models.RepoSettings,
) error {
	if settings.ForkOwner() == "" {
		return nil
	}
	if err := p.git.RestoreRemoteAuth(repoDir, settings.CommitOwnerFor(repo), repo.Repo); err != nil {
		return fmt.Errorf("set fork remote for %s: %w", repo.Name, err)
	}
	if err := p.git.FetchRemote(repoDir); err != nil {
		return fmt.Errorf("fetch fork for %s: %w", repo.Name, err)
	}
	return nil
}

// replyToCommentsOnRepo posts replies to comments on a specific
// repo's PR. When sha is "unable", posts unable-to-address replies.
// When sha is a commit SHA, posts addressed replies with optional AI
// response summaries.
func (p *Pipeline) replyToCommentsOnRepo(
	logger *zap.Logger,
	owner, repo string,
	pr *models.PRDetails,
	comments []models.PRComment,
	sha string,
	aiResponses ...map[int64]string,
) {
	var responses map[int64]string
	if len(aiResponses) > 0 {
		responses = aiResponses[0]
	}

	for _, c := range comments {
		var replyBody string
		if sha == "unable" {
			replyBody = "I was unable to produce code changes to address this comment after multiple attempts."
		} else if summary, ok := responses[c.ID]; ok {
			replyBody = fmt.Sprintf("%s\n\nAddressed in %s.", summary, sha)
		} else {
			replyBody = fmt.Sprintf("Addressed in %s.", sha)
		}

		if c.IsReviewComment {
			if err := p.git.ReplyToComment(owner, repo, pr.Number, c.ID, replyBody); err != nil {
				logger.Warn("Failed to reply to review comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		} else {
			markedBody := fmt.Sprintf("%s\n%s", replyBody, commentfilter.AddressedMarker(c.ID))
			if err := p.git.PostIssueComment(owner, repo, pr.Number, markedBody); err != nil {
				logger.Warn("Failed to reply to conversation comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		}
	}
}

// CategorizeComments separates PR comments into new (requiring action)
// and addressed (bot has already replied). Bot's own comments are
// excluded from both lists.
//
// A review comment is "addressed" when the bot has a threaded reply
// to it (InReplyTo match). A conversation comment is "addressed"
// when the bot has posted a comment containing an addressed marker
// (<!-- addressed: ID -->) referencing it.
//
// Both returned slices are non-nil (empty slices, not nil).
func CategorizeComments(comments []models.PRComment, botUsername string) (newComments, addressed []models.PRComment) {
	normBot := normalizeUsername(botUsername)
	botRepliedTo := commentfilter.BotRepliedTo(comments, normBot)

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

// ensureForkRemote sets the workspace origin to the assignee's fork
// and fetches its refs so the PR branch is available locally. This is
// a no-op when fork mode is not active (GitHubUsername is empty).
func (p *Pipeline) ensureForkRemote(wsPath string, settings *models.ProjectSettings) error {
	if settings.ForkOwner() == "" {
		return nil
	}
	if err := p.git.RestoreRemoteAuth(wsPath, settings.CommitOwner(), settings.Repos[0].Repo); err != nil {
		return fmt.Errorf("set fork remote: %w", err)
	}
	if err := p.git.FetchRemote(wsPath); err != nil {
		return fmt.Errorf("fetch fork: %w", err)
	}
	return nil
}

// handleFeedbackFailure posts an error comment on feedback failure.
// Unlike [Pipeline.handleFailure] for new tickets, feedback failures
// do not revert the ticket status (it stays "in review").
func (p *Pipeline) handleFeedbackFailure(
	logger *zap.Logger,
	ticketKey string,
	settings *models.ProjectSettings,
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

// replyToComments posts a reply to each comment that was processed.
// When the AI provides a per-comment response summary (via
// comment-responses.json), the reply includes that summary alongside
// the commit reference. Otherwise, a generic "Addressed in <sha>"
// reply is used.
//
// Review comments are replied to via the threaded review comment API.
// Conversation comments are replied to via a new issue comment that
// includes a machine-readable marker for deduplication.
//
// Failures are logged but not fatal.
func (p *Pipeline) replyToComments(
	logger *zap.Logger,
	settings *models.ProjectSettings,
	prDetails *models.PRDetails,
	comments []models.PRComment,
	commitSHA string,
	aiResponses map[int64]string,
) {
	shortSHA := commitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	for _, c := range comments {
		var replyBody string
		if summary, ok := aiResponses[c.ID]; ok {
			replyBody = fmt.Sprintf("%s\n\nAddressed in %s.", summary, shortSHA)
		} else {
			replyBody = fmt.Sprintf("Addressed in %s.", shortSHA)
		}

		if c.IsReviewComment {
			if err := p.git.ReplyToComment(
				settings.Repos[0].Owner, settings.Repos[0].Repo, prDetails.Number, c.ID, replyBody); err != nil {
				logger.Warn("Failed to reply to review comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		} else {
			// Conversation comments don't support threading, so
			// embed a marker that CategorizeComments can parse to
			// detect addressed comments.
			markedBody := fmt.Sprintf("%s\n%s", replyBody, commentfilter.AddressedMarker(c.ID))
			if err := p.git.PostIssueComment(
				settings.Repos[0].Owner, settings.Repos[0].Repo, prDetails.Number, markedBody); err != nil {
				logger.Warn("Failed to reply to conversation comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		}
	}
}

// isFinalAttempt returns true when the current attempt is the last
// one before the job manager stops retrying. This accounts for the
// coordinator's check (failureCounts > maxRetries), which allows
// maxRetries+1 total attempts.
func (p *Pipeline) isFinalAttempt(attemptNum int) bool {
	return p.cfg.MaxRetries >= 0 && attemptNum > p.cfg.MaxRetries
}

// replyUnableToAddress posts a reply to each comment indicating that
// the bot was unable to make changes after multiple attempts. The
// reply includes an addressed marker so the comment is not picked up
// again by future scanner cycles.
func (p *Pipeline) replyUnableToAddress(
	logger *zap.Logger,
	settings *models.ProjectSettings,
	prDetails *models.PRDetails,
	comments []models.PRComment,
) {
	for _, c := range comments {
		replyBody := "I was unable to produce code changes to address this comment after multiple attempts."

		if c.IsReviewComment {
			if err := p.git.ReplyToComment(
				settings.Repos[0].Owner, settings.Repos[0].Repo, prDetails.Number, c.ID, replyBody); err != nil {
				logger.Warn("Failed to reply to review comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		} else {
			markedBody := fmt.Sprintf("%s\n%s", replyBody, commentfilter.AddressedMarker(c.ID))
			if err := p.git.PostIssueComment(
				settings.Repos[0].Owner, settings.Repos[0].Repo, prDetails.Number, markedBody); err != nil {
				logger.Warn("Failed to reply to conversation comment",
					zap.Int64("comment_id", c.ID),
					zap.Error(err))
			}
		}
	}
}

// normalizeUsername strips the GitHub [bot] suffix and lowercases
// for case-insensitive comparison. Matches the normalization used
// by [commentfilter.Filter].
func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSuffix(s, "[bot]"))
}
