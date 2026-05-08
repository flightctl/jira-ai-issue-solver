// Package projectresolver maps work items to project-specific settings
// using the bot's configuration. It bridges the gap between the
// application's multi-project configuration model and the per-work-item
// settings that pipelines need at execution time.
//
// ConfigResolver satisfies executor.ProjectResolver,
// recovery.ProjectResolver, and (via LocateRepo) scanner.RepoLocator.
// These interfaces are defined in each consumer package; this package
// does not import them.
package projectresolver

import (
	"fmt"
	"net/url"
	"strings"

	"jira-ai-issue-solver/models"
)

// ConfigResolver maps work items to project settings using the bot's
// configuration. It satisfies executor.ProjectResolver,
// recovery.ProjectResolver, and (via LocateRepo) scanner.RepoLocator.
type ConfigResolver struct {
	config *models.Config
}

// NewConfigResolver returns a ConfigResolver backed by the given
// configuration. Returns an error if config is nil.
func NewConfigResolver(config *models.Config) (*ConfigResolver, error) {
	if config == nil {
		return nil, fmt.Errorf("config must not be nil")
	}
	return &ConfigResolver{config: config}, nil
}

// ResolveProject returns project-specific settings for the work item.
// It locates the project configuration, resolves the component (or
// default workspace) to a workspace, and maps status transitions for
// the work item's type. Each repo in the workspace gets its own
// RepoSettings entry populated from its profile.
func (r *ConfigResolver) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return nil, err
	}

	wsName, err := r.findWorkspaceName(workItem, pc)
	if err != nil {
		return nil, err
	}

	ws, ok := lookupWorkspace(pc.Workspaces, wsName)
	if !ok {
		return nil, fmt.Errorf("workspace %q does not exist in project config for %s", wsName, workItem.Key)
	}
	if len(ws.Repos) == 0 {
		return nil, fmt.Errorf("workspace %q has no repos configured for %s", wsName, workItem.Key)
	}

	repos, err := r.buildRepoSettings(workItem, pc, ws)
	if err != nil {
		return nil, err
	}

	transitions := pc.StatusTransitions.GetStatusTransitions(workItem.Type)

	var ghUsername string
	if workItem.Assignee != nil {
		ghUsername = r.config.Jira.AssigneeToGitHubUsername[workItem.Assignee.Email]
	}

	return &models.ProjectSettings{
		Repos:                repos,
		RootRepoURL:          ws.RootRepo,
		InProgressStatus:     transitions.InProgress,
		InReviewStatus:       transitions.InReview,
		TodoStatus:           transitions.Todo,
		PRURLFieldName:       pc.GitPullRequestFieldName,
		DisableErrorComments: pc.DisableErrorComments,
		AIProvider:           r.config.AIProvider,
		Container:            ws.Container,
		GitHubUsername:       ghUsername,
	}, nil
}

// buildRepoSettings constructs a RepoSettings entry for each repo
// in the workspace, resolving the profile for each.
func (r *ConfigResolver) buildRepoSettings(workItem models.WorkItem, pc *models.ProjectConfig, ws models.WorkspaceConfig) ([]models.RepoSettings, error) {
	repos := make([]models.RepoSettings, 0, len(ws.Repos))
	for _, entry := range ws.Repos {
		owner, repo, err := parseRepoURL(entry.URL)
		if err != nil {
			return nil, fmt.Errorf("parsing repo URL %q for %s: %w", entry.URL, workItem.Key, err)
		}

		baseBranch := entry.TargetBranch
		if baseBranch == "" {
			baseBranch = "main"
		}

		rs := models.RepoSettings{
			Name:       entry.Name,
			Owner:      owner,
			Repo:       repo,
			CloneURL:   entry.URL,
			BaseBranch: baseBranch,
		}

		if entry.Profile != "" {
			profile, ok := lookupProfile(pc.Profiles, entry.Profile)
			if !ok {
				return nil, fmt.Errorf("profile %q referenced by repo %q does not exist in project config for %s", entry.Profile, entry.Name, workItem.Key)
			}
			rs.Container = profile.Container
			rs.Imports = profile.Imports
			if rs.Imports == nil {
				rs.Imports = []models.ImportConfig{}
			}
			rs.Instructions = profile.Instructions
			rs.NewTicketWorkflow = profile.NewTicketWorkflow
			rs.FeedbackWorkflow = profile.FeedbackWorkflow
		} else {
			rs.Imports = []models.ImportConfig{}
		}

		repos = append(repos, rs)
	}
	return repos, nil
}

