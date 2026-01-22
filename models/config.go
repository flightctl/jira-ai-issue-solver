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
	} `yaml:"github" mapstructure:"github"`

	// AI Provider selection
	AIProvider string `yaml:"ai_provider" mapstructure:"ai_provider" default:"claude"` // "claude" or "gemini"

	// Claude CLI configuration
	Claude struct {
		CLIPath                    string `yaml:"cli_path" mapstructure:"cli_path" default:"claude-cli"`
		Timeout                    int    `yaml:"timeout" mapstructure:"timeout" default:"300"`
		DangerouslySkipPermissions bool   `yaml:"dangerously_skip_permissions" mapstructure:"dangerously_skip_permissions" default:"false"`
		AllowedTools               string `yaml:"allowed_tools" mapstructure:"allowed_tools" default:"Bash Edit"`
		DisallowedTools            string `yaml:"disallowed_tools" mapstructure:"disallowed_tools" default:"Python"`
		APIKey                     string `yaml:"api_key" mapstructure:"api_key"` // Anthropic API key for headless/container environments
	} `yaml:"claude" mapstructure:"claude"`

	// Gemini CLI configuration
	Gemini struct {
		CLIPath  string `yaml:"cli_path" mapstructure:"cli_path" default:"gemini"`
		Timeout  int    `yaml:"timeout" mapstructure:"timeout" default:"300"`
		Model    string `yaml:"model" mapstructure:"model" default:"gemini-2.5-pro"`
		AllFiles bool   `yaml:"all_files" mapstructure:"all_files" default:"false"`
		Sandbox  bool   `yaml:"sandbox" mapstructure:"sandbox" default:"false"`
		APIKey   string `yaml:"api_key" mapstructure:"api_key"`
	} `yaml:"gemini" mapstructure:"gemini"`

	// AI configuration
	AI struct {
		GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
		MaxRetries            int  `yaml:"max_retries" mapstructure:"max_retries" default:"5"`                 // Maximum number of times to retry AI code generation if no changes are detected
		RetryDelaySeconds     int  `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds" default:"2"` // Delay in seconds between AI retries
	} `yaml:"ai" mapstructure:"ai"`

	// Temporary directory for cloning repositories
	TempDir string `yaml:"temp_dir" mapstructure:"temp_dir" default:"/tmp/jira-ai-issue-solver"`
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

	// AI configuration
	bindEnv("ai_provider")

	// Claude configuration
	bindEnv("claude.cli_path")
	bindEnv("claude.timeout")
	bindEnv("claude.dangerously_skip_permissions")
	bindEnv("claude.allowed_tools")
	bindEnv("claude.disallowed_tools")

	// Gemini configuration
	bindEnv("gemini.cli_path")
	bindEnv("gemini.timeout")
	bindEnv("gemini.model")
	bindEnv("gemini.all_files")
	bindEnv("gemini.sandbox")
	bindEnv("gemini.api_key")

	// AI configuration
	bindEnv("ai.generate_documentation")
	bindEnv("ai.max_retries")
	bindEnv("ai.retry_delay_seconds")

	// Server configuration
	bindEnv("server.port")
	bindEnv("PORT")

	// Logging configuration
	bindEnv("logging.level")
	bindEnv("logging.format")

	// Other configuration
	bindEnv("temp_dir")
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

	// Claude defaults
	v.SetDefault("claude.cli_path", "claude")
	v.SetDefault("claude.timeout", 300)
	v.SetDefault("claude.dangerously_skip_permissions", false)
	v.SetDefault("claude.allowed_tools", "Bash Edit")
	v.SetDefault("claude.disallowed_tools", "Python")

	// Gemini defaults
	v.SetDefault("gemini.cli_path", "gemini")
	v.SetDefault("gemini.timeout", 300)
	v.SetDefault("gemini.model", "gemini-2.5-pro")
	v.SetDefault("gemini.all_files", false)
	v.SetDefault("gemini.sandbox", false)

	// AI defaults
	v.SetDefault("ai.generate_documentation", true)
	v.SetDefault("ai.max_retries", 5)
	v.SetDefault("ai.retry_delay_seconds", 2)

	// Temp directory defaults
	v.SetDefault("temp_dir", "/tmp/jira-ai-issue-solver")
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
		projectPrefix := fmt.Sprintf("jira.projects[%d]", i)

		// Validate project keys - at least one project key must be configured per project
		if len(project.ProjectKeys) == 0 {
			return fmt.Errorf("%s.project_keys: at least one project key must be configured", projectPrefix)
		}

		// Validate status transitions - every configured ticket type must have all required status transitions
		for ticketType, transitions := range project.StatusTransitions {
			if transitions.Todo == "" {
				return fmt.Errorf("%s.status_transitions.%s.todo cannot be empty", projectPrefix, ticketType)
			}
			if transitions.InProgress == "" {
				return fmt.Errorf("%s.status_transitions.%s.in_progress cannot be empty", projectPrefix, ticketType)
			}
			if transitions.InReview == "" {
				return fmt.Errorf("%s.status_transitions.%s.in_review cannot be empty", projectPrefix, ticketType)
			}
		}

		// Ensure at least one ticket type is configured per project
		if len(project.StatusTransitions) == 0 {
			return fmt.Errorf("%s.status_transitions: at least one ticket type must be configured", projectPrefix)
		}

		// Validate component to repo mapping (required for functionality)
		if len(project.ComponentToRepo) == 0 {
			return fmt.Errorf("%s.component_to_repo: at least one component_to_repo mapping is required", projectPrefix)
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

	// Validate Jira assignee mapping
	if len(c.Jira.AssigneeToGitHubUsername) == 0 {
		return errors.New("jira.assignee_to_github_username is required (needed to map assignees to forks)")
	}

	// Validate AI configuration
	if c.AI.MaxRetries < 1 {
		return errors.New("ai.max_retries must be at least 1")
	}
	if c.AI.RetryDelaySeconds < 0 {
		return errors.New("ai.retry_delay_seconds must be non-negative")
	}

	return nil
}
