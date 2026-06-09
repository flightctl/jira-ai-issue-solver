package executor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/taskfile"
	"jira-ai-issue-solver/workspace"
)

func (p *Pipeline) executeMerge(ctx context.Context, job *jobmanager.Job) (result jobmanager.JobResult, retErr error) {
	logger := p.logger.With(
		zap.String("ticket", job.TicketKey),
		zap.String("job_id", job.ID),
		zap.Int("attempt", job.AttemptNum),
	)
	logger.Info("Starting merge pipeline")

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
		if retErr != nil {
			p.handleMergeFailure(logger, job.TicketKey, settings, retErr)
		}
	}()

	if settings.IsMultiRepo() {
		return p.executeMultiRepoMerge(ctx, job, logger, workItem, settings)
	}

	return p.executeSingleRepoMerge(ctx, job, logger, workItem, settings)
}

func (p *Pipeline) executeSingleRepoMerge(
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

	repo := settings.Repos[0]
	branchName := fmt.Sprintf("%s/%s", p.cfg.BotUsername, job.TicketKey)

	// --- Step 3: Find PR by branch ---
	prDetails, err := p.git.GetPRForBranch(repo.Owner, repo.Repo, settings.PRHead(branchName))
	if err != nil {
		return result, fmt.Errorf("find PR for branch %s: %w", branchName, err)
	}

	// --- Step 4: Find or create workspace ---
	wsPath, reused, err := p.workspaces.FindOrCreate(job.TicketKey, repo.CloneURL)
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

	// --- Step 6: Attempt merge ---
	conflictFiles, mergeErr := p.git.MergeBase(wsPath, repo.BaseBranch)

	if mergeErr == nil {
		// Tier 1: Clean merge — commit without AI.
		return p.commitCleanMerge(ctx, logger, job, workItem, settings, wsPath, branchName, prDetails)
	}

	if !errors.Is(mergeErr, services.ErrMergeConflict) {
		return result, fmt.Errorf("merge %s: %w", repo.BaseBranch, mergeErr)
	}

	// Tier 2: Conflicts — invoke AI for resolution.
	logger.Info("Merge conflicts detected, invoking AI for resolution")

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

	// --- Step 9: Write task files ---
	downloaded, dlErr := p.downloadAttachments(logger, *workItem, wsPath)
	if dlErr != nil {
		return result, fmt.Errorf("download attachments: %w", dlErr)
	}
	comments := p.fetchTicketComments(logger, workItem.Key)
	if err := p.taskWriter.WriteIssue(*workItem, wsPath, downloaded, comments); err != nil {
		return result, fmt.Errorf("write issue file: %w", err)
	}
	if err := p.taskWriter.WriteMergeConflictTask(
		*prDetails, conflictFiles, wsPath, repo.Instructions,
	); err != nil {
		return result, fmt.Errorf("write merge task file: %w", err)
	}

	// --- Step 10: Resolve and start container ---
	provider := p.resolveProvider(settings)
	sp := buildScriptParams(provider, p.cfg.DefaultClaudeModel, p.cfg.DefaultGeminiModel, repoCfg)
	execCommand := buildExecCommand(sp)

	ctr, err = p.startContainer(ctx, wsPath, job.TicketKey, provider, settings)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	if err := p.runImportInstalls(ctx, logger, ctr, mergedImports); err != nil {
		return result, fmt.Errorf("import install: %w", err)
	}

	// --- Step 10a: Strip remote auth ---
	if err := p.git.StripRemoteAuth(wsPath); err != nil {
		return result, fmt.Errorf("strip remote auth: %w", err)
	}
	authStripped := true
	defer func() {
		if authStripped {
			if restoreErr := p.git.RestoreRemoteAuth(wsPath, settings.CommitOwner(), repo.Repo); restoreErr != nil {
				logger.Warn("Failed to restore remote auth", zap.Error(restoreErr))
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

	logger.Info("AI merge resolution completed",
		zap.Int("exit_code", exitCode),
		zap.Float64("cost_usd", session.CostUSD))
	result.CostUSD = session.CostUSD

	// --- Step 11a: Restore remote auth ---
	if err := p.git.RestoreRemoteAuth(wsPath, settings.CommitOwner(), repo.Repo); err != nil {
		return result, fmt.Errorf("restore remote auth: %w", err)
	}
	authStripped = false

	if execErr != nil {
		if execCtx.Err() != nil {
			return result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return result, fmt.Errorf("AI session failed: %w", execErr)
	}

	// --- Step 12: Check for changes ---
	hasChanges, err := p.git.HasChanges(wsPath, repo.BaseBranch)
	if err != nil {
		return result, fmt.Errorf("check changes: %w", err)
	}
	if !hasChanges {
		return result, fmt.Errorf("AI produced no changes resolving merge conflicts (exit code: %d)", exitCode)
	}

	// --- Step 13: Commit via GitHub API ---
	importExcludes := collectExcludes(mergedImports)
	commitMsg := fmt.Sprintf("%s: resolve merge conflicts with %s", job.TicketKey, repo.BaseBranch)
	_, err = p.git.CommitChanges(
		repo.Owner, settings.CommitOwner(), repo.Repo, branchName,
		commitMsg, wsPath, repo.BaseBranch, workItem.Assignee, importExcludes,
	)
	if errors.Is(err, services.ErrNoChanges) {
		return result, fmt.Errorf("AI produced no committable changes resolving merge conflicts")
	}
	if err != nil {
		return result, fmt.Errorf("commit changes: %w", err)
	}

	// --- Step 14: Post-commit sync ---
	if err := p.git.SyncWithRemote(wsPath, branchName, importExcludes); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	p.postOrUpdateCostComment(logger,
		repo.Owner, repo.Repo, prDetails.Number, result.CostUSD, "Merge conflict resolution")

	result.PRURL = prDetails.URL
	result.PRNumber = prDetails.Number

	logger.Info("Merge conflicts resolved via AI",
		zap.String("url", prDetails.URL),
		zap.Int("number", prDetails.Number))

	return result, nil
}

// commitCleanMerge commits a clean merge (no conflicts) without AI.
func (p *Pipeline) commitCleanMerge(
	_ context.Context,
	logger *zap.Logger,
	job *jobmanager.Job,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
	wsPath, branchName string,
	prDetails *models.PRDetails,
) (jobmanager.JobResult, error) {
	var result jobmanager.JobResult
	repo := settings.Repos[0]

	hasChanges, err := p.git.HasChanges(wsPath, repo.BaseBranch)
	if err != nil {
		return result, fmt.Errorf("check changes: %w", err)
	}
	if !hasChanges {
		logger.Info("No changes after merge (already up to date)")
		return result, nil
	}

	commitMsg := fmt.Sprintf("%s: merge %s into %s", job.TicketKey, repo.BaseBranch, branchName)
	_, err = p.git.CommitChanges(
		repo.Owner, settings.CommitOwner(), repo.Repo, branchName,
		commitMsg, wsPath, repo.BaseBranch, workItem.Assignee, nil,
	)
	if errors.Is(err, services.ErrNoChanges) {
		logger.Info("No committable changes after clean merge")
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("commit clean merge: %w", err)
	}

	if err := p.git.SyncWithRemote(wsPath, branchName, nil); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	result.PRURL = prDetails.URL
	result.PRNumber = prDetails.Number

	logger.Info("Clean merge committed",
		zap.String("url", prDetails.URL),
		zap.Int("number", prDetails.Number))

	return result, nil
}

func (p *Pipeline) executeMultiRepoMerge(
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
	repoInfos, err := p.findMergeRepoPRs(logger, settings, head)
	if err != nil {
		return result, err
	}

	// --- Step 4: Prepare multi-repo workspace ---
	repoEntries := make([]workspace.RepoEntry, len(settings.Repos))
	for i, r := range settings.Repos {
		repoEntries[i] = workspace.RepoEntry{Name: r.Name, URL: r.CloneURL}
	}
	wsPath, reused, err := p.workspaces.FindOrCreateMultiRepo(job.TicketKey, repoEntries, settings.RootRepoURL)
	if err != nil {
		return result, fmt.Errorf("prepare workspace: %w", err)
	}
	logger.Info("Multi-repo workspace ready",
		zap.String("path", wsPath),
		zap.Bool("reused", reused))

	// --- Step 5: Per-repo branch setup ---
	if err := p.syncMultiRepoMergeBranches(wsPath, branchName, settings, repoInfos); err != nil {
		return result, err
	}

	// --- Step 6: Attempt merge per repo ---
	hasConflicts, allConflictFiles, err := p.attemptMultiRepoMerge(wsPath, repoInfos)
	if err != nil {
		return result, err
	}

	if !hasConflicts {
		return p.commitMultiRepoCleanMerge(logger, job, workItem, settings, wsPath, branchName, repoInfos)
	}

	// Tier 2: At least one repo has conflicts — AI resolution.
	logger.Info("Merge conflicts detected in multi-repo workspace, invoking AI")

	ctr, result, err = p.runMultiRepoMergeAI(ctx, logger, job, workItem, settings, wsPath, branchName, repoInfos, allConflictFiles)
	return result, err
}

func (p *Pipeline) findMergeRepoPRs(
	logger *zap.Logger,
	settings *models.ProjectSettings,
	head string,
) ([]mergeRepoPR, error) {
	var repoInfos []mergeRepoPR
	for _, repo := range settings.Repos {
		pr, err := p.git.GetPRForBranch(repo.Owner, repo.Repo, head)
		if err != nil {
			logger.Debug("No PR found for repo",
				zap.String("repo", repo.Name))
			continue
		}
		repoInfos = append(repoInfos, mergeRepoPR{repo: repo, pr: pr})
	}
	if len(repoInfos) == 0 {
		return nil, fmt.Errorf("no PRs found for branch %s in any repository", head)
	}
	return repoInfos, nil
}

func (p *Pipeline) syncMultiRepoMergeBranches(
	wsPath, branchName string,
	settings *models.ProjectSettings,
	repoInfos []mergeRepoPR,
) error {
	for _, ri := range repoInfos {
		repoDir := filepath.Join(wsPath, ri.repo.Name)
		if err := p.ensureForkRemoteForRepo(repoDir, settings, ri.repo); err != nil {
			return err
		}
		if err := p.git.SwitchBranch(repoDir, branchName); err != nil {
			return fmt.Errorf("switch to branch in %s: %w", ri.repo.Name, err)
		}
		if err := p.git.SyncWithRemote(repoDir, branchName, nil); err != nil {
			return fmt.Errorf("sync with remote for %s: %w", ri.repo.Name, err)
		}
	}
	return nil
}

func (p *Pipeline) attemptMultiRepoMerge(
	wsPath string,
	repoInfos []mergeRepoPR,
) (bool, []string, error) {
	hasConflicts := false
	var allConflictFiles []string
	for _, ri := range repoInfos {
		repoDir := filepath.Join(wsPath, ri.repo.Name)
		conflicts, mergeErr := p.git.MergeBase(repoDir, ri.repo.BaseBranch)
		if mergeErr == nil {
			continue
		}
		if !errors.Is(mergeErr, services.ErrMergeConflict) {
			return false, nil, fmt.Errorf("merge %s in %s: %w", ri.repo.BaseBranch, ri.repo.Name, mergeErr)
		}
		hasConflicts = true
		for _, f := range conflicts {
			allConflictFiles = append(allConflictFiles, ri.repo.Name+"/"+f)
		}
	}
	return hasConflicts, allConflictFiles, nil
}

//nolint:cyclop
func (p *Pipeline) runMultiRepoMergeAI(
	ctx context.Context,
	logger *zap.Logger,
	job *jobmanager.Job,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
	wsPath, branchName string,
	repoInfos []mergeRepoPR,
	allConflictFiles []string,
) (*container.Container, jobmanager.JobResult, error) {
	var result jobmanager.JobResult

	repoConfigs := make([]*repoconfig.Config, len(settings.Repos))
	for i, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		cfg, loadErr := repoconfig.Load(repoDir)
		if loadErr != nil {
			logger.Warn("Failed to load repo config, using defaults",
				zap.String("repo", repo.Name), zap.Error(loadErr))
			cfg = repoconfig.Default()
		}
		repoConfigs[i] = cfg
	}

	mergedImports := mergeMultiRepoImports(settings, repoConfigs)
	if err := p.cloneImportEntries(logger, wsPath, mergedImports); err != nil {
		return nil, result, err
	}

	if err := p.writeMultiRepoMergeFiles(logger, *workItem, repoInfos[0].pr, allConflictFiles, wsPath, settings); err != nil {
		return nil, result, err
	}

	provider := p.resolveProvider(settings)
	sp := buildScriptParams(provider, p.cfg.DefaultClaudeModel, p.cfg.DefaultGeminiModel, repoConfigs[0])
	execCommand := buildExecCommand(sp)

	ctr, err := p.startContainer(ctx, wsPath, job.TicketKey, provider, settings)
	if err != nil {
		return nil, result, fmt.Errorf("start container: %w", err)
	}

	if err := p.runImportInstalls(ctx, logger, ctr, mergedImports); err != nil {
		return ctr, result, fmt.Errorf("import install: %w", err)
	}

	strippedRepos := make([]models.RepoSettings, 0, len(settings.Repos))
	authStripped := true
	defer func() {
		if authStripped {
			for _, repo := range strippedRepos {
				repoDir := filepath.Join(wsPath, repo.Name)
				if restoreErr := p.git.RestoreRemoteAuth(
					repoDir, settings.CommitOwnerFor(repo), repo.Repo); restoreErr != nil {
					logger.Warn("Failed to restore remote auth",
						zap.String("repo", repo.Name), zap.Error(restoreErr))
				}
			}
		}
	}()
	for _, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		if err := p.git.StripRemoteAuth(repoDir); err != nil {
			return ctr, result, fmt.Errorf("strip remote auth for %s: %w", repo.Name, err)
		}
		strippedRepos = append(strippedRepos, repo)
	}

	execCtx := ctx
	if p.cfg.SessionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, p.cfg.SessionTimeout)
		defer cancel()
	}

	_, exitCode, execErr := p.containers.Exec(execCtx, ctr, execCommand)
	if execErr != nil && ctx.Err() != nil {
		return ctr, result, fmt.Errorf("job cancelled: %w", ctx.Err())
	}

	session := readSessionOutput(wsPath)
	p.applyCostEstimate(&session)
	result.CostUSD = session.CostUSD

	for _, repo := range settings.Repos {
		repoDir := filepath.Join(wsPath, repo.Name)
		if err := p.git.RestoreRemoteAuth(
			repoDir, settings.CommitOwnerFor(repo), repo.Repo); err != nil {
			return ctr, result, fmt.Errorf("restore remote auth for %s: %w", repo.Name, err)
		}
	}
	authStripped = false

	if execErr != nil {
		if execCtx.Err() != nil {
			return ctr, result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return ctr, result, fmt.Errorf("AI session failed: %w", execErr)
	}

	importExcludes := collectExcludes(mergedImports)
	committed, err := p.commitMultiRepoMergeResolution(logger, job, workItem, settings, wsPath, branchName, repoInfos, importExcludes)
	if err != nil {
		return ctr, result, err
	}
	if !committed {
		return ctr, result, fmt.Errorf("AI produced no committable changes resolving merge conflicts (exit code: %d)", exitCode)
	}

	p.postOrUpdateCostComment(logger,
		repoInfos[0].repo.Owner, repoInfos[0].repo.Repo,
		repoInfos[0].pr.Number, result.CostUSD, "Merge conflict resolution")

	result.PRURL = repoInfos[0].pr.URL
	result.PRNumber = repoInfos[0].pr.Number
	return ctr, result, nil
}

func (p *Pipeline) writeMultiRepoMergeFiles(
	logger *zap.Logger,
	workItem models.WorkItem,
	pr *models.PRDetails,
	conflictFiles []string,
	wsPath string,
	settings *models.ProjectSettings,
) error {
	downloaded, err := p.downloadAttachments(logger, workItem, wsPath)
	if err != nil {
		return fmt.Errorf("download attachments: %w", err)
	}
	comments := p.fetchTicketComments(logger, workItem.Key)
	if err := p.taskWriter.WriteIssue(workItem, wsPath, downloaded, comments); err != nil {
		return fmt.Errorf("write issue file: %w", err)
	}

	repoContexts := make([]taskfile.RepoContext, len(settings.Repos))
	for i, repo := range settings.Repos {
		repoContexts[i] = taskfile.RepoContext{
			Name:                 repo.Name,
			Dir:                  filepath.Join(wsPath, repo.Name),
			OverrideInstructions: repo.Instructions,
		}
	}
	if err := p.taskWriter.WriteMultiRepoMergeConflictTask(
		*pr, conflictFiles, wsPath, repoContexts,
	); err != nil {
		return fmt.Errorf("write merge task file: %w", err)
	}
	return nil
}

func (p *Pipeline) commitMultiRepoMergeResolution(
	logger *zap.Logger,
	job *jobmanager.Job,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
	wsPath, branchName string,
	repoInfos []mergeRepoPR,
	importExcludes []string,
) (bool, error) {
	committed := false
	for _, ri := range repoInfos {
		repoDir := filepath.Join(wsPath, ri.repo.Name)
		has, err := p.git.HasChanges(repoDir, ri.repo.BaseBranch)
		if err != nil {
			return false, fmt.Errorf("check changes for %s: %w", ri.repo.Name, err)
		}
		if !has {
			continue
		}

		commitMsg := fmt.Sprintf("%s: resolve merge conflicts with %s", job.TicketKey, ri.repo.BaseBranch)
		_, err = p.git.CommitChanges(
			ri.repo.Owner, settings.CommitOwnerFor(ri.repo), ri.repo.Repo, branchName,
			commitMsg, repoDir, ri.repo.BaseBranch, workItem.Assignee, importExcludes,
		)
		if errors.Is(err, services.ErrNoChanges) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("commit merge for %s: %w", ri.repo.Name, err)
		}
		committed = true

		if err := p.git.SyncWithRemote(repoDir, branchName, importExcludes); err != nil {
			return false, fmt.Errorf("sync with remote for %s: %w", ri.repo.Name, err)
		}
	}

	if committed {
		logger.Info("Multi-repo merge conflicts resolved via AI",
			zap.Int("repos", len(repoInfos)))
	}
	return committed, nil
}