// LocateRepo returns the GitHub owner and repo for the work item.
// For multi-repo workspaces, returns the first repo.
func (r *ConfigResolver) LocateRepo(workItem models.WorkItem) (string, string, error) {
	settings, err := r.ResolveProject(workItem)
	if err != nil {
		return "", "", err
	}
	if len(settings.Repos) == 0 {
		return "", "", fmt.Errorf("no repos configured for %s", workItem.Key)
	}
	return settings.Repos[0].Owner, settings.Repos[0].Repo, nil
}

// LocateRepos returns all repositories for the work item's resolved
// workspace. For single-repo workspaces this returns one entry; for
// multi-repo workspaces it returns all repos.
func (r *ConfigResolver) LocateRepos(workItem models.WorkItem) ([]models.RepoCoord, error) {
	settings, err := r.ResolveProject(workItem)
	if err != nil {
		return nil, err
	}
	results := make([]models.RepoCoord, len(settings.Repos))
	for i, rs := range settings.Repos {
		results[i] = models.RepoCoord{Owner: rs.Owner, Repo: rs.Repo}
	}
	return results, nil
}

// ForkOwner returns the GitHub username that owns the assignee's fork.
// Returns empty string if the work item has no assignee or the
// assignee is not in the assignee-to-GitHub-username mapping.
func (r *ConfigResolver) ForkOwner(workItem models.WorkItem) string {
	if workItem.Assignee == nil {
		return ""
	}
	return r.config.Jira.AssigneeToGitHubUsername[workItem.Assignee.Email]
}

// findProjectConfig returns the ProjectConfig for the work item's
// project key. Returns an error if no configuration can be found.
func (r *ConfigResolver) findProjectConfig(workItem models.WorkItem) (*models.ProjectConfig, error) {
	pc := r.config.GetProjectConfigForTicket(workItem.Key)
	if pc == nil {
		return nil, fmt.Errorf("no project configuration found for %s", workItem.Key)
	}
	return pc, nil
}

// findWorkspaceName returns the workspace name for a work item by
// checking component mappings first, then falling back to
// DefaultWorkspace.
func (r *ConfigResolver) findWorkspaceName(workItem models.WorkItem, pc *models.ProjectConfig) (string, error) {
	// Try component matching if the work item has components.
	if len(workItem.Components) > 0 {
		for _, component := range workItem.Components {
			// Exact match first.
			if comp, ok := pc.Components[component]; ok {
				return comp.Workspace, nil
			}
			// Case-insensitive fallback.
			lower := strings.ToLower(component)
			for key, comp := range pc.Components {
				if strings.ToLower(key) == lower {
					return comp.Workspace, nil
				}
			}
		}
	}

	// Fall back to default workspace.
	if pc.DefaultWorkspace != "" {
		return pc.DefaultWorkspace, nil
	}

	if len(workItem.Components) == 0 {
		return "", fmt.Errorf("work item %s has no components and no default_workspace is configured", workItem.Key)
	}
	return "", fmt.Errorf(
		"no component mapping found for %s; components %v do not match any configured mapping and no default_workspace is configured",
		workItem.Key, workItem.Components)
}

// lookupWorkspace finds a workspace by name with case-insensitive fallback.
func lookupWorkspace(workspaces map[string]models.WorkspaceConfig, name string) (models.WorkspaceConfig, bool) {
	if ws, ok := workspaces[name]; ok {
		return ws, true
	}
	lower := strings.ToLower(name)
	for key, ws := range workspaces {
		if strings.ToLower(key) == lower {
			return ws, true
		}
	}
	return models.WorkspaceConfig{}, false
}

// lookupProfile finds a profile by name with case-insensitive fallback.
func lookupProfile(profiles map[string]models.Profile, name string) (models.Profile, bool) {
	if p, ok := profiles[name]; ok {
		return p, true
	}
	lower := strings.ToLower(name)
	for key, p := range profiles {
		if strings.ToLower(key) == lower {
			return p, true
		}
	}
	return models.Profile{}, false
}

// parseRepoURL extracts the owner and repository name from a GitHub
// URL. Supports URLs with or without a .git suffix and with or without
// a trailing slash.
func parseRepoURL(rawURL string) (string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}

	// Trim trailing slashes and split on "/".
	trimmed := strings.TrimRight(parsed.Path, "/")
	segments := strings.Split(trimmed, "/")

	// Filter out empty segments (from leading slash).
	var parts []string
	for _, s := range segments {
		if s != "" {
			parts = append(parts, s)
		}
	}

	if len(parts) < 2 {
		return "", "", fmt.Errorf("URL path %q does not contain owner/repo", parsed.Path)
	}

	owner := parts[len(parts)-2]
	repo := strings.TrimSuffix(parts[len(parts)-1], ".git")

	return owner, repo, nil
}
