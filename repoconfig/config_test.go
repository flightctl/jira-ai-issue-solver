package repoconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"jira-ai-issue-solver/repoconfig"
)

func TestLoad_FullConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `validation_commands:
  - make build
  - make lint
  - make test
pr:
  draft: true
  title_prefix: "[AI]"
  labels:
    - ai-generated
    - automated
ai:
  claude:
    allowed_tools: "Bash Edit Read Write"
  gemini:
    model: "gemini-2.5-pro"
`)

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.ValidationCommands) != 3 {
		t.Fatalf("len(ValidationCommands) = %d, want 3", len(cfg.ValidationCommands))
	}
	if cfg.ValidationCommands[0] != "make build" {
		t.Errorf("ValidationCommands[0] = %q, want %q", cfg.ValidationCommands[0], "make build")
	}
	if cfg.ValidationCommands[1] != "make lint" {
		t.Errorf("ValidationCommands[1] = %q, want %q", cfg.ValidationCommands[1], "make lint")
	}
	if cfg.ValidationCommands[2] != "make test" {
		t.Errorf("ValidationCommands[2] = %q, want %q", cfg.ValidationCommands[2], "make test")
	}

	if !cfg.PR.Draft {
		t.Error("PR.Draft = false, want true")
	}
	if cfg.PR.TitlePrefix != "[AI]" {
		t.Errorf("PR.TitlePrefix = %q, want %q", cfg.PR.TitlePrefix, "[AI]")
	}
	if len(cfg.PR.Labels) != 2 {
		t.Fatalf("len(PR.Labels) = %d, want 2", len(cfg.PR.Labels))
	}
	if cfg.PR.Labels[0] != "ai-generated" {
		t.Errorf("PR.Labels[0] = %q, want %q", cfg.PR.Labels[0], "ai-generated")
	}

	if cfg.AI.Claude == nil {
		t.Fatal("AI.Claude is nil")
	}
	if cfg.AI.Claude.AllowedTools != "Bash Edit Read Write" {
		t.Errorf("AI.Claude.AllowedTools = %q, want %q", cfg.AI.Claude.AllowedTools, "Bash Edit Read Write")
	}

	if cfg.AI.Gemini == nil {
		t.Fatal("AI.Gemini is nil")
	}
	if cfg.AI.Gemini.Model != "gemini-2.5-pro" {
		t.Errorf("AI.Gemini.Model = %q, want %q", cfg.AI.Gemini.Model, "gemini-2.5-pro")
	}
}

func TestLoad_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `pr:
  title_prefix: "[Bot]"
`)

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.ValidationCommands) != 0 {
		t.Errorf("len(ValidationCommands) = %d, want 0", len(cfg.ValidationCommands))
	}
	if cfg.ValidationCommands == nil {
		t.Error("ValidationCommands should be non-nil empty slice")
	}

	if cfg.PR.TitlePrefix != "[Bot]" {
		t.Errorf("PR.TitlePrefix = %q, want %q", cfg.PR.TitlePrefix, "[Bot]")
	}
	if cfg.PR.Labels == nil {
		t.Error("PR.Labels should be non-nil empty slice")
	}

	if cfg.AI.Claude != nil {
		t.Error("AI.Claude should be nil when not configured")
	}
	if cfg.AI.Gemini != nil {
		t.Error("AI.Gemini should be nil when not configured")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ValidationCommands == nil {
		t.Error("ValidationCommands should be non-nil")
	}
	if len(cfg.ValidationCommands) != 0 {
		t.Errorf("len(ValidationCommands) = %d, want 0", len(cfg.ValidationCommands))
	}
	if cfg.PR.Labels == nil {
		t.Error("PR.Labels should be non-nil")
	}
	if cfg.PR.Draft {
		t.Error("PR.Draft should be false by default")
	}
	if cfg.PR.TitlePrefix != "" {
		t.Errorf("PR.TitlePrefix = %q, want empty", cfg.PR.TitlePrefix)
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `validation_commands: [
  invalid yaml here`)

	_, err := repoconfig.Load(dir)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "")

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ValidationCommands == nil {
		t.Error("ValidationCommands should be non-nil")
	}
	if cfg.PR.Labels == nil {
		t.Error("PR.Labels should be non-nil")
	}
}

func TestLoad_OnlyValidationCommands(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `validation_commands:
  - npm test
  - npm run lint
`)

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.ValidationCommands) != 2 {
		t.Fatalf("len(ValidationCommands) = %d, want 2", len(cfg.ValidationCommands))
	}
	if cfg.ValidationCommands[0] != "npm test" {
		t.Errorf("ValidationCommands[0] = %q, want %q", cfg.ValidationCommands[0], "npm test")
	}
	if cfg.ValidationCommands[1] != "npm run lint" {
		t.Errorf("ValidationCommands[1] = %q, want %q", cfg.ValidationCommands[1], "npm run lint")
	}
}

// --- Imports ---

func TestLoad_WithImports(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `imports:
  - repo: https://github.com/org/workflows
    path: .ai-workflows
    ref: main
  - repo: https://github.com/org/tools
    path: .tools
`)

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Imports) != 2 {
		t.Fatalf("len(Imports) = %d, want 2", len(cfg.Imports))
	}
	if cfg.Imports[0].Repo != "https://github.com/org/workflows" {
		t.Errorf("Imports[0].Repo = %q, want %q", cfg.Imports[0].Repo, "https://github.com/org/workflows")
	}
	if cfg.Imports[0].Path != ".ai-workflows" {
		t.Errorf("Imports[0].Path = %q, want %q", cfg.Imports[0].Path, ".ai-workflows")
	}
	if cfg.Imports[0].Ref != "main" {
		t.Errorf("Imports[0].Ref = %q, want %q", cfg.Imports[0].Ref, "main")
	}
	if cfg.Imports[1].Ref != "" {
		t.Errorf("Imports[1].Ref = %q, want empty", cfg.Imports[1].Ref)
	}
}

func TestLoad_NoImports_NonNilSlice(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `pr:
  draft: true
`)

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Imports == nil {
		t.Error("Imports should be non-nil empty slice")
	}
	if len(cfg.Imports) != 0 {
		t.Errorf("len(Imports) = %d, want 0", len(cfg.Imports))
	}
}

func TestLoad_MissingFile_ImportsNonNil(t *testing.T) {
	dir := t.TempDir()

	cfg, err := repoconfig.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Imports == nil {
		t.Error("Imports should be non-nil when file is missing")
	}
}

// --- helpers ---

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	configDir := filepath.Join(dir, ".ai-bot")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
