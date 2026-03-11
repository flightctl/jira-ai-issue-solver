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
// It locates the project configuration, resolves the repository from
// the work item's components, and maps status transitions for the
// work item's type.
func (r *ConfigResolver) ResolveProject(workItem models.WorkItem) (*models.ProjectSettings, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return nil, err
	}

	repoURL, err := r.matchComponentRepo(workItem, pc)
	if err != nil {
		return nil, err
	}

	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, fmt.Errorf("parsing repo URL %q for %s: %w", repoURL, workItem.Key, err)
	}

	transitions := pc.StatusTransitions.GetStatusTransitions(workItem.Type)

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
	}, nil
}

// LocateRepo returns the GitHub owner and repo for the work item.
func (r *ConfigResolver) LocateRepo(workItem models.WorkItem) (string, string, error) {
	pc, err := r.findProjectConfig(workItem)
	if err != nil {
		return "", "", err
	}

	repoURL, err := r.matchComponentRepo(workItem, pc)
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

// matchComponentRepo finds the first work item component that has a
// mapping in the project's ComponentToRepo. Returns an error if the
// work item has no components or none match.
func (r *ConfigResolver) matchComponentRepo(workItem models.WorkItem, pc *models.ProjectConfig) (string, error) {
	if len(workItem.Components) == 0 {
		return "", fmt.Errorf("work item %s has no components; cannot resolve repository", workItem.Key)
	}

	for _, component := range workItem.Components {
		if repoURL, ok := pc.ComponentToRepo[component]; ok {
			return repoURL, nil
		}
	}

	return "", fmt.Errorf(
		"no component-to-repo mapping found for %s; components %v do not match any configured mapping",
		workItem.Key, workItem.Components)
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
