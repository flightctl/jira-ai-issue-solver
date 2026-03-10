package executor

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
	"jira-ai-issue-solver/taskfile"
	"jira-ai-issue-solver/tracker"
	"jira-ai-issue-solver/workspace"
)

// Compile-time check that Pipeline implements Executor.
var _ Executor = (*Pipeline)(nil)

// Pipeline implements the job execution pipelines for new tickets
// and PR feedback. It coordinates workspace preparation, container
// lifecycle, AI execution, committing, and PR management.
type Pipeline struct {
	tracker    tracker.IssueTracker
	git        GitService
	containers container.Manager
	workspaces workspace.Manager
	taskWriter taskfile.Writer
	projects   ProjectResolver
	cfg        Config
	logger     *zap.Logger
}

// NewPipeline creates a Pipeline with the given dependencies.
// Returns an error if any required parameter is invalid.
func NewPipeline(
	cfg Config,
	issueTracker tracker.IssueTracker,
	git GitService,
	containers container.Manager,
	workspaces workspace.Manager,
	taskWriter taskfile.Writer,
	projects ProjectResolver,
	logger *zap.Logger,
) (*Pipeline, error) {
	if cfg.BotUsername == "" {
		return nil, errors.New("bot username must not be empty")
	}
	if cfg.DefaultProvider == "" {
		return nil, errors.New("default AI provider must not be empty")
	}
	if cfg.SessionTimeout < 0 {
		return nil, errors.New("session timeout must not be negative")
	}
	if issueTracker == nil {
		return nil, errors.New("issue tracker must not be nil")
	}
	if git == nil {
		return nil, errors.New("git service must not be nil")
	}
	if containers == nil {
		return nil, errors.New("container manager must not be nil")
	}
	if workspaces == nil {
		return nil, errors.New("workspace manager must not be nil")
	}
	if taskWriter == nil {
		return nil, errors.New("task file writer must not be nil")
	}
	if projects == nil {
		return nil, errors.New("project resolver must not be nil")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	return &Pipeline{
		tracker:    issueTracker,
		git:        git,
		containers: containers,
		workspaces: workspaces,
		taskWriter: taskWriter,
		projects:   projects,
		cfg:        cfg,
		logger:     logger,
	}, nil
}

// Execute dispatches a job by type. Matches [jobmanager.ExecuteFunc].
func (p *Pipeline) Execute(ctx context.Context, job *jobmanager.Job) (jobmanager.JobResult, error) {
	switch job.Type {
	case jobmanager.JobTypeNewTicket:
		return p.executeNewTicket(ctx, job)
	case jobmanager.JobTypeFeedback:
		return p.executeFeedback(ctx, job)
	default:
		return jobmanager.JobResult{}, fmt.Errorf("unknown job type: %s", job.Type)
	}
}

func (p *Pipeline) executeNewTicket(ctx context.Context, job *jobmanager.Job) (result jobmanager.JobResult, retErr error) {
	logger := p.logger.With(
		zap.String("ticket", job.TicketKey),
		zap.String("job_id", job.ID),
		zap.Int("attempt", job.AttemptNum),
	)
	logger.Info("Starting new ticket pipeline")

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

	// --- Step 3: Transition to in-progress ---
	if err := p.tracker.TransitionStatus(job.TicketKey, settings.InProgressStatus); err != nil {
		return result, fmt.Errorf("transition to in-progress: %w", err)
	}
	statusTransitioned := true

	// Track container for cleanup.
	var ctr *container.Container

	defer func() {
		// Always stop container if started.
		if ctr != nil {
			if stopErr := p.containers.Stop(context.Background(), ctr); stopErr != nil {
				logger.Warn("Failed to stop container", zap.Error(stopErr))
			}
		}
		// On failure: revert status and optionally post error comment.
		if retErr != nil && statusTransitioned {
			p.handleFailure(logger, job.TicketKey, settings, retErr)
		}
	}()

	// --- Step 4: Prepare workspace ---
	wsPath, reused, err := p.workspaces.FindOrCreate(job.TicketKey, settings.CloneURL)
	if err != nil {
		return result, fmt.Errorf("prepare workspace: %w", err)
	}
	logger.Info("Workspace ready",
		zap.String("path", wsPath),
		zap.Bool("reused", reused))

	// --- Step 5: Create or switch to branch ---
	branchName := fmt.Sprintf("%s/%s", p.cfg.BotUsername, job.TicketKey)
	if reused {
		if err := p.git.SwitchBranch(wsPath, branchName); err != nil {
			return result, fmt.Errorf("switch to branch: %w", err)
		}
	} else {
		if err := p.git.CreateBranch(wsPath, branchName); err != nil {
			return result, fmt.Errorf("create branch: %w", err)
		}
	}

	// --- Step 6: Write task file ---
	if err := p.taskWriter.WriteNewTicketTask(*workItem, wsPath); err != nil {
		return result, fmt.Errorf("write task file: %w", err)
	}

	// --- Step 7: Determine AI provider ---
	provider := p.resolveProvider(settings)

	// --- Step 8: Load repo config ---
	repoCfg, err := repoconfig.Load(wsPath)
	if err != nil {
		logger.Warn("Failed to load repo config, using defaults", zap.Error(err))
		repoCfg = repoconfig.Default()
	}

	// --- Step 9: Write wrapper script ---
	sp := buildScriptParams(provider, repoCfg)
	if err := writeRunScript(wsPath, sp); err != nil {
		return result, fmt.Errorf("write run script: %w", err)
	}

	// --- Step 10: Resolve and start container ---
	ctr, err = p.startContainer(ctx, logger, wsPath, provider)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	// --- Step 11: Execute AI agent ---
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
			// Parent context cancelled (shutdown).
			return result, fmt.Errorf("job cancelled: %w", ctx.Err())
		}
		logger.Warn("AI agent exec failed", zap.Error(execErr))
	}

	// Read session metadata (may be absent on abnormal exit).
	session := readSessionOutput(wsPath)
	result.CostUSD = session.CostUSD

	// Exec runtime error (not just non-zero exit) is fatal.
	if execErr != nil {
		if execCtx.Err() != nil {
			return result, fmt.Errorf("session timeout exceeded: %w", execErr)
		}
		return result, fmt.Errorf("AI session failed: %w", execErr)
	}

	// --- Step 12: Check for changes ---
	hasChanges, err := p.git.HasChanges(wsPath)
	if err != nil {
		return result, fmt.Errorf("check changes: %w", err)
	}
	if !hasChanges {
		return result, fmt.Errorf("AI produced no changes (exit code: %d)", exitCode)
	}

	// --- Step 13: Commit via GitHub API ---
	commitMsg := fmt.Sprintf("%s: %s", job.TicketKey, workItem.Summary)
	if _, err := p.git.CommitChanges(
		settings.Owner, settings.Repo, branchName,
		commitMsg, wsPath, workItem.Assignee,
	); err != nil {
		return result, fmt.Errorf("commit changes: %w", err)
	}

	// --- Step 14: Post-commit sync ---
	if err := p.git.SyncWithRemote(wsPath, branchName); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 15: Create PR ---
	draft := shouldCreateDraft(session, exitCode, repoCfg.PR.Draft)
	prTitle, prBody := buildPRContent(workItem, job.TicketKey, repoCfg.PR.TitlePrefix)

	pr, err := p.git.CreatePR(models.PRParams{
		Owner:  settings.Owner,
		Repo:   settings.Repo,
		Title:  prTitle,
		Body:   prBody,
		Head:   branchName,
		Base:   settings.BaseBranch,
		Draft:  draft,
		Labels: repoCfg.PR.Labels,
	})
	if err != nil {
		return result, fmt.Errorf("create PR: %w", err)
	}

	result.PRURL = pr.URL
	result.PRNumber = pr.Number
	result.Draft = draft
	result.ValidationPassed = !draft

	logger.Info("PR created",
		zap.String("url", pr.URL),
		zap.Int("number", pr.Number),
		zap.Bool("draft", draft))

	// --- Step 16: Update ticket ---
	p.setPRURL(logger, job.TicketKey, settings, pr.URL)

	if !draft {
		if err := p.tracker.TransitionStatus(job.TicketKey, settings.InReviewStatus); err != nil {
			logger.Warn("Failed to transition to in-review", zap.Error(err))
		}
	}

	return result, nil
}

