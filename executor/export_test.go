package executor

import (
	"context"

	"go.uber.org/zap"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/repoconfig"
)

// MergeImports exposes mergeImports for testing.
func MergeImports(
	settings *models.ProjectSettings,
	repoCfg *repoconfig.Config,
) []ImportEntry {
	return mergeImports(settings, repoCfg)
}

// ImportEntry is the exported version of importEntry for tests.
type ImportEntry = importEntry

// ExcludeImportPaths exposes excludeImportPaths for testing.
func ExcludeImportPaths(wsPath string, imports []ImportEntry) error {
	return excludeImportPaths(wsPath, imports)
}

// RunImportInstalls exposes runImportInstalls for testing.
func (p *Pipeline) RunImportInstalls(
	ctx context.Context,
	logger *zap.Logger,
	ctr *container.Container,
	imports []ImportEntry,
) error {
	return p.runImportInstalls(ctx, logger, ctr, imports)
}
