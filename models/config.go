package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// LogLevel represents the logging level
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// LogFormat represents the logging format
type LogFormat string

const (
	LogFormatConsole LogFormat = "console"
	LogFormatJSON    LogFormat = "json"
)

// String returns the string representation of LogLevel
func (l LogLevel) String() string {
	return string(l)
}

// String returns the string representation of LogFormat
func (f LogFormat) String() string {
	return string(f)
}

// IsValid checks if the LogLevel is valid
func (l LogLevel) IsValid() bool {
	switch l {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	default:
		return false
	}
}

// IsValid checks if the LogFormat is valid
func (f LogFormat) IsValid() bool {
	switch f {
	case LogFormatConsole, LogFormatJSON:
		return true
	default:
		return false
	}
}

// UnmarshalYAML implements custom unmarshaling for LogLevel
func (l *LogLevel) UnmarshalYAML(value *yaml.Node) error {
	var str string
	if err := value.Decode(&str); err != nil {
		return err
	}

	level := LogLevel(strings.ToLower(str))
	if !level.IsValid() {
		return fmt.Errorf("invalid log level: %s. Valid options are: debug, info, warn, error", str)
	}

	*l = level
	return nil
}

// UnmarshalYAML implements custom unmarshaling for LogFormat
func (f *LogFormat) UnmarshalYAML(value *yaml.Node) error {
	var str string
	if err := value.Decode(&str); err != nil {
		return err
	}

	format := LogFormat(strings.ToLower(str))
	if !format.IsValid() {
		return fmt.Errorf("invalid log format: %s. Valid options are: console, json", str)
	}

	*f = format
	return nil
}

// ComponentToRepoMap is a custom type for parsing component_to_repo from environment variables and YAML
type ComponentToRepoMap map[string]string

// UnmarshalText implements encoding.TextUnmarshaler for parsing from environment variables
func (c *ComponentToRepoMap) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*c = make(map[string]string)
		return nil
	}

	str := string(text)
	pairs := strings.Split(str, ",")
	result := make(map[string]string)

	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}

	*c = result
	return nil
}

// ProjectKeys is a custom type for parsing project_keys from environment variables and YAML
type ProjectKeys []string

// UnmarshalText implements encoding.TextUnmarshaler for parsing from environment variables
func (p *ProjectKeys) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*p = make([]string, 0)
		return nil
	}

	str := string(text)
	keys := strings.Split(str, ",")
	result := make([]string, 0, len(keys))

	for _, key := range keys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" {
			result = append(result, trimmedKey)
		}
	}

	*p = result
	return nil
}

// UnmarshalYAML implements custom unmarshaling for YAML to preserve case sensitivity
func (c *ComponentToRepoMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node for ComponentToRepoMap, got %v", value.Kind)
	}

	result := make(map[string]string)
	for i := 0; i < len(value.Content); i += 2 {
		if i+1 >= len(value.Content) {
			break
		}
		keyNode := value.Content[i]
		valueNode := value.Content[i+1]

		if keyNode.Kind != yaml.ScalarNode {
			continue
		}

		var key, val string
		if err := keyNode.Decode(&key); err != nil {
			continue
		}
		if err := valueNode.Decode(&val); err != nil {
			continue
		}

		// Preserve the exact case of the key as it appears in YAML
		result[key] = val
	}

	*c = result
	return nil
}

// StatusTransitions represents the status transitions for different ticket types
type StatusTransitions struct {
	Todo       string `yaml:"todo" mapstructure:"todo" default:"To Do"`
	InProgress string `yaml:"in_progress" mapstructure:"in_progress" default:"In Progress"`
	InReview   string `yaml:"in_review" mapstructure:"in_review" default:"In Review"`
}

// TicketTypeStatusTransitions maps ticket types to their specific status transitions
type TicketTypeStatusTransitions map[string]StatusTransitions

// UnmarshalYAML implements custom unmarshaling for TicketTypeStatusTransitions
func (t *TicketTypeStatusTransitions) UnmarshalYAML(value *yaml.Node) error {
	*t = make(TicketTypeStatusTransitions)

	// Handle the case where status_transitions is a map of ticket types
	return value.Decode((*map[string]StatusTransitions)(t))
}

