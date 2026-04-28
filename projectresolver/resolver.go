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
// the work item's type.
//
// Temporary bridge: extracts the first repo from the workspace and
// populates the old-shape ProjectSettings. Task 2 will reshape
// ProjectSettings to carry all repos.
func (r *ConfigResolver) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return nil, err
	}

	repoURL, profile, err := r.resolveWorkspace(workItem, pc)
	if err != nil {
		return nil, err
	}

	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, fmt.Errorf("parsing repo URL %q for %s: %w", repoURL, workItem.Key, err)
	}

	transitions := pc.StatusTransitions.GetStatusTransitions(workItem.Type)

	imports := profile.Imports
	if imports == nil {
		imports = []models.ImportConfig{}
	}

	// Resolve the assignee's GitHub username from the config mapping.
	var ghUsername string
	if workItem.Assignee != nil {
		ghUsername = r.config.Jira.AssigneeToGitHubUsername[workItem.Assignee.Email]
	}

	return &models.ProjectSettings{
		Owner:                owner,
		Repo:                 repo,
		CloneURL:             repoURL,
		BaseBranch:           r.config.GitHub.TargetBranch,
		InProgressStatus:     transitions.InProgress,
		InReviewStatus:       transitions.InReview,
		TodoStatus:           transitions.Todo,
		PRURLFieldName:       pc.GitPullRequestFieldName,
		DisableErrorComments: pc.DisableErrorComments,
		AIProvider:           r.config.AIProvider,
		Container:            profile.Container,
		Imports:              imports,
		Instructions:         profile.Instructions,
		NewTicketWorkflow:    profile.NewTicketWorkflow,
		FeedbackWorkflow:     profile.FeedbackWorkflow,
		GitHubUsername:       ghUsername,
	}, nil
}

// LocateRepo returns the GitHub owner and repo for the work item.
// For multi-repo workspaces, returns the first repo (temporary;
// Task 2 introduces LocateRepos).
func (r *ConfigResolver) LocateRepo(workItem models.WorkItem) (string, string, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return "", "", err
	}

	repoURL, _, err := r.resolveWorkspace(workItem, pc)
	if err != nil {
		return "", "", err
	}

	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return "", "", fmt.Errorf("parsing repo URL %q for %s: %w", repoURL, workItem.Key, err)
	}

	return owner, repo, nil
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

// resolveWorkspace finds the workspace for a work item by matching
// its Jira components against the project's component-to-workspace
// mappings, falling back to DefaultWorkspace when no component
// matches. Returns the first repo's URL and its resolved profile.
//
// Component matching is case-insensitive because viper lowercases
// YAML map keys internally.
func (r *ConfigResolver) resolveWorkspace(workItem models.WorkItem, pc *models.ProjectConfig) (string, *models.Profile, error) {
	wsName, err := r.findWorkspaceName(workItem, pc)
	if err != nil {
		return "", nil, err
	}

	ws, ok := lookupWorkspace(pc.Workspaces, wsName)
	if !ok {
		return "", nil, fmt.Errorf("workspace %q does not exist in project config for %s", wsName, workItem.Key)
	}

	// Use first repo (temporary bridge until Task 2 reshapes ProjectSettings).
	repo := ws.Repos[0]

	profile, ok := lookupProfile(pc.Profiles, repo.Profile)
	if !ok {
		return "", nil, fmt.Errorf("profile %q referenced by repo %q does not exist in project config for %s", repo.Profile, repo.Name, workItem.Key)
	}

	return repo.URL, &profile, nil
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
