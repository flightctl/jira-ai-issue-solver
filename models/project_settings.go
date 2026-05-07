package models

// RepoSettings carries per-repo profile data alongside the repo
// coordinates. The executor uses these as fallbacks when .ai-bot/
// files don't exist in the repo.
type RepoSettings struct {
	// Name is the short identifier used as the subdirectory name in
	// multi-repo workspaces (e.g., "fulfillment-service").
	Name string

	// Owner is the GitHub repository owner (e.g., "my-org").
	Owner string

	// Repo is the GitHub repository name (e.g., "backend").
	Repo string

	// CloneURL is the full clone URL for the repository.
	CloneURL string

	// Container holds per-repo container settings from the profile.
	Container ContainerSettings

	// Imports declares auxiliary repositories from the profile to
	// clone into the workspace before AI execution. Merged with
	// repo-level imports from .ai-bot/config.yaml.
	Imports []ImportConfig

	// Instructions provides per-repo AI instructions from the
	// profile. Used as a fallback when the repo does not have
	// .ai-bot/instructions.md.
	Instructions string

	// NewTicketWorkflow provides per-repo workflow instructions from
	// the profile. Used as a fallback when the repo does not have
	// .ai-bot/new-ticket-workflow.md.
	NewTicketWorkflow string

	// FeedbackWorkflow provides per-repo workflow instructions from
	// the profile. Used as a fallback when the repo does not have
	// .ai-bot/feedback-workflow.md.
	FeedbackWorkflow string

	// BaseBranch is the target branch for pull requests (e.g.,
	// "main", "master"). Defaults to "main".
	BaseBranch string
}

// ProjectSettings contains the resolved per-project settings needed
// to execute or recover a job for a specific work item. The concrete
// resolver (built during application startup) maps work items to these
// settings based on the bot's configuration.
type ProjectSettings struct {
	// Repos holds per-repo settings for all repositories in the
	// resolved workspace. Single-repo workspaces have exactly one
	// entry; multi-repo workspaces have multiple.
	Repos []RepoSettings

	// InProgressStatus is the tracker status name for "in progress".
	InProgressStatus string

	// InReviewStatus is the tracker status name for "in review".
	InReviewStatus string

	// TodoStatus is the tracker status name to revert to on failure.
	TodoStatus string

	// PRURLFieldName is the custom field for storing the PR URL.
	// Empty means PR URL is posted as a structured comment instead.
	PRURLFieldName string

	// DisableErrorComments prevents posting error details as tracker
	// comments on job failure. Errors are still logged.
	DisableErrorComments bool

	// AIProvider overrides the default AI provider for this project.
	// Empty means use the pipeline's default provider.
	AIProvider string

	// Container holds workspace-level container settings. For
	// multi-repo workspaces this is the "fat container" with all
	// toolchains. For single-repo workspaces this may be zero-value,
	// in which case the repo's profile container is used instead.
	Container ContainerSettings

	// GitHubUsername is the GitHub username of the ticket assignee,
	// resolved from the assignee-to-GitHub-username config mapping.
	// Empty when the assignee has no mapping or the ticket is unassigned.
	// When set, fork-based workflow is used: commits go to the
	// assignee's fork (GitHubUsername/Repo) and PRs are created as
	// cross-repo PRs targeting the upstream Owner/Repo.
	GitHubUsername string
}

// IsMultiRepo returns true when the workspace contains more than
// one repository.
func (s *ProjectSettings) IsMultiRepo() bool {
	return len(s.Repos) > 1
}

// ResolvedContainer returns the effective container settings.
// Workspace-level container takes precedence; falls back to the
// first repo's profile container for single-repo workspaces.
func (s *ProjectSettings) ResolvedContainer() ContainerSettings {
	if s.Container.Image != "" {
		return s.Container
	}
	if len(s.Repos) > 0 {
		return s.Repos[0].Container
	}
	return ContainerSettings{}
}

// ForkOwner returns the GitHub owner of the assignee's fork.
// Returns empty string if no fork is configured (no assignee mapping).
func (s *ProjectSettings) ForkOwner() string {
	return s.GitHubUsername
}

// CommitOwner returns the repo owner to target for commits and
// branch operations. When a fork owner is configured, commits go
// to the fork; otherwise they go directly to the upstream repo.
// For multi-repo workspaces use CommitOwnerFor instead; this
// method uses Repos[0] as a convenience for single-repo callers.
func (s *ProjectSettings) CommitOwner() string {
	if s.GitHubUsername != "" {
		return s.GitHubUsername
	}
	if len(s.Repos) > 0 {
		return s.Repos[0].Owner
	}
	return ""
}

// CommitOwnerFor returns the owner to target for commits on the
// given repo. Fork mode overrides the repo's owner with the
// assignee's GitHub username.
func (s *ProjectSettings) CommitOwnerFor(repo RepoSettings) string {
	if s.GitHubUsername != "" {
		return s.GitHubUsername
	}
	return repo.Owner
}

// PRHead returns the head ref for PR creation. For fork-based PRs
// this is "forkOwner:branch" (GitHub's cross-repo format); for
// same-repo PRs it is just the branch name.
func (s *ProjectSettings) PRHead(branch string) string {
	if s.GitHubUsername != "" {
		return s.GitHubUsername + ":" + branch
	}
	return branch
}
