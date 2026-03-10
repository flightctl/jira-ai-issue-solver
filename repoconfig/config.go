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
//     creates PRs and invokes AI providers.
type Config struct {
	// ValidationCommands are shell commands the AI can use for
	// validation (e.g., "make build", "make test"). These are hints,
	// not directives -- the AI decides when and how to use them.
	// Always non-nil; empty slice when not configured.
	ValidationCommands []string `yaml:"validation_commands"`

	// PR contains settings used by the bot when creating pull requests.
	PR PRConfig `yaml:"pr"`

	// AI contains provider-specific preferences.
	AI AIConfig `yaml:"ai"`
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

// defaultConfig returns a Config with non-nil empty slices.
func defaultConfig() *Config {
	return &Config{
		ValidationCommands: []string{},
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
	if cfg.PR.Labels == nil {
		cfg.PR.Labels = []string{}
	}
}
