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

// RunImportInstalls exposes runImportInstalls for testing.
func (p *Pipeline) RunImportInstalls(
	ctx context.Context,
	logger *zap.Logger,
	ctr *container.Container,
	imports []ImportEntry,
) error {
	return p.runImportInstalls(ctx, logger, ctr, imports)
}

// ReadPRDescription exposes readPRDescription for testing.
func ReadPRDescription(dir string) *PRDescription {
	return readPRDescription(dir)
}

// ParsePRContent exposes parsePRContent for testing.
func ParsePRContent(content string) (string, string) {
	return parsePRContent(content)
}

// BuildPRContent exposes buildPRContent for testing.
func BuildPRContent(workItem *models.WorkItem, ticketKey, titlePrefix string, aiPR *PRDescription) (string, string) {
	return buildPRContent(workItem, ticketKey, titlePrefix, aiPR)
}

// ContainerCredsMountTarget exposes the mount path constant for tests.
const ContainerCredsMountTarget = containerCredsMountTarget
