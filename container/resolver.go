package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

const (
	// DefaultFallbackImage is the built-in fallback container image
	// used when no other configuration source provides an image.
	// This is a minimal image; teams should provide their own image
	// with their toolchain and AI CLI installed.
	DefaultFallbackImage = "ubuntu:latest"

	// botConfigPath is the path (relative to repo root) for the
	// bot-specific container configuration file.
	botConfigPath = ".ai-bot/container.json"

	// devcontainerConfigPath is the path (relative to repo root) for
	// the standard devcontainer configuration file.
	devcontainerConfigPath = ".devcontainer/devcontainer.json"
)

// Resolver resolves container configuration for a repository by
// checking multiple sources in priority order and merging with defaults.
//
// The resolution chain (highest to lowest priority):
//  1. .ai-bot/container.json in the repository
//  2. .devcontainer/devcontainer.json in the repository (practical subset)
//  3. Bot-level defaults (defaultImage, defaultLimits)
//  4. Built-in minimal fallback ([DefaultFallbackImage])
//
// Only the highest-priority repo-level source is used: sources 1 and 2
// do not stack. Within the selected source, any field left unset falls
// through to bot-level defaults, then to the built-in fallback.
type Resolver struct {
	defaults ResolverDefaults
	logger   *zap.Logger
}

// ResolverDefaults holds the global fallback container settings and
// host-level runtime policy. The fallback settings fill in gaps when
// neither the target repo nor the per-project config provides values.
// Host policy (DisableSELinux, UserNS) is always applied regardless
// of the resolution chain.
type ResolverDefaults struct {
	// Fallback holds image, resource limits, and other settings
	// used when no higher-priority source provides them.
	Fallback SettingsOverride

	// DisableSELinux is host-level policy: always applied.
	DisableSELinux bool

	// UserNS is host-level policy: always applied.
	UserNS string
}

// SettingsOverride holds container settings that can come from either
// per-project config or the global fallback. It is the container
// package's counterpart to models.ContainerSettings.
type SettingsOverride struct {
	Image       string
	Limits      ResourceLimits
	Env         map[string]string
	Tmpfs       []string
	ExtraMounts []Mount
}

// NewResolver creates a Resolver with the given bot-level defaults.
// The defaults fill in gaps when a repository's config does not
// specify those values. Pass zero values to rely entirely on
// repository config or the built-in fallback.
func NewResolver(defaults ResolverDefaults, logger *zap.Logger) (*Resolver, error) {
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}
	return &Resolver{
		defaults: defaults,
		logger:   logger,
	}, nil
}

// Resolve determines the container configuration for the repository at
// repoDir. The projectOverride, if non-nil, sits between repo-level
// config and the global fallback in the resolution chain:
//
//  1. .ai-bot/container.json or .devcontainer/devcontainer.json (repo)
//  2. projectOverride (per-project bot config)
//  3. Global fallback (ResolverDefaults.Fallback)
//  4. Built-in fallback (ubuntu:latest)
//
// Host policy (DisableSELinux, UserNS) is applied unconditionally.
func (r *Resolver) Resolve(repoDir string, projectOverride *SettingsOverride) (*Config, error) {
	// Try repo-level configs in priority order.
	repoCfg, err := r.tryBotConfig(repoDir)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", botConfigPath, err)
	}

	if repoCfg == nil {
		repoCfg, err = r.tryDevcontainer(repoDir)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", devcontainerConfigPath, err)
		}
	}

	// Build the resolved config by layering sources from lowest to
	// highest priority.
	resolved := &Config{
		Image:  DefaultFallbackImage,
		Source: "built-in fallback",
	}

	// Layer 3: global fallback.
	applySettingsOverride(resolved, &r.defaults.Fallback, "bot default")

	// Layer 2: per-project override.
	if projectOverride != nil {
		applySettingsOverride(resolved, projectOverride, "project config")
	}

	// Layer 1: repo-level config.
	if repoCfg != nil {
		overlay(resolved, repoCfg)
	}

	// Host policy: always applied.
	resolved.DisableSELinux = r.defaults.DisableSELinux
	resolved.UserNS = r.defaults.UserNS

	if resolved.Env == nil {
		resolved.Env = make(map[string]string)
	}

	return resolved, nil
}