// mergeRepoPR groups a repo's settings with its PR details for
// multi-repo merge processing.
type mergeRepoPR struct {
	repo models.RepoSettings
	pr   *models.PRDetails
}

// commitMultiRepoCleanMerge commits clean merges across repos.
func (p *Pipeline) commitMultiRepoCleanMerge(
	logger *zap.Logger,
	job *jobmanager.Job,
	workItem *models.WorkItem,
	settings *models.ProjectSettings,
	wsPath, branchName string,
	repoInfos []mergeRepoPR,
) (jobmanager.JobResult, error) {
	var result jobmanager.JobResult
	committed := false

	for _, ri := range repoInfos {
		repoDir := filepath.Join(wsPath, ri.repo.Name)
		has, err := p.git.HasChanges(repoDir, ri.repo.BaseBranch)
		if err != nil {
			return result, fmt.Errorf("check changes for %s: %w", ri.repo.Name, err)
		}
		if !has {
			continue
		}

		commitMsg := fmt.Sprintf("%s: merge %s into %s", job.TicketKey, ri.repo.BaseBranch, branchName)
		_, err = p.git.CommitChanges(
			ri.repo.Owner, settings.CommitOwnerFor(ri.repo), ri.repo.Repo, branchName,
			commitMsg, repoDir, ri.repo.BaseBranch, workItem.Assignee, nil,
		)
		if errors.Is(err, services.ErrNoChanges) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("commit clean merge for %s: %w", ri.repo.Name, err)
		}
		committed = true

		if err := p.git.SyncWithRemote(repoDir, branchName, nil); err != nil {
			return result, fmt.Errorf("sync with remote for %s: %w", ri.repo.Name, err)
		}
	}

	if !committed {
		logger.Info("No changes after multi-repo merge (already up to date)")
		return result, nil
	}

	result.PRURL = repoInfos[0].pr.URL
	result.PRNumber = repoInfos[0].pr.Number

	logger.Info("Multi-repo clean merge committed",
		zap.Int("repos", len(repoInfos)))

	return result, nil
}

func (p *Pipeline) handleMergeFailure(
	logger *zap.Logger,
	ticketKey string,
	settings *models.ProjectSettings,
	jobErr error,
) {
	if settings.DisableErrorComments {
		return
	}

	comment := fmt.Sprintf("AI merge processing failed: %s", jobErr.Error())
	if err := p.tracker.AddComment(ticketKey, comment); err != nil {
		logger.Error("Failed to post error comment", zap.Error(err))
	}
}
