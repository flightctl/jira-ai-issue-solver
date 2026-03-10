package models

// ProjectSettings contains the resolved per-project settings needed
// to execute or recover a job for a specific work item. The concrete
// resolver (built during application startup) maps work items to these
// settings based on the bot's configuration.
type ProjectSettings struct {
	// Owner is the GitHub repository owner (e.g., "my-org").
	Owner string

	// Repo is the GitHub repository name (e.g., "backend").
	Repo string

	// CloneURL is the full clone URL for the repository.
	CloneURL string

	// BaseBranch is the target branch for pull requests (e.g., "main").
	BaseBranch string

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
}