// applySettingsOverride overlays a SettingsOverride onto a Config.
// Non-empty fields in the override replace the corresponding Config
// fields.
func applySettingsOverride(base *Config, so *SettingsOverride, source string) {
	if so.Image != "" {
		base.Image = so.Image
		base.Source = source
	}
	if so.Limits.Memory != "" {
		base.ResourceLimits.Memory = so.Limits.Memory
	}
	if so.Limits.CPUs != "" {
		base.ResourceLimits.CPUs = so.Limits.CPUs
	}
	if len(so.Env) > 0 {
		if base.Env == nil {
			base.Env = make(map[string]string)
		}
		maps.Copy(base.Env, so.Env)
	}
	if len(so.Tmpfs) > 0 {
		base.Tmpfs = so.Tmpfs
	}
	if len(so.ExtraMounts) > 0 {
		base.ExtraMounts = so.ExtraMounts
	}
}

// overlay applies non-zero fields from src onto dst. Empty/zero fields
// in src are treated as "not set" and do not override dst.
func overlay(dst, src *Config) {
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.PostCreateCommand != "" {
		dst.PostCreateCommand = src.PostCreateCommand
	}
	if src.ResourceLimits.Memory != "" {
		dst.ResourceLimits.Memory = src.ResourceLimits.Memory
	}
	if src.ResourceLimits.CPUs != "" {
		dst.ResourceLimits.CPUs = src.ResourceLimits.CPUs
	}
	if len(src.Env) > 0 {
		if dst.Env == nil {
			dst.Env = make(map[string]string)
		}
		maps.Copy(dst.Env, src.Env)
	}
	if src.Source != "" {
		dst.Source = src.Source
	}
}

// --- .ai-bot/container.json ---

// tryBotConfig reads and parses .ai-bot/container.json from the repo.
// Returns (nil, nil) if the file does not exist.
func (r *Resolver) tryBotConfig(repoDir string) (*Config, error) {
	path := filepath.Join(repoDir, botConfigPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is repo dir + constant
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	r.logger.Debug("Found bot container config", zap.String("path", path))
	return parseBotConfig(data)
}

// botConfigJSON is the deserialization target for .ai-bot/container.json.
type botConfigJSON struct {
	Image             string            `json:"image"`
	PostCreateCommand string            `json:"postCreateCommand"`
	Env               map[string]string `json:"env"`
	ResourceLimits    *struct {
		Memory string `json:"memory"`
		CPUs   string `json:"cpus"`
	} `json:"resourceLimits"`
}

func parseBotConfig(data []byte) (*Config, error) {
	var raw botConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := &Config{
		Image:             raw.Image,
		PostCreateCommand: raw.PostCreateCommand,
		Env:               raw.Env,
		Source:            botConfigPath,
	}
	if raw.ResourceLimits != nil {
		cfg.ResourceLimits.Memory = raw.ResourceLimits.Memory
		cfg.ResourceLimits.CPUs = raw.ResourceLimits.CPUs
	}
	return cfg, nil
}

// --- .devcontainer/devcontainer.json ---

// tryDevcontainer reads and parses .devcontainer/devcontainer.json from
// the repo. Returns (nil, nil) if the file does not exist.
func (r *Resolver) tryDevcontainer(repoDir string) (*Config, error) {
	path := filepath.Join(repoDir, devcontainerConfigPath)

	data, err := os.ReadFile(path) // #nosec G304 -- path is repo dir + constant
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	r.logger.Debug("Found devcontainer config", zap.String("path", path))
	return r.parseDevcontainer(data)
}

// devcontainerJSON is the deserialization target for devcontainer.json.
// Only a practical subset of the devcontainer spec is supported:
// image, postCreateCommand, and containerEnv.
type devcontainerJSON struct {
	Image             string            `json:"image"`
	PostCreateCommand json.RawMessage   `json:"postCreateCommand"`
	ContainerEnv      map[string]string `json:"containerEnv"`
}

// devcontainerUnsupportedFields lists devcontainer.json fields that we
// detect but do not support, so we can warn operators.
var devcontainerUnsupportedFields = []string{
	"build",
	"features",
	"forwardPorts",
	"customizations",
	"mounts",
	"runArgs",
	"remoteUser",
	"remoteEnv",
}

func (r *Resolver) parseDevcontainer(data []byte) (*Config, error) {
	cleaned := stripJSONComments(data)

	var raw devcontainerJSON
	if err := json.Unmarshal(cleaned, &raw); err != nil {
		return nil, err
	}

	r.warnUnsupportedFields(cleaned)

	return &Config{
		Image:             raw.Image,
		PostCreateCommand: r.parsePostCreateCommand(raw.PostCreateCommand),
		Env:               raw.ContainerEnv,
		Source:            devcontainerConfigPath,
	}, nil
}

func (r *Resolver) warnUnsupportedFields(data []byte) {
	var rawMap map[string]json.RawMessage
	if json.Unmarshal(data, &rawMap) != nil {
		return
	}
	for _, field := range devcontainerUnsupportedFields {
		if _, ok := rawMap[field]; ok {
			r.logger.Warn("Unsupported devcontainer.json field ignored",
				zap.String("field", field))
		}
	}
}

// parsePostCreateCommand handles the devcontainer spec's
// postCreateCommand, which can be a string or an array of strings.
// Unrecognized formats (e.g., object form) are logged and ignored.
func (r *Resolver) parsePostCreateCommand(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return strings.Join(arr, " && ")
	}

	r.logger.Warn("Unsupported postCreateCommand format ignored (expected string or string array)",
		zap.String("raw", string(raw)))
	return ""
}

