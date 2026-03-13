// Package containertest provides test doubles for the container package.
package containertest

import (
	"context"
	"time"

	"jira-ai-issue-solver/container"
)

// Compile-time checks.
var (
	_ container.Runner  = (*StubRunner)(nil)
	_ container.Manager = (*StubManager)(nil)
)

// StubRunner is a test double for [container.Runner].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubRunner struct {
	PullFunc           func(ctx context.Context, image string) error
	RunFunc            func(ctx context.Context, opts container.RunOptions) (string, error)
	ExecFunc           func(ctx context.Context, containerID string, cmd []string) (string, int, error)
	StopFunc           func(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveFunc         func(ctx context.Context, containerID string) error
	ListContainersFunc func(ctx context.Context, namePrefix string) ([]string, error)
}

func (s *StubRunner) Pull(ctx context.Context, image string) error {
	if s.PullFunc != nil {
		return s.PullFunc(ctx, image)
	}
	return nil
}

func (s *StubRunner) Run(ctx context.Context, opts container.RunOptions) (string, error) {
	if s.RunFunc != nil {
		return s.RunFunc(ctx, opts)
	}
	return "", nil
}

func (s *StubRunner) Exec(ctx context.Context, containerID string, cmd []string) (string, int, error) {
	if s.ExecFunc != nil {
		return s.ExecFunc(ctx, containerID, cmd)
	}
	return "", 0, nil
}

func (s *StubRunner) Stop(ctx context.Context, containerID string, timeout time.Duration) error {
	if s.StopFunc != nil {
		return s.StopFunc(ctx, containerID, timeout)
	}
	return nil
}

func (s *StubRunner) Remove(ctx context.Context, containerID string) error {
	if s.RemoveFunc != nil {
		return s.RemoveFunc(ctx, containerID)
	}
	return nil
}

func (s *StubRunner) ListContainers(ctx context.Context, namePrefix string) ([]string, error) {
	if s.ListContainersFunc != nil {
		return s.ListContainersFunc(ctx, namePrefix)
	}
	return nil, nil
}

// StubManager is a test double for [container.Manager].
// Set the corresponding Func field to control each method's behavior.
// When a Func field is nil, the method returns zero values.
type StubManager struct {
	ResolveConfigFunc  func(repoDir string, projectOverride *container.SettingsOverride) (*container.Config, error)
	StartFunc          func(ctx context.Context, cfg *container.Config, workspaceDir, ticketKey string, env map[string]string) (*container.Container, error)
	ExecFunc           func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error)
	StopFunc           func(ctx context.Context, ctr *container.Container) error
	CleanupOrphansFunc func(ctx context.Context, prefix string) error
}

func (s *StubManager) ResolveConfig(repoDir string, projectOverride *container.SettingsOverride) (*container.Config, error) {
	if s.ResolveConfigFunc != nil {
		return s.ResolveConfigFunc(repoDir, projectOverride)
	}
	return &container.Config{}, nil
}

func (s *StubManager) Start(ctx context.Context, cfg *container.Config, workspaceDir, ticketKey string, env map[string]string) (*container.Container, error) {
	if s.StartFunc != nil {
		return s.StartFunc(ctx, cfg, workspaceDir, ticketKey, env)
	}
	return &container.Container{}, nil
}

func (s *StubManager) Exec(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
	if s.ExecFunc != nil {
		return s.ExecFunc(ctx, ctr, cmd)
	}
	return "", 0, nil
}

func (s *StubManager) Stop(ctx context.Context, ctr *container.Container) error {
	if s.StopFunc != nil {
		return s.StopFunc(ctx, ctr)
	}
	return nil
}

func (s *StubManager) CleanupOrphans(ctx context.Context, prefix string) error {
	if s.CleanupOrphansFunc != nil {
		return s.CleanupOrphansFunc(ctx, prefix)
	}
	return nil
}
