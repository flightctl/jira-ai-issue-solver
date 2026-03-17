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
// It locates the project configuration, resolves the component to a
// repository and profile, and maps status transitions for the work
// item's type.
func (r *ConfigResolver) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return nil, err
	}

	repoURL, profile, err := r.resolveComponent(workItem, pc)
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
		GitHubUsername:       ghUsername,
	}, nil
}

// LocateRepo returns the GitHub owner and repo for the work item.
func (r *ConfigResolver) LocateRepo(workItem models.WorkItem) (string, string, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return "", "", err
	}

	repoURL, _, err := r.resolveComponent(workItem, pc)
	if err != nil {
		return "", "", err
	}

	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return "", "", fmt.Errorf("parsing repo URL %q for %s: %w", repoURL, workItem.Key, err)
	}

	return owner, repo, nil
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

// resolveComponent finds the first work item component that has a
// mapping in the project's Components, then looks up the referenced
// profile. Returns the repo URL and profile. Returns an error if
// the work item has no components, none match, or the referenced
// profile does not exist.
//
// Matching is case-insensitive because viper lowercases YAML map keys
// internally, so configured keys like "FlightCtl" become "flightctl"
// in the loaded config regardless of the original YAML casing.
func (r *ConfigResolver) resolveComponent(workItem models.WorkItem, pc *models.ProjectConfig) (string, *models.Profile, error) {
	if len(workItem.Components) == 0 {
		return "", nil, fmt.Errorf("work item %s has no components; cannot resolve repository", workItem.Key)
	}

	var comp *models.ComponentConfig
	for _, component := range workItem.Components {
		// Try exact match first.
		if c, ok := pc.Components[component]; ok {
			comp = &c
			break
		}
		// Fall back to case-insensitive match (viper lowercases map keys).
		lower := strings.ToLower(component)
		for key, c := range pc.Components {
			if strings.ToLower(key) == lower {
				cc := c
				comp = &cc
				break
			}
		}
		if comp != nil {
			break
		}
	}

	if comp == nil {
		return "", nil, fmt.Errorf(
			"no component mapping found for %s; components %v do not match any configured mapping",
			workItem.Key, workItem.Components)
	}

	// Look up profile (case-insensitive).
	profile, ok := pc.Profiles[comp.Profile]
	if !ok {
		lower := strings.ToLower(comp.Profile)
		for key, p := range pc.Profiles {
			if strings.ToLower(key) == lower {
				profile = p
				ok = true
				break
			}
		}
		if !ok {
			return "", nil, fmt.Errorf("profile %q referenced by component does not exist in project config for %s", comp.Profile, workItem.Key)
		}
	}

	return comp.Repo, &profile, nil
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
