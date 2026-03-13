// Package repoconfig handles parsing of per-repository .ai-bot/config.yaml
// files.
//
// The .ai-bot/config.yaml file is optional. It provides hints for the AI
// agent and settings for the bot. When the file is absent, the bot uses
// defaults and the AI discovers project conventions autonomously from the
// repository itself (CLAUDE.md, Makefile, CI config, etc.).
//
// Configuration precedence: when both bot-level config and repo-level
// .ai-bot/config.yaml specify the same setting, the repo-level config
// wins. This follows the principle that teams own their environments.
package repoconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configPath = ".ai-bot/config.yaml"

// Config represents the per-repository configuration in .ai-bot/config.yaml.
//
// This file serves two purposes:
//   - Hints for the AI: validation_commands tells the AI what commands to
//     use for validation. The AI reads these from the config file directly;
//     they are not included in the task file.
//   - Settings for the bot: pr and ai sections configure how the bot
//     creates PRs and invokes AI providers. imports declares auxiliary
//     repos to clone into the workspace before AI execution.
type Config struct {
	// ValidationCommands are shell commands the AI can use for
	// validation (e.g., "make build", "make test"). These are hints,
	// not directives -- the AI decides when and how to use them.
	// Always non-nil; empty slice when not configured.
	ValidationCommands []string `yaml:"validation_commands"`

	// Imports declares auxiliary repositories to clone into the
	// workspace before AI execution. For example, a shared AI
	// workflow repo that provides skills, guidelines, or scripts.
	// Always non-nil; empty slice when not configured.
	Imports []Import `yaml:"imports"`

	// PR contains settings used by the bot when creating pull requests.
	PR PRConfig `yaml:"pr"`

	// AI contains provider-specific preferences.
	AI AIConfig `yaml:"ai"`
}

// Import declares an auxiliary repository to clone into the workspace.
type Import struct {
	// Repo is the clone URL (e.g., "https://github.com/org/repo").
	Repo string `yaml:"repo"`

	// Path is the destination directory relative to the workspace
	// root (e.g., ".ai-workflows"). Required.
	Path string `yaml:"path"`

	// Ref is the branch, tag, or commit to check out. Empty means
	// the remote's default branch.
	Ref string `yaml:"ref"`

	// Install is a shell command to run inside the container after
	// cloning. Runs from the workspace root with access to the
	// container's toolchain. Empty means no install step.
	Install string `yaml:"install"`
}

// PRConfig contains settings for pull request creation.
type PRConfig struct {
	// Draft controls whether PRs are created as drafts.
	Draft bool `yaml:"draft"`

	// TitlePrefix is prepended to PR titles (e.g., "[AI]").
	TitlePrefix string `yaml:"title_prefix"`

	// Labels are applied to created PRs.
	// Always non-nil; empty slice when not configured.
	Labels []string `yaml:"labels"`
}

// AIConfig contains provider-specific preferences.
type AIConfig struct {
	// Claude contains Claude-specific configuration.
	// Nil when not configured.
	Claude *ClaudeConfig `yaml:"claude"`

	// Gemini contains Gemini-specific configuration.
	// Nil when not configured.
	Gemini *GeminiConfig `yaml:"gemini"`
}

// ClaudeConfig contains Claude-specific settings.
type ClaudeConfig struct {
	// AllowedTools restricts which tools the AI can use
	// (e.g., "Bash Edit Read Write").
	AllowedTools string `yaml:"allowed_tools"`
}

// GeminiConfig contains Gemini-specific settings.
type GeminiConfig struct {
	// Model specifies the Gemini model to use (e.g., "gemini-2.5-pro").
	Model string `yaml:"model"`
}

// Load reads and parses the .ai-bot/config.yaml file from the given
// directory. Returns a zero-value Config with non-nil slices (not an
// error) if the file does not exist. Returns an error if the file
// exists but cannot be parsed.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, configPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is dir + constant
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}

	normalizeSlices(&cfg)
	return &cfg, nil
}

// Default returns a Config with non-nil empty slices and zero-value
// settings. Used when the config file is absent or cannot be loaded.
func Default() *Config {
	return defaultConfig()
}

// defaultConfig returns a Config with non-nil empty slices.
func defaultConfig() *Config {
	return &Config{
		ValidationCommands: []string{},
		Imports:            []Import{},
		PR: PRConfig{
			Labels: []string{},
		},
	}
}

// normalizeSlices ensures slice fields are non-nil.
func normalizeSlices(cfg *Config) {
	if cfg.ValidationCommands == nil {
		cfg.ValidationCommands = []string{}
	}
	if cfg.Imports == nil {
		cfg.Imports = []Import{}
	}
	if cfg.PR.Labels == nil {
		cfg.PR.Labels = []string{}
	}
}