// startContainer resolves configuration, starts a container, and
// falls back to the fallback image if the primary start fails.
func (p *Pipeline) startContainer(
	ctx context.Context,
	logger *zap.Logger,
	wsPath, provider string,
) (*container.Container, error) {
	containerCfg, err := p.containers.ResolveConfig(wsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve container config: %w", err)
	}

	env := p.buildContainerEnv(provider)

	ctr, err := p.containers.Start(ctx, containerCfg, wsPath, env)
	if err == nil {
		return ctr, nil
	}

	// Primary image failed. Try fallback if configured.
	if p.cfg.FallbackImage == "" {
		return nil, err
	}

	logger.Warn("Container start failed, trying fallback image",
		zap.String("original_image", containerCfg.Image),
		zap.String("fallback_image", p.cfg.FallbackImage),
		zap.Error(err))

	fallbackCfg := &container.Config{Image: p.cfg.FallbackImage}
	return p.containers.Start(ctx, fallbackCfg, wsPath, env)
}

func (p *Pipeline) resolveProvider(settings *models.ProjectSettings) string {
	if settings.AIProvider != "" {
		return settings.AIProvider
	}
	return p.cfg.DefaultProvider
}

func (p *Pipeline) buildContainerEnv(provider string) map[string]string {
	env := map[string]string{
		"AI_PROVIDER": provider,
		"PROJECT_DIR": "/workspace",
	}

	switch provider {
	case "claude":
		if key, ok := p.cfg.AIAPIKeys["claude"]; ok {
			env["ANTHROPIC_API_KEY"] = key
		}
	case "gemini":
		if key, ok := p.cfg.AIAPIKeys["gemini"]; ok {
			env["GEMINI_API_KEY"] = key
		}
	}

	return env
}