// GetStatusTransitions returns the status transitions for a specific ticket type
func (t TicketTypeStatusTransitions) GetStatusTransitions(ticketType string) StatusTransitions {
	// Try exact match first
	if transitions, exists := t[ticketType]; exists {
		return transitions
	}
	// Try lowercase match (since Viper converts YAML keys to lowercase)
	if transitions, exists := t[strings.ToLower(ticketType)]; exists {
		return transitions
	}
	// Return empty transitions if ticket type is not configured
	return StatusTransitions{}
}

// UnmarshalMapstructure implements custom mapstructure decoding for handling different data types
func (t *TicketTypeStatusTransitions) UnmarshalMapstructure(data interface{}) error {
	*t = make(TicketTypeStatusTransitions)

	// Handle string data (from environment variables as JSON)
	if jsonStr, ok := data.(string); ok {
		// Try to parse as JSON
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &jsonData); err != nil {
			return fmt.Errorf("failed to parse status transitions JSON: %w", err)
		}

		// Convert JSON data to TicketTypeStatusTransitions
		for ticketType, transitionData := range jsonData {
			if transitionMap, ok := transitionData.(map[string]interface{}); ok {
				transitions := StatusTransitions{}
				if todo, ok := transitionMap["todo"].(string); ok {
					transitions.Todo = todo
				}
				if inProgress, ok := transitionMap["in_progress"].(string); ok {
					transitions.InProgress = inProgress
				}
				if inReview, ok := transitionMap["in_review"].(string); ok {
					transitions.InReview = inReview
				}
				(*t)[ticketType] = transitions
			}
		}
		return nil
	}

	// Handle the case where data is a map[string]interface{} (from mapstructure)
	if mapData, ok := data.(map[string]interface{}); ok {

		// New format - convert map[string]interface{} to map[string]StatusTransitions
		for ticketType, transitionData := range mapData {
			if transitionMap, ok := transitionData.(map[string]interface{}); ok {
				transitions := StatusTransitions{}
				if todo, ok := transitionMap["todo"].(string); ok {
					transitions.Todo = todo
				}
				if inProgress, ok := transitionMap["in_progress"].(string); ok {
					transitions.InProgress = inProgress
				}
				if inReview, ok := transitionMap["in_review"].(string); ok {
					transitions.InReview = inReview
				}
				(*t)[ticketType] = transitions
			}
		}
		return nil
	}

	return fmt.Errorf("unsupported data type for TicketTypeStatusTransitions: %T", data)
}

// ProjectConfig represents configuration for a specific project or group of projects
type ProjectConfig struct {
	ProjectKeys             ProjectKeys                 `yaml:"project_keys" mapstructure:"project_keys"`
	StatusTransitions       TicketTypeStatusTransitions `yaml:"status_transitions" mapstructure:"status_transitions"`
	GitPullRequestFieldName string                      `yaml:"git_pull_request_field_name" mapstructure:"git_pull_request_field_name"`
	ComponentToRepo         ComponentToRepoMap          `yaml:"component_to_repo" mapstructure:"component_to_repo"`
	DisableErrorComments    bool                        `yaml:"disable_error_comments" mapstructure:"disable_error_comments" default:"false"`

	// Container holds per-project container settings. When set,
	// these override the global fallback (container.fallback) but
	// are still overridden by repo-level config
	// (.ai-bot/container.json or .devcontainer/devcontainer.json).
	Container ContainerSettings `yaml:"container" mapstructure:"container"`
}

type JiraConfig struct {
	BaseURL                  string            `yaml:"base_url" mapstructure:"base_url"`
	Username                 string            `yaml:"username" mapstructure:"username"`
	APIToken                 string            `yaml:"api_token" mapstructure:"api_token"`
	IntervalSeconds          int               `yaml:"interval_seconds" mapstructure:"interval_seconds" default:"300"`
	AssigneeToGitHubUsername map[string]string `yaml:"assignee_to_github_username" mapstructure:"assignee_to_github_username"`
	Projects                 []ProjectConfig   `yaml:"projects" mapstructure:"projects"`
}

