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

	// RootRepoURL is the clone URL of a scaffold repo cloned as the
	// workspace root. When set, the scaffold is cloned first and
	// child repos are placed as subdirectories inside it. The
	// scaffold is never branched, committed to, or PR'd.
	RootRepoURL string

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

	// FailureLabels holds the configured failure-state label strings.
	// Empty strings mean the corresponding labeling behavior is
	// disabled.
	FailureLabels FailureLabels

	// LifecycleLabels holds the configured lifecycle label strings
	// that track ticket progression (queued → review → merged).
	// Empty strings disable the corresponding label.
	LifecycleLabels LifecycleLabels

	// PRValidationLabels holds configurable GitHub PR labels applied
	// when the AI session reports validation failure or exits with a
	// non-zero code. At most one is set on a PR at any time.
	PRValidationLabels PRValidationLabels

	// MergedStatus is the tracker status name to transition to when
	// all PRs are merged. Empty means no transition on merge.
	MergedStatus string

	// ForkMode indicates this project requires fork-based
	// contributions. When true, the bot pushes to the assignee's
	// fork and creates cross-repo PRs.
	ForkMode bool

	// GitHubUsername is the GitHub username of the ticket assignee,
	// resolved from the assignee-to-GitHub-username config mapping.
	// Empty when the assignee has no mapping or the ticket is
	// unassigned. Only used when ForkMode is true.
	GitHubUsername string

	// MaxTicketCostUSD is the per-ticket cost cap in USD. No new AI
	// sessions are started for a ticket once its cumulative cost
	// reaches or exceeds this value. Zero or negative means no cap.
	MaxTicketCostUSD float64
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
// Returns empty string when fork mode is disabled or no assignee
// mapping exists.
func (s *ProjectSettings) ForkOwner() string {
	if !s.ForkMode {
		return ""
	}
	return s.GitHubUsername
}

// CommitOwner returns the repo owner to target for commits and
// branch operations. When fork mode is enabled and a fork owner
// is configured, commits go to the fork; otherwise they go
// directly to the upstream repo. For multi-repo workspaces use
// CommitOwnerFor instead; this method uses Repos[0] as a
// convenience for single-repo callers.
func (s *ProjectSettings) CommitOwner() string {
	if s.ForkMode && s.GitHubUsername != "" {
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
	if s.ForkMode && s.GitHubUsername != "" {
		return s.GitHubUsername
	}
	return repo.Owner
}

// PRHead returns the head ref for PR creation. For fork-based PRs
// this is "forkOwner:branch" (GitHub's cross-repo format); for
// same-repo PRs it is just the branch name.
func (s *ProjectSettings) PRHead(branch string) string {
	if s.ForkMode && s.GitHubUsername != "" {
		return s.GitHubUsername + ":" + branch
	}
	return branch
}

// PRHeads returns candidate head refs in priority order for PR
// lookup. Fork-mode projects return the fork head first with a
// direct-mode fallback so that PRs created before fork_mode was
// enabled can still be found.
func (s *ProjectSettings) PRHeads(branch string) []string {
	if s.ForkMode && s.GitHubUsername != "" {
		return []string{s.GitHubUsername + ":" + branch, branch}
	}
	return []string{branch}
}
