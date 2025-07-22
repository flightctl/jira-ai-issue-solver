package models

import (
	"errors"
	"fmt"
	"strings"

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

// ComponentToRepoMap is a custom type for parsing component_to_repo from environment variables
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

// JiraConfig represents Jira configuration
type JiraConfig struct {
	BaseURL                 string `yaml:"base_url" mapstructure:"base_url"`
	Username                string `yaml:"username" mapstructure:"username"`
	APIToken                string `yaml:"api_token" mapstructure:"api_token"`
	IntervalSeconds         int    `yaml:"interval_seconds" mapstructure:"interval_seconds" default:"300"`
	DisableErrorComments    bool   `yaml:"disable_error_comments" mapstructure:"disable_error_comments" default:"false"`
	GitPullRequestFieldName string `yaml:"git_pull_request_field_name" mapstructure:"git_pull_request_field_name"`
	StatusTransitions       struct {
		Todo       string `yaml:"todo" mapstructure:"todo" default:"To Do"`
		InProgress string `yaml:"in_progress" mapstructure:"in_progress" default:"In Progress"`
		InReview   string `yaml:"in_review" mapstructure:"in_review" default:"In Review"`
	} `yaml:"status_transitions" mapstructure:"status_transitions"`
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

	// Component to Repository mapping
	ComponentToRepo ComponentToRepoMap `yaml:"component_to_repo" mapstructure:"component_to_repo"`

	// Temporary directory for cloning repositories
	TempDir string `yaml:"temp_dir" mapstructure:"temp_dir" default:"/tmp/jira-ai-issue-solver"`
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
	v.BindEnv("jira.base_url")
	v.BindEnv("jira.username")
	v.BindEnv("jira.api_token")
	v.BindEnv("jira.interval_seconds")
	v.BindEnv("jira.disable_error_comments")
	v.BindEnv("jira.git_pull_request_field_name")
	v.BindEnv("jira.status_transitions.todo")
	v.BindEnv("jira.status_transitions.in_progress")
	v.BindEnv("jira.status_transitions.in_review")

	// GitHub configuration
	v.BindEnv("github.personal_access_token")
	v.BindEnv("github.bot_username")
	v.BindEnv("github.bot_email")
	v.BindEnv("github.target_branch")
	v.BindEnv("github.pr_label")
	v.BindEnv("github.ssh_key_path")

	// AI configuration
	v.BindEnv("ai_provider")

	// Claude configuration
	v.BindEnv("claude.cli_path")
	v.BindEnv("claude.timeout")
	v.BindEnv("claude.dangerously_skip_permissions")
	v.BindEnv("claude.allowed_tools")
	v.BindEnv("claude.disallowed_tools")

	// Gemini configuration
	v.BindEnv("gemini.cli_path")
	v.BindEnv("gemini.timeout")
	v.BindEnv("gemini.model")
	v.BindEnv("gemini.all_files")
	v.BindEnv("gemini.sandbox")
	v.BindEnv("gemini.api_key")

	// Server configuration
	v.BindEnv("server.port")
	v.BindEnv("PORT")

	// Logging configuration
	v.BindEnv("logging.level")
	v.BindEnv("logging.format")

	// Other configuration
	v.BindEnv("temp_dir")
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
	}

	// Unmarshal into struct
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Handle component_to_repo parsing manually if it's empty
	if len(config.ComponentToRepo) == 0 {
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

			config.ComponentToRepo = ComponentToRepoMap(result)
		}
	}

	// Validate configuration
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
	v.SetDefault("jira.status_transitions.todo", "To Do")
	v.SetDefault("jira.status_transitions.in_progress", "In Progress")
	v.SetDefault("jira.status_transitions.in_review", "In Review")

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

	// Validate component to repo mapping (required for functionality)
	if len(c.ComponentToRepo) == 0 {
		return errors.New("at least one component_to_repo mapping is required")
	}

	// Only validate Jira and GitHub configs if they're provided (for debugging flexibility)
	if c.Jira.BaseURL != "" || c.Jira.Username != "" || c.Jira.APIToken != "" {
		// If any Jira config is provided, validate all required fields
		if c.Jira.BaseURL == "" {
			return errors.New("jira.base_url is required when Jira configuration is provided")
		}
		if c.Jira.Username == "" {
			return errors.New("jira.username is required when Jira configuration is provided")
		}
		if c.Jira.APIToken == "" {
			return errors.New("jira.api_token is required when Jira configuration is provided")
		}

		// Validate status transitions if Jira is configured
		if c.Jira.StatusTransitions.Todo == "" {
			return errors.New("jira.status_transitions.todo cannot be empty")
		}
		if c.Jira.StatusTransitions.InProgress == "" {
			return errors.New("jira.status_transitions.in_progress cannot be empty")
		}
		if c.Jira.StatusTransitions.InReview == "" {
			return errors.New("jira.status_transitions.in_review cannot be empty")
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