// Config represents the application configuration
type Config struct {
	// Server configuration
	Server struct {
		Port int `yaml:"port" mapstructure:"port" default:"8080"`
	} `yaml:"server" mapstructure:"server"`

	// Logging configuration
	Logging struct {
		Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
		Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
	} `yaml:"logging" mapstructure:"logging"`

	// Jira configuration
	Jira JiraConfig `yaml:"jira" mapstructure:"jira"`

	// GitHub configuration
	GitHub struct {
		// GitHub App authentication
		AppID          int64  `yaml:"app_id" mapstructure:"app_id"`
		PrivateKeyPath string `yaml:"private_key_path" mapstructure:"private_key_path"`

		// Common fields
		BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
		BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"` // Optional: auto-constructed for GitHub App mode
		TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
		PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
		SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`                     // Path to SSH private key for commit signing
		MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"` // Maximum number of bot replies allowed in a comment thread (e.g., 5 = bot can reply up to 5 times)
		KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`       // List of known bot usernames to prevent loops
		IgnoredUsernames  []string `yaml:"ignored_usernames" mapstructure:"ignored_usernames"`           // List of usernames whose PR comments are completely ignored
	} `yaml:"github" mapstructure:"github"`

	// AI Provider selection
	AIProvider string `yaml:"ai_provider" mapstructure:"ai_provider" default:"claude"` // "claude" or "gemini"

	// Claude configuration — only the API key is needed at the bot level;
	// CLI path, timeout, and tool settings are configured per-repo via
	// .ai-bot/config.yaml or container environment.
	Claude struct {
		APIKey string `yaml:"api_key" mapstructure:"api_key"`
	} `yaml:"claude" mapstructure:"claude"`

	// Gemini configuration — only the API key is needed at the bot level.
	Gemini struct {
		APIKey string `yaml:"api_key" mapstructure:"api_key"`
	} `yaml:"gemini" mapstructure:"gemini"`

	// Workspaces configuration for ticket-scoped workspace lifecycle
	Workspaces WorkspacesConfig `yaml:"workspaces" mapstructure:"workspaces"`

	// Container configuration for dev container management
	Container ContainerCfg `yaml:"container" mapstructure:"container"`

	// Guardrails configuration for safety and resource limits
	Guardrails GuardrailsConfig `yaml:"guardrails" mapstructure:"guardrails"`
}

// ContainerCfg holds bot-level container configuration: host-level
// runtime policy and a global fallback for container settings.
type ContainerCfg struct {
	// Runtime specifies the container runtime preference. Valid values
	// are "auto" (detect, preferring podman), "podman", or "docker".
	Runtime string `yaml:"runtime" mapstructure:"runtime" default:"auto"`

	// DisableSELinux disables SELinux confinement for all spawned
	// containers (--security-opt=label=disable). This is host-level
	// policy and applies regardless of per-project container config.
	DisableSELinux bool `yaml:"disable_selinux" mapstructure:"disable_selinux"`

	// UserNS sets the user namespace mode for all spawned containers
	// (e.g., "keep-id", "keep-id:uid=1000,gid=1000"). This is
	// host-level policy. Empty means the container runtime's default.
	UserNS string `yaml:"userns" mapstructure:"userns"`

	// Fallback holds the global fallback container settings, used
	// when neither the target repo nor the project config specifies
	// container settings.
	Fallback ContainerSettings `yaml:"fallback" mapstructure:"fallback"`
}

// ContainerSettings holds per-environment container settings. This
// type is shared between the global fallback (container.fallback) and
// per-project overrides (jira.projects[].container). Both sit below
// repo-level config (.ai-bot/container.json) in the resolution chain.
type ContainerSettings struct {
	// Image is the container image reference (e.g., "my-org/dev:latest").
	Image string `yaml:"image" mapstructure:"image"`

	// ResourceLimits constrain the container's resource usage.
	ResourceLimits ContainerResourceLimits `yaml:"resource_limits" mapstructure:"resource_limits"`

	// Env holds static environment variables injected into the
	// container. These are merged additively through the resolution
	// chain: higher-priority sources override keys from lower-priority
	// sources, but keys not present in the higher source are preserved.
	// These are separate from runtime env vars (API keys, etc.) passed
	// by the executor at start time.
	Env map[string]string `yaml:"env" mapstructure:"env"`

	// Tmpfs specifies tmpfs mounts. Each entry uses the standard
	// runtime format (e.g., "/tmp:size=4g").
	Tmpfs []string `yaml:"tmpfs" mapstructure:"tmpfs"`

	// ExtraMounts specifies additional volume mounts beyond the
	// workspace mount. Useful for persistent caches (Go module
	// cache, build cache) that survive across container restarts.
	ExtraMounts []ExtraMountCfg `yaml:"extra_mounts" mapstructure:"extra_mounts"`
}

// ExtraMountCfg represents an additional volume mount for containers.
type ExtraMountCfg struct {
	Source  string `yaml:"source" mapstructure:"source"`
	Target  string `yaml:"target" mapstructure:"target"`
	Options string `yaml:"options" mapstructure:"options"`
}

// ContainerResourceLimits holds default resource limits for containers.
type ContainerResourceLimits struct {
	// Memory limit in container runtime format (e.g., "8g", "512m").
	Memory string `yaml:"memory" mapstructure:"memory"`

	// CPUs limit in container runtime format (e.g., "4", "0.5").
	CPUs string `yaml:"cpus" mapstructure:"cpus"`
}

// WorkspacesConfig holds configuration for ticket-scoped workspace management.
type WorkspacesConfig struct {
	// BaseDir is the root directory under which per-ticket workspaces are created.
	// Each workspace is a subdirectory named after the ticket key (e.g., PROJ-123/).
	BaseDir string `yaml:"base_dir" mapstructure:"base_dir"`

	// TTLDays is the maximum age (in days) before a workspace is eligible
	// for cleanup, regardless of ticket status. Must be positive.
	//
	// If a workspace is cleaned up while its ticket still has an active PR,
	// the feedback pipeline will self-heal by re-cloning the repository.
	// However, AI-generated artifacts from prior sessions will be lost.
	// Set this high enough to cover typical PR review turnaround times.
	TTLDays int `yaml:"ttl_days" mapstructure:"ttl_days" default:"7"`
}

// GuardrailsConfig holds safety and resource limit settings.
type GuardrailsConfig struct {
	// MaxConcurrentJobs is the maximum number of jobs that can run
	// simultaneously. Must be positive.
	MaxConcurrentJobs int `yaml:"max_concurrent_jobs" mapstructure:"max_concurrent_jobs" default:"10"`

	// MaxRetries is the maximum number of times a ticket can fail
	// before further submissions are rejected. Zero means no retries
	// (one attempt total). Negative disables the retry limit.
	MaxRetries int `yaml:"max_retries" mapstructure:"max_retries" default:"3"`

	// MaxDailyCostUSD is the maximum daily AI session cost in USD.
	// Job creation is paused when this budget is exceeded. Zero or
	// negative disables cost-based limiting.
	MaxDailyCostUSD float64 `yaml:"max_daily_cost_usd" mapstructure:"max_daily_cost_usd"`

	// MaxContainerRuntimeMinutes is the maximum duration (in minutes)
	// for an AI session inside a container. Zero means no timeout.
	MaxContainerRuntimeMinutes int `yaml:"max_container_runtime_minutes" mapstructure:"max_container_runtime_minutes" default:"60"`

	// CircuitBreakerThreshold is the number of consecutive failures
	// within CircuitBreakerWindow that trips the breaker. Zero
	// disables the circuit breaker.
	CircuitBreakerThreshold int `yaml:"circuit_breaker_threshold" mapstructure:"circuit_breaker_threshold" default:"5"`

	// CircuitBreakerWindowMinutes is the time window (in minutes) for
	// counting consecutive failures. Failures outside this window are
	// pruned.
	CircuitBreakerWindowMinutes int `yaml:"circuit_breaker_window_minutes" mapstructure:"circuit_breaker_window_minutes" default:"10"`

	// CircuitBreakerCooldownMinutes is how long (in minutes) the circuit
	// breaker stays open before automatically resetting.
	CircuitBreakerCooldownMinutes int `yaml:"circuit_breaker_cooldown_minutes" mapstructure:"circuit_breaker_cooldown_minutes" default:"5"`
}

// GetProjectConfigForTicket returns the project configuration for a given ticket key
func (c *Config) GetProjectConfigForTicket(ticketKey string) *ProjectConfig {
	projectKey := strings.Split(ticketKey, "-")[0]

	for _, project := range c.Jira.Projects {
		for _, key := range project.ProjectKeys {
			if strings.EqualFold(key, projectKey) {
				return &project
			}
		}
	}

	// Return the first project if no specific match found (fallback)
	if len(c.Jira.Projects) > 0 {
		return &c.Jira.Projects[0]
	}

	return nil
}

// GetAllProjectKeys returns all project keys from all project configurations
func (c *Config) GetAllProjectKeys() []string {
	var allKeys []string
	for _, project := range c.Jira.Projects {
		allKeys = append(allKeys, project.ProjectKeys...)
	}
	return allKeys
}

// GetBotEmail returns the bot email, constructing it from app_id and bot_username for GitHub App mode if not explicitly set
// For GitHub App: {app_id}+{bot_username}[bot]@users.noreply.github.com
// For PAT mode: uses the explicitly configured bot_email
func (c *Config) GetBotEmail() string {
	// If bot_email is explicitly set, use it (PAT mode or manual override)
	if c.GitHub.BotEmail != "" {
		return c.GitHub.BotEmail
	}

	// For GitHub App mode, construct from app_id and bot_username
	if c.GitHub.AppID > 0 {
		return fmt.Sprintf("%d+%s[bot]@users.noreply.github.com", c.GitHub.AppID, c.GitHub.BotUsername)
	}

	// Fallback: return empty string (will be caught by validation)
	return ""
}

// LoadConfig loads configuration from multiple sources with Viper
// Priority order: Environment variables > Config file > .env file > Defaults
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	// Set defaults first
	setDefaults(v)

	// Enable environment variable support
	v.SetEnvPrefix("JIRA_AI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Note: Automatic environment variable mapping is enabled
	// Environment variables should use the format: JIRA_AI_<SECTION>_<FIELD>
	// Examples:
	// - JIRA_AI_JIRA_BASE_URL → jira.base_url
	// - JIRA_AI_JIRA_USERNAME → jira.username
	// - JIRA_AI_JIRA_GIT_PULL_REQUEST_FIELD_NAME → jira.git_pull_request_field_name

	// Helper function to bind environment variables with error checking
	// Panics on error since all keys are static strings and should never fail
	bindEnv := func(key string) {
		if err := v.BindEnv(key); err != nil {
			panic(fmt.Sprintf("failed to bind environment variable %s: %v", key, err))
		}
	}

	// Explicit bindings for all environment variables (automatic mapping doesn't work with nested structs)
	// Jira configuration
	bindEnv("jira.base_url")
	bindEnv("jira.username")
	bindEnv("jira.api_token")
	bindEnv("jira.interval_seconds")
	bindEnv("jira.assignee_to_github_username")
	bindEnv("jira.disable_error_comments")
	bindEnv("jira.git_pull_request_field_name")
	bindEnv("jira.status_transitions")
	bindEnv("jira.project_keys")

	// GitHub configuration
	bindEnv("github.app_id")
	bindEnv("github.private_key_path")
	bindEnv("github.bot_username")
	bindEnv("github.bot_email")
	bindEnv("github.target_branch")
	bindEnv("github.pr_label")
	bindEnv("github.ssh_key_path")
	bindEnv("github.max_thread_depth")
	bindEnv("github.known_bot_usernames")
	bindEnv("github.ignored_usernames")

	// AI configuration
	bindEnv("ai_provider")

	// AI API key configuration
	bindEnv("claude.api_key")
	bindEnv("gemini.api_key")

	// Server configuration
	bindEnv("server.port")
	bindEnv("PORT")

	// Logging configuration
	bindEnv("logging.level")
	bindEnv("logging.format")

	// Workspaces configuration
	bindEnv("workspaces.base_dir")
	bindEnv("workspaces.ttl_days")

	// Container configuration
	bindEnv("container.runtime")
	bindEnv("container.disable_selinux")
	bindEnv("container.userns")
	bindEnv("container.fallback.image")
	bindEnv("container.fallback.resource_limits.memory")
	bindEnv("container.fallback.resource_limits.cpus")

	// Guardrails configuration
	bindEnv("guardrails.max_concurrent_jobs")
	bindEnv("guardrails.max_retries")
	bindEnv("guardrails.max_daily_cost_usd")
	bindEnv("guardrails.max_container_runtime_minutes")
	bindEnv("guardrails.circuit_breaker_threshold")
	bindEnv("guardrails.circuit_breaker_window_minutes")
	bindEnv("guardrails.circuit_breaker_cooldown_minutes")

	// Note: component_to_repo has custom unmarshaling logic, so we don't bind it explicitly

	// Load main config file if provided
	if configPath != "" {
		v.SetConfigFile(configPath)

		if err := v.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				// Config file not found; this is okay, we'll use env vars and defaults
				fmt.Printf("Warning: Config file %s not found, using environment variables and defaults\n", configPath)
			} else {
				// Config file was found but another error was produced
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
		} else {
			fmt.Printf("Config file found and successfully parsed: %s\n", configPath)
		}
	}

	// Load .env file if it exists (after main config to allow overrides)
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		// It's okay if .env file doesn't exist
		_ = err
	}

	// Unmarshal into struct
	var config Config
	if err := v.Unmarshal(&config, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
			if t == reflect.TypeOf(TicketTypeStatusTransitions{}) {
				var result TicketTypeStatusTransitions
				if err := result.UnmarshalMapstructure(data); err != nil {
					return nil, err
				}
				return result, nil
			}
			return data, nil
		},
	))); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Fallback to environment variable parsing for projects if still empty
	if len(config.Jira.Projects) == 0 {
		// Create a default project from environment variables
		defaultProject := ProjectConfig{}

		// Handle component_to_repo from environment
		componentToRepoStr := v.GetString("component_to_repo")
		if componentToRepoStr != "" {
			pairs := strings.Split(componentToRepoStr, ",")
			result := make(map[string]string)

			for _, pair := range pairs {
				parts := strings.SplitN(pair, "=", 2)
				if len(parts) == 2 {
					result[parts[0]] = parts[1]
				}
			}

			defaultProject.ComponentToRepo = ComponentToRepoMap(result)
		}

		// Handle status transitions from environment
		defaultProject.StatusTransitions = reconstructStatusTransitionsFromEnv(v)

		// Handle project keys from environment (if set)
		projectKeysStr := v.GetString("jira.project_keys")
		if projectKeysStr != "" {
			keys := strings.Split(projectKeysStr, ",")
			var projectKeys []string
			for _, key := range keys {
				if trimmed := strings.TrimSpace(key); trimmed != "" {
					projectKeys = append(projectKeys, trimmed)
				}
			}
			defaultProject.ProjectKeys = ProjectKeys(projectKeys)
		}

		// Handle git PR field name from environment
		gitFieldName := v.GetString("jira.git_pull_request_field_name")
		if gitFieldName != "" {
			defaultProject.GitPullRequestFieldName = gitFieldName
		}

		// Handle disable error comments from environment
		defaultProject.DisableErrorComments = v.GetBool("jira.disable_error_comments")

		// Only add the default project if it has some configuration
		if len(defaultProject.ComponentToRepo) > 0 || len(defaultProject.StatusTransitions) > 0 || len(defaultProject.ProjectKeys) > 0 {
			config.Jira.Projects = []ProjectConfig{defaultProject}
		}
	}

	// Validate configuration (after all fallbacks have been applied)
	if err := config.validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// reconstructStatusTransitionsFromEnv reconstructs status transitions from individual environment variables
// This is used as a fallback when the JSON approach doesn't work in deployment
func reconstructStatusTransitionsFromEnv(v *viper.Viper) TicketTypeStatusTransitions {
	result := make(TicketTypeStatusTransitions)

	// Check for Bug ticket type
	bugTransitions := StatusTransitions{}
	if todo := v.GetString("jira.status_transitions_bug_todo"); todo != "" {
		bugTransitions.Todo = todo
	}
	if inProgress := v.GetString("jira.status_transitions_bug_in_progress"); inProgress != "" {
		bugTransitions.InProgress = inProgress
	}
	if inReview := v.GetString("jira.status_transitions_bug_in_review"); inReview != "" {
		bugTransitions.InReview = inReview
	}
	if bugTransitions.Todo != "" || bugTransitions.InProgress != "" || bugTransitions.InReview != "" {
		result["Bug"] = bugTransitions
	}

	// Check for Story ticket type
	storyTransitions := StatusTransitions{}
	if todo := v.GetString("jira.status_transitions_story_todo"); todo != "" {
		storyTransitions.Todo = todo
	}
	if inProgress := v.GetString("jira.status_transitions_story_in_progress"); inProgress != "" {
		storyTransitions.InProgress = inProgress
	}
	if inReview := v.GetString("jira.status_transitions_story_in_review"); inReview != "" {
		storyTransitions.InReview = inReview
	}
	if storyTransitions.Todo != "" || storyTransitions.InProgress != "" || storyTransitions.InReview != "" {
		result["Story"] = storyTransitions
	}

	// Check for Task ticket type
	taskTransitions := StatusTransitions{}
	if todo := v.GetString("jira.status_transitions_task_todo"); todo != "" {
		taskTransitions.Todo = todo
	}
	if inProgress := v.GetString("jira.status_transitions_task_in_progress"); inProgress != "" {
		taskTransitions.InProgress = inProgress
	}
	if inReview := v.GetString("jira.status_transitions_task_in_review"); inReview != "" {
		taskTransitions.InReview = inReview
	}
	if taskTransitions.Todo != "" || taskTransitions.InProgress != "" || taskTransitions.InReview != "" {
		result["Task"] = taskTransitions
	}

	return result
}

// setDefaults sets all configuration defaults
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.port", 8080)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "console")

	// Jira defaults
	v.SetDefault("jira.interval_seconds", 300)
	v.SetDefault("jira.disable_error_comments", false)

	// GitHub defaults
	v.SetDefault("github.target_branch", "main")
	v.SetDefault("github.pr_label", "ai-pr")
	v.SetDefault("github.max_thread_depth", 5)
	v.SetDefault("github.known_bot_usernames", []string{
		"github-actions",
		"dependabot",
		"renovate",
		"coderabbitai",
		"sourcery-ai",
		"copilot",
		"deepsource-io",
		"codefactor-io",
		"codeclimate",
	})

	// AI Provider defaults
	v.SetDefault("ai_provider", "claude")

	// Workspace defaults
	v.SetDefault("workspaces.ttl_days", 7)

	// Container defaults
	v.SetDefault("container.runtime", "auto")

	// Guardrails defaults
	v.SetDefault("guardrails.max_concurrent_jobs", 10)
	v.SetDefault("guardrails.max_retries", 3)
	v.SetDefault("guardrails.max_container_runtime_minutes", 60)
	v.SetDefault("guardrails.circuit_breaker_threshold", 5)
	v.SetDefault("guardrails.circuit_breaker_window_minutes", 10)
	v.SetDefault("guardrails.circuit_breaker_cooldown_minutes", 5)
}

// validate validates the entire configuration
func (c *Config) validate() error {
	// Validate AI provider configuration
	if c.AIProvider != "claude" && c.AIProvider != "gemini" {
		return errors.New("ai_provider must be either 'claude' or 'gemini'")
	}

	// Validate logging configuration
	if !c.Logging.Level.IsValid() {
		return fmt.Errorf("invalid log level: %s. Valid options are: debug, info, warn, error", c.Logging.Level)
	}
	if !c.Logging.Format.IsValid() {
		return fmt.Errorf("invalid log format: %s. Valid options are: console, json", c.Logging.Format)
	}

	// Validate Jira configuration (required for this application)
	if c.Jira.BaseURL == "" {
		return errors.New("jira.base_url is required")
	}
	if c.Jira.Username == "" {
		return errors.New("jira.username is required")
	}
	if c.Jira.APIToken == "" {
		return errors.New("jira.api_token is required")
	}

	// Validate projects configuration - at least one project must be configured
	if len(c.Jira.Projects) == 0 {
		return errors.New("at least one project must be configured in jira.projects")
	}

	// Validate each project configuration
	for i, project := range c.Jira.Projects {
		if err := project.validate(i); err != nil {
			return err
		}
	}

	// GitHub validation - App credentials required
	if c.GitHub.AppID <= 0 {
		return errors.New("github.app_id must be a positive integer")
	}
	if c.GitHub.PrivateKeyPath == "" {
		return errors.New("github.private_key_path must be provided")
	}
	if _, err := os.Stat(c.GitHub.PrivateKeyPath); os.IsNotExist(err) {
		return fmt.Errorf("github.private_key_path file does not exist: %s", c.GitHub.PrivateKeyPath)
	}

	if c.GitHub.BotUsername == "" {
		return errors.New("github.bot_username is required")
	}

	// Validate bot email can be determined
	if c.GetBotEmail() == "" {
		return errors.New("github.bot_email is required (either set explicitly, or it will be auto-constructed from app_id)")
	}

	// Validate Jira assignee mapping (required for GitHub App fork-based workflow)
	if len(c.Jira.AssigneeToGitHubUsername) == 0 {
		return errors.New("jira.assignee_to_github_username is required: GitHub App mode creates PRs against assignee forks, so all ticket assignees must map to GitHub usernames - tickets must be assigned before processing")
	}

	// Validate bot username doesn't contain characters that could cause issues
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	for _, char := range invalidChars {
		if strings.Contains(c.GitHub.BotUsername, char) {
			return fmt.Errorf("github.bot_username contains invalid character %q - bot username will be used in branch names and must be git-safe", char)
		}
	}

	// Validate known bot usernames don't contain problematic characters
	for _, botUsername := range c.GitHub.KnownBotUsernames {
		for _, char := range invalidChars {
			if strings.Contains(botUsername, char) {
				return fmt.Errorf("github.known_bot_usernames contains username %q with invalid character %q", botUsername, char)
			}
		}
	}

	// Validate workspaces configuration
	if c.Workspaces.BaseDir == "" {
		return errors.New("workspaces.base_dir is required")
	}
	if c.Workspaces.TTLDays <= 0 {
		return errors.New("workspaces.ttl_days must be positive")
	}

	if err := c.Container.validate(); err != nil {
		return err
	}

	if err := c.Guardrails.validate(); err != nil {
		return err
	}

	return nil
}

// validate checks a single project configuration for required fields.
func (p *ProjectConfig) validate(index int) error {
	prefix := fmt.Sprintf("jira.projects[%d]", index)

	if len(p.ProjectKeys) == 0 {
		return fmt.Errorf("%s.project_keys: at least one project key must be configured", prefix)
	}

	for ticketType, transitions := range p.StatusTransitions {
		if transitions.Todo == "" {
			return fmt.Errorf("%s.status_transitions.%s.todo cannot be empty", prefix, ticketType)
		}
		if transitions.InProgress == "" {
			return fmt.Errorf("%s.status_transitions.%s.in_progress cannot be empty", prefix, ticketType)
		}
		if transitions.InReview == "" {
			return fmt.Errorf("%s.status_transitions.%s.in_review cannot be empty", prefix, ticketType)
		}
	}

	if len(p.StatusTransitions) == 0 {
		return fmt.Errorf("%s.status_transitions: at least one ticket type must be configured", prefix)
	}

	if len(p.ComponentToRepo) == 0 {
		return fmt.Errorf("%s.component_to_repo: at least one component_to_repo mapping is required", prefix)
	}

	return nil
}

// validate checks guardrails configuration values.
func (g *GuardrailsConfig) validate() error {
	if g.MaxConcurrentJobs <= 0 {
		return errors.New("guardrails.max_concurrent_jobs must be positive")
	}
	if g.MaxContainerRuntimeMinutes < 0 {
		return errors.New("guardrails.max_container_runtime_minutes must be non-negative")
	}
	if g.CircuitBreakerThreshold < 0 {
		return errors.New("guardrails.circuit_breaker_threshold must be non-negative")
	}
	if g.CircuitBreakerWindowMinutes < 0 {
		return errors.New("guardrails.circuit_breaker_window_minutes must be non-negative")
	}
	if g.CircuitBreakerCooldownMinutes < 0 {
		return errors.New("guardrails.circuit_breaker_cooldown_minutes must be non-negative")
	}
	return nil
}

// validate checks container configuration values.
func (cc *ContainerCfg) validate() error {
	// Empty runtime is treated as "auto" (the default).
	// Valid values must match container.RuntimeAuto/RuntimePodman/RuntimeDocker constants.
	if cc.Runtime != "" {
		validRuntimes := map[string]bool{"auto": true, "podman": true, "docker": true}
		if !validRuntimes[cc.Runtime] {
			return fmt.Errorf("container.runtime must be \"auto\", \"podman\", or \"docker\", got %q", cc.Runtime)
		}
	}
	return nil
}