// setPRURL stores the PR URL on the ticket via either a custom field
// or a structured comment.
func (p *Pipeline) setPRURL(logger *zap.Logger, ticketKey string, settings *models.ProjectSettings, prURL string) {
	if settings.PRURLFieldName != "" {
		if err := p.tracker.SetFieldValue(ticketKey, settings.PRURLFieldName, prURL); err != nil {
			logger.Warn("Failed to set PR URL field", zap.Error(err))
		}
	} else {
		comment := fmt.Sprintf("[AI-BOT-PR] %s", prURL)
		if err := p.tracker.AddComment(ticketKey, comment); err != nil {
			logger.Warn("Failed to add PR URL comment", zap.Error(err))
		}
	}
}

// handleFailure reverts the ticket status and optionally posts an
// error comment.
func (p *Pipeline) handleFailure(logger *zap.Logger, ticketKey string, settings *models.ProjectSettings, jobErr error) {
	if err := p.tracker.TransitionStatus(ticketKey, settings.TodoStatus); err != nil {
		logger.Error("Failed to revert ticket status",
			zap.String("target_status", settings.TodoStatus),
			zap.Error(err))
	}

	if settings.DisableErrorComments {
		return
	}

	comment := fmt.Sprintf("AI processing failed: %s", jobErr.Error())
	if err := p.tracker.AddComment(ticketKey, comment); err != nil {
		logger.Error("Failed to post error comment", zap.Error(err))
	}
}

// shouldCreateDraft determines whether the PR should be created as a
// draft based on session output, exit code, and repo config.
func shouldCreateDraft(session SessionOutput, exitCode int, repoDraft bool) bool {
	if repoDraft {
		return true
	}
	if session.ValidationPassed != nil && !*session.ValidationPassed {
		return true
	}
	if exitCode != 0 {
		return true
	}
	return false
}

// buildPRContent generates the PR title and body from the work item.
// Security-level tickets get redacted content.
func buildPRContent(workItem *models.WorkItem, ticketKey, titlePrefix string) (title, body string) {
	if workItem.HasSecurityLevel() {
		title = fmt.Sprintf("%s: Security fix", ticketKey)
		body = fmt.Sprintf("Security fix for %s.\n\nDetails redacted due to security level.", ticketKey)
	} else {
		title = fmt.Sprintf("%s: %s", ticketKey, workItem.Summary)
		body = fmt.Sprintf("Resolves %s\n\n## Summary\n%s", ticketKey, workItem.Summary)
		if workItem.Description != "" {
			body += fmt.Sprintf("\n\n## Description\n%s", workItem.Description)
		}
	}

	if titlePrefix != "" {
		title = titlePrefix + " " + title
	}

	return title, body
}

// buildScriptParams extracts provider-specific script configuration
// from the repo config.
func buildScriptParams(provider string, repoCfg *repoconfig.Config) scriptParams {
	params := scriptParams{Provider: provider}
	if repoCfg == nil {
		return params
	}
	if repoCfg.AI.Claude != nil {
		params.AllowedTools = repoCfg.AI.Claude.AllowedTools
	}
	if repoCfg.AI.Gemini != nil {
		params.Model = repoCfg.AI.Gemini.Model
	}
	return params
}
