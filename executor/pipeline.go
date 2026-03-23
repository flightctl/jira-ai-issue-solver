package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
	"jira-ai-issue-solver/services"
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
	if err := p.prepareBranch(logger, wsPath, branchName, reused, settings); err != nil {
		return result, err
	}

	// --- Step 6: Load repo config ---
	repoCfg, err := repoconfig.Load(wsPath)
	if err != nil {
		logger.Warn("Failed to load repo config, using defaults", zap.Error(err))
		repoCfg = repoconfig.Default()
	}

	// --- Step 7: Clone imports ---
	mergedImports, err := p.cloneImports(logger, wsPath, settings, repoCfg)
	if err != nil {
		return result, err
	}

	// --- Step 8: Download attachments, write issue and task files ---
	downloaded, err := p.downloadAttachments(logger, *workItem, wsPath)
	if err != nil {
		return result, fmt.Errorf("download attachments: %w", err)
	}
	if err := p.taskWriter.WriteIssue(*workItem, wsPath, downloaded); err != nil {
		return result, fmt.Errorf("write issue file: %w", err)
	}
	if err := p.taskWriter.WriteNewTicketTask(*workItem, wsPath, settings.Instructions, settings.NewTicketWorkflow); err != nil {
		return result, fmt.Errorf("write task file: %w", err)
	}

	// --- Step 9: Determine AI provider ---
	provider := p.resolveProvider(settings)
	logger.Info("AI provider selected", zap.String("provider", provider))

	// --- Step 10: Build AI command ---
	sp := buildScriptParams(provider, p.cfg.DefaultGeminiModel, repoCfg)
	execCommand := buildExecCommand(sp)

	// --- Step 11: Resolve and start container ---
	ctr, err = p.startContainer(ctx, wsPath, job.TicketKey, provider, settings)
	if err != nil {
		return result, fmt.Errorf("start container: %w", err)
	}

	// --- Step 11a: Run import install commands inside container ---
	if err := p.runImportInstalls(ctx, logger, ctr, mergedImports); err != nil {
		return result, fmt.Errorf("import install: %w", err)
	}

	// --- Step 11b: Strip remote auth before AI execution ---
	// Prevent the AI from pushing directly to the remote.
	if err := p.git.StripRemoteAuth(wsPath); err != nil {
		return result, fmt.Errorf("strip remote auth: %w", err)
	}
	authStripped := true
	defer func() {
		if authStripped {
			if restoreErr := p.git.RestoreRemoteAuth(wsPath, settings.Owner, settings.Repo); restoreErr != nil {
				logger.Warn("Failed to restore remote auth", zap.Error(restoreErr))
			}
		}
	}()

	// --- Step 12: Execute AI agent ---
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
			// Parent context cancelled (shutdown).
			return result, fmt.Errorf("job cancelled: %w", ctx.Err())
		}
		logger.Warn("AI agent exec failed", zap.Error(execErr))
	}

	// Read session metadata (may be absent on abnormal exit).
	session := readSessionOutput(wsPath)

	logger.Info("AI session completed",
		zap.Int("exit_code", exitCode),
		zap.Float64("cost_usd", session.CostUSD),
		zap.Any("validation_passed", session.ValidationPassed),
		zap.String("summary", session.Summary))
	result.CostUSD = session.CostUSD

	// --- Step 12a: Restore remote auth ---
	// Must happen before SyncWithRemote which needs fetch access.
	if err := p.git.RestoreRemoteAuth(wsPath, settings.Owner, settings.Repo); err != nil {
		return result, fmt.Errorf("restore remote auth: %w", err)
	}
	authStripped = false

	// Exec runtime error (not just non-zero exit) is fatal.
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
	importExcludes := collectExcludes(mergedImports)
	commitMsg := fmt.Sprintf("%s: %s", job.TicketKey, workItem.Summary)
	_, err = p.git.CommitChanges(
		settings.Owner, settings.Repo, branchName,
		commitMsg, wsPath, workItem.Assignee, importExcludes,
	)
	if errors.Is(err, services.ErrNoChanges) {
		return result, fmt.Errorf("AI produced no committable changes (exit code: %d)", exitCode)
	}
	if err != nil {
		return result, fmt.Errorf("commit changes: %w", err)
	}

	// --- Step 15: Post-commit sync ---
	if err := p.git.SyncWithRemote(wsPath, branchName, importExcludes); err != nil {
		return result, fmt.Errorf("sync with remote: %w", err)
	}

	// --- Step 16: Create PR ---
	draft := shouldCreateDraft(session, exitCode, repoCfg.PR.Draft)
	if draft {
		logger.Info("Creating draft PR",
			zap.Int("exit_code", exitCode),
			zap.Any("validation_passed", session.ValidationPassed),
			zap.Bool("repo_config_draft", repoCfg.PR.Draft))
	}
	aiPR := readPRDescription(wsPath)
	prTitle, prBody := buildPRContent(workItem, job.TicketKey, repoCfg.PR.TitlePrefix, aiPR)

	pr, err := p.git.CreatePR(models.PRParams{
		Owner:     settings.Owner,
		Repo:      settings.Repo,
		Title:     prTitle,
		Body:      prBody,
		Head:      branchName,
		Base:      settings.BaseBranch,
		Draft:     draft,
		Labels:    repoCfg.PR.Labels,
		Assignees: assigneesFromSettings(settings),
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

	// --- Step 17: Update ticket ---
	p.setPRURL(logger, job.TicketKey, settings, pr.URL)

	if !draft {
		if err := p.tracker.TransitionStatus(job.TicketKey, settings.InReviewStatus); err != nil {
			logger.Warn("Failed to transition to in-review", zap.Error(err))
		}
	}

	return result, nil
}

// startContainer resolves configuration and starts a container.
func (p *Pipeline) startContainer(
	ctx context.Context,
	wsPath, ticketKey, provider string,
	settings *models.ProjectSettings,
) (*container.Container, error) {
	profileOverride := toSettingsOverride(settings)
	containerCfg, err := p.containers.ResolveConfig(wsPath, profileOverride)
	if err != nil {
		return nil, fmt.Errorf("resolve container config: %w", err)
	}

	env := p.buildContainerEnv(provider)
	return p.containers.Start(ctx, containerCfg, wsPath, ticketKey, env)
}

// toSettingsOverride converts profile container settings to the
// container package's override type. Returns nil if no container
// settings are configured (zero-value ContainerSettings).
func toSettingsOverride(settings *models.ProjectSettings) *container.SettingsOverride {
	cs := settings.Container
	if cs.Image == "" && cs.ResourceLimits.Memory == "" && cs.ResourceLimits.CPUs == "" &&
		len(cs.Env) == 0 && len(cs.Tmpfs) == 0 && len(cs.ExtraMounts) == 0 {
		return nil
	}
	mounts := make([]container.Mount, len(cs.ExtraMounts))
	for i, m := range cs.ExtraMounts {
		mounts[i] = container.Mount{Source: m.Source, Target: m.Target, Options: m.Options}
	}
	return &container.SettingsOverride{
		Image: cs.Image,
		Limits: container.ResourceLimits{
			Memory: cs.ResourceLimits.Memory,
			CPUs:   cs.ResourceLimits.CPUs,
		},
		Env:         cs.Env,
		Tmpfs:       cs.Tmpfs,
		ExtraMounts: mounts,
	}
}

// cloneImports merges project-level and repo-level imports (repo-level
// wins on path conflicts) and clones each into the workspace. Existing
// directories are skipped (workspace reuse). Import directories are
// excluded from commits at the GitHub API level (see isBotArtifact in
// services/github.go), and the nested .git directory prevents git from
// tracking their contents locally.
// Returns the merged import list for use by runImportInstalls.
func (p *Pipeline) cloneImports(
	logger *zap.Logger,
	wsPath string,
	settings *models.ProjectSettings,
	repoCfg *repoconfig.Config,
) ([]importEntry, error) {
	merged := mergeImports(settings, repoCfg)
	if len(merged) == 0 {
		return merged, nil
	}

	for _, imp := range merged {
		destDir := filepath.Join(wsPath, imp.Path)

		// Skip if already cloned (workspace reuse).
		if _, err := os.Stat(destDir); err == nil {
			logger.Debug("Import already exists, skipping",
				zap.String("path", imp.Path))
			continue
		}

		logger.Info("Cloning import",
			zap.String("repo", imp.Repo),
			zap.String("path", imp.Path),
			zap.String("ref", imp.Ref))

		if err := p.git.CloneImport(imp.Repo, destDir, imp.Ref); err != nil {
			return nil, fmt.Errorf("clone import %s into %s: %w", imp.Repo, imp.Path, err)
		}
	}

	return merged, nil
}

// runImportInstalls executes install commands for imports that declare
// one. Commands run inside the container from /workspace, with access
// to the container's full toolchain. This is plumbing — the bot sets
// up the environment so the AI finds it ready to use.
func (p *Pipeline) runImportInstalls(
	ctx context.Context,
	logger *zap.Logger,
	ctr *container.Container,
	imports []importEntry,
) error {
	for _, imp := range imports {
		if imp.Install == "" {
			continue
		}

		logger.Info("Running import install command",
			zap.String("path", imp.Path),
			zap.String("command", imp.Install))

		cmd := []string{
			"sh", "-c",
			"cd /workspace && " + imp.Install,
		}
		output, exitCode, err := p.containers.Exec(ctx, ctr, cmd)
		if err != nil {
			return fmt.Errorf("install command for import %s: %w", imp.Path, err)
		}
		if exitCode != 0 {
			return fmt.Errorf(
				"install command for import %s exited with code %d: %s",
				imp.Path, exitCode, output)
		}

		logger.Info("Import install completed",
			zap.String("path", imp.Path))
	}
	return nil
}

// importEntry is the unified type used during import merging.
type importEntry struct {
	Repo     string
	Path     string
	Ref      string
	Install  string
	Excludes []string
}

// mergeImports combines project-level and repo-level imports. When both
// sources declare the same destination path, the repo-level import wins
// (teams own their environment). Paths are normalized to avoid
// duplicates from trailing slashes.
func mergeImports(
	settings *models.ProjectSettings,
	repoCfg *repoconfig.Config,
) []importEntry {
	byPath := make(map[string]importEntry)

	// Project-level imports go in first.
	for _, imp := range settings.Imports {
		p := filepath.Clean(imp.Path)
		byPath[p] = importEntry{Repo: imp.Repo, Path: p, Ref: imp.Ref, Install: imp.Install, Excludes: imp.Excludes}
	}

	// Repo-level imports override on path conflict.
	for _, imp := range repoCfg.Imports {
		p := filepath.Clean(imp.Path)
		byPath[p] = importEntry{Repo: imp.Repo, Path: p, Ref: imp.Ref, Install: imp.Install, Excludes: imp.Excludes}
	}

	// Deterministic order: sort by path.
	result := make([]importEntry, 0, len(byPath))
	for _, e := range byPath {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result
}

// collectExcludes gathers all exclude paths declared by imports into
// a single list. These paths are directories that import tools may
// create as output and should be excluded from commits.
func collectExcludes(imports []importEntry) []string {
	var excludes []string
	for _, imp := range imports {
		excludes = append(excludes, imp.Excludes...)
	}
	return excludes
}

// maxAttachmentSize is the maximum size (in bytes) of a single Jira
// attachment that will be downloaded. Larger files are skipped.
const maxAttachmentSize = 1 << 20 // 1 MiB

// downloadAttachments fetches qualifying Jira attachments into the
// workspace's .ai-bot/attachments/ directory and returns the list of
// filenames that were written. Attachments exceeding maxAttachmentSize
// are skipped with a log message; download failures are logged and
// skipped (non-fatal).
func (p *Pipeline) downloadAttachments(
	logger *zap.Logger,
	workItem models.WorkItem,
	wsPath string,
) ([]string, error) {
	if len(workItem.Attachments) == 0 {
		return nil, nil
	}

	destDir := filepath.Join(wsPath, taskfile.AttachmentsDirPath)
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return nil, fmt.Errorf("create attachments dir: %w", err)
	}

	var downloaded []string
	for _, att := range workItem.Attachments {
		// Sanitize filename early to check for existing file.
		safeName := filepath.Base(att.Filename)
		destPath := filepath.Join(destDir, safeName)

		// Skip attachments already on disk (workspace persists across sessions).
		if _, err := os.Stat(destPath); err == nil {
			downloaded = append(downloaded, safeName)
			logger.Debug("Attachment already exists, skipping download",
				zap.String("filename", safeName))
			continue
		}

		if att.Size > maxAttachmentSize {
			logger.Info("Skipping large attachment",
				zap.String("filename", att.Filename),
				zap.Int64("size_bytes", att.Size),
				zap.Int64("max_bytes", maxAttachmentSize))
			continue
		}

		data, err := p.tracker.DownloadAttachment(att.URL)
		if err != nil {
			logger.Warn("Failed to download attachment",
				zap.String("filename", att.Filename),
				zap.Error(err))
			continue
		}

		if err := os.WriteFile(destPath, data, 0o644); err != nil { // #nosec G306
			return nil, fmt.Errorf("write attachment %s: %w", safeName, err)
		}

		downloaded = append(downloaded, safeName)
		logger.Info("Downloaded attachment",
			zap.String("filename", safeName),
			zap.Int64("size_bytes", att.Size))
	}

	return downloaded, nil
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

// prepareBranch sets up the working branch for a new-ticket pipeline run.
// When the workspace is reused and the remote branch still exists, it
// switches to it. When the remote branch was deleted (e.g., user closed
// the PR), it recreates the branch from the target branch so the AI
// starts from a clean slate. For fresh workspaces it creates a new branch.
func (p *Pipeline) prepareBranch(
	logger *zap.Logger,
	wsPath, branchName string,
	reused bool,
	settings *models.ProjectSettings,
) error {
	if !reused {
		if err := p.git.CreateBranch(wsPath, branchName); err != nil {
			return fmt.Errorf("create branch: %w", err)
		}
		return nil
	}

	remoteExists, err := p.git.RemoteBranchExists(
		settings.Owner, settings.Repo, branchName)
	if err != nil {
		return fmt.Errorf("check remote branch: %w", err)
	}

	if remoteExists {
		if err := p.git.SwitchBranch(wsPath, branchName); err != nil {
			return fmt.Errorf("switch to branch: %w", err)
		}
		return nil
	}

	// Remote branch was deleted — start fresh from the target branch.
	logger.Info("Remote branch deleted, recreating from target branch",
		zap.String("branch", branchName))
	if err := p.git.CreateBranch(wsPath, branchName); err != nil {
		return fmt.Errorf("recreate branch: %w", err)
	}
	return nil
}

// assigneesFromSettings returns the PR assignee list from resolved
// project settings. Returns nil when no GitHub username is configured.
func assigneesFromSettings(settings *models.ProjectSettings) []string {
	if settings.GitHubUsername == "" {
		return nil
	}
	return []string{settings.GitHubUsername}
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
func buildPRContent(workItem *models.WorkItem, ticketKey, titlePrefix string, aiPR *PRDescription) (title, body string) {
	if workItem.HasSecurityLevel() {
		// Security-level tickets always use redacted content —
		// the AI might leak vulnerability details in its PR description.
		title = fmt.Sprintf("%s: Security fix", ticketKey)
		body = fmt.Sprintf("Security fix for %s.\n\nDetails redacted due to security level.", ticketKey)
	} else if aiPR != nil && aiPR.Title != "" {
		// AI-generated PR description takes precedence over Jira-derived.
		// Strip the ticket key prefix if the AI already included it.
		aiTitle := aiPR.Title
		if cut, ok := strings.CutPrefix(aiTitle, ticketKey+": "); ok {
			aiTitle = cut
		} else if cut, ok := strings.CutPrefix(aiTitle, ticketKey+" "); ok {
			aiTitle = cut
		}
		title = fmt.Sprintf("%s: %s", ticketKey, aiTitle)
		body = fmt.Sprintf("Resolves %s", ticketKey)
		if aiPR.Body != "" {
			body += "\n\n" + aiPR.Body
		}
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
// from the repo config, falling back to the pipeline's default model.
func buildScriptParams(provider, defaultGeminiModel string, repoCfg *repoconfig.Config) scriptParams {
	params := scriptParams{Provider: provider}

	// Apply bot-level default first.
	if provider == "gemini" {
		params.Model = defaultGeminiModel
	}

	if repoCfg == nil {
		return params
	}

	// Repo-level overrides.
	if repoCfg.AI.Claude != nil {
		params.AllowedTools = repoCfg.AI.Claude.AllowedTools
	}
	if repoCfg.AI.Gemini != nil && repoCfg.AI.Gemini.Model != "" {
		params.Model = repoCfg.AI.Gemini.Model
	}
	return params
}
