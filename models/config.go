package models

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ComponentToRepoMap is a custom type for parsing component_to_repo from YAML
type ComponentToRepoMap map[string]string

// ProjectKeys is a custom type for project keys
type ProjectKeys []string

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
	RepoLabelName           string                      `yaml:"repo_label_name" mapstructure:"repo_label_name"`
	DisableErrorComments    bool                        `yaml:"disable_error_comments" mapstructure:"disable_error_comments" default:"false"`
}

type JiraConfig struct {
	BaseURL         string          `yaml:"base_url" mapstructure:"base_url"`
	Username        string          `yaml:"username" mapstructure:"username"`
	APIToken        string          `yaml:"api_token" mapstructure:"api_token"`
	IntervalSeconds int             `yaml:"interval_seconds" mapstructure:"interval_seconds" default:"300"`
	Projects        []ProjectConfig `yaml:"projects" mapstructure:"projects"`
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
		PersonalAccessToken string `yaml:"personal_access_token" mapstructure:"personal_access_token"`
		BotUsername         string `yaml:"bot_username" mapstructure:"bot_username"`
		BotEmail            string `yaml:"bot_email" mapstructure:"bot_email"`
		TargetBranch        string `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
		PRLabel             string `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
		SSHKeyPath          string `yaml:"ssh_key_path" mapstructure:"ssh_key_path"` // Path to SSH private key for commit signing
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
	// - JIRA_AI_GITHUB_PERSONAL_ACCESS_TOKEN → github.personal_access_token

	// Explicit bindings for all environment variables (automatic mapping doesn't work with nested structs)
	// Jira configuration
	_ = v.BindEnv("jira.base_url")
	_ = v.BindEnv("jira.username")
	_ = v.BindEnv("jira.api_token")
	_ = v.BindEnv("jira.interval_seconds")

	// GitHub configuration
	_ = v.BindEnv("github.personal_access_token")
	_ = v.BindEnv("github.bot_username")
	_ = v.BindEnv("github.bot_email")
	_ = v.BindEnv("github.target_branch")
	_ = v.BindEnv("github.pr_label")
	_ = v.BindEnv("github.ssh_key_path")

	// AI configuration
	_ = v.BindEnv("ai_provider")

	// Claude configuration
	_ = v.BindEnv("claude.cli_path")
	_ = v.BindEnv("claude.timeout")
	_ = v.BindEnv("claude.dangerously_skip_permissions")
	_ = v.BindEnv("claude.allowed_tools")
	_ = v.BindEnv("claude.disallowed_tools")

	// Gemini configuration
	_ = v.BindEnv("gemini.cli_path")
	_ = v.BindEnv("gemini.timeout")
	_ = v.BindEnv("gemini.model")
	_ = v.BindEnv("gemini.all_files")
	_ = v.BindEnv("gemini.sandbox")
	_ = v.BindEnv("gemini.api_key")

	// AI configuration
	_ = v.BindEnv("ai.generate_documentation")

	// Server configuration
	_ = v.BindEnv("server.port")
	_ = v.BindEnv("PORT")

	// Logging configuration
	_ = v.BindEnv("logging.level")
	_ = v.BindEnv("logging.format")

	// Other configuration
	_ = v.BindEnv("temp_dir")

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
	// Load .env file if it exists - ignore errors since it's optional
	_ = v.ReadInConfig()

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

	// Validate configuration (after all fallbacks have been applied)
	if err := config.validate(); err != nil {
		return nil, err
	}

	return &config, nil
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

		// Validate that either component mapping or repo label name is configured
		if len(project.ComponentToRepo) == 0 && project.RepoLabelName == "" {
			return fmt.Errorf("%s: either component_to_repo mappings or repo_label_name must be configured", projectPrefix)
		}
	}

	if c.GitHub.PersonalAccessToken != "" || c.GitHub.BotUsername != "" || c.GitHub.BotEmail != "" {
		// If any GitHub config is provided, validate all required fields
		if c.GitHub.PersonalAccessToken == "" {
			return errors.New("github.personal_access_token is required when GitHub configuration is provided")
		}
		if c.GitHub.BotUsername == "" {
			return errors.New("github.bot_username is required when GitHub configuration is provided")
		}
		if c.GitHub.BotEmail == "" {
			return errors.New("github.bot_email is required when GitHub configuration is provided")
		}
	}

	return nil
}