// --- JSONC support ---

// stripJSONComments removes single-line (//) and multi-line (/* */)
// comments from JSONC data, then removes trailing commas before } and ].
// String contents are preserved: comment and comma patterns inside
// quoted strings are not modified.
func stripJSONComments(data []byte) []byte {
	withoutComments := removeComments(data)
	return removeTrailingCommas(withoutComments)
}

// removeComments strips // and /* */ comments from JSONC, preserving
// string contents.
func removeComments(data []byte) []byte {
	result := make([]byte, 0, len(data))
	inString := false

	for i := 0; i < len(data); {
		ch := data[i]

		if ch == '"' && !isEscaped(data, i) {
			inString = !inString
			result = append(result, ch)
			i++
			continue
		}

		if inString {
			result = append(result, ch)
			i++
			continue
		}

		// Single-line comment.
		if i+1 < len(data) && ch == '/' && data[i+1] == '/' {
			i += 2
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}

		// Multi-line comment.
		if i+1 < len(data) && ch == '/' && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
				i++
			}
			if i+1 < len(data) {
				i += 2
			}
			continue
		}

		result = append(result, ch)
		i++
	}

	return result
}

// removeTrailingCommas removes commas that appear immediately before
// } or ] (with only whitespace between), preserving string contents.
func removeTrailingCommas(data []byte) []byte {
	result := make([]byte, 0, len(data))
	inString := false

	for i := range len(data) {
		ch := data[i]

		if ch == '"' && !isEscaped(data, i) {
			inString = !inString
			result = append(result, ch)
			continue
		}

		if inString {
			result = append(result, ch)
			continue
		}

		if ch == ',' {
			// Look ahead past whitespace for a closing bracket.
			j := i + 1
			for j < len(data) && isJSONWhitespace(data[j]) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // skip trailing comma
			}
		}

		result = append(result, ch)
	}

	return result
}

// isEscaped reports whether the byte at position i is preceded by an
// odd number of backslashes (meaning it is escaped).
func isEscaped(data []byte, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && data[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
