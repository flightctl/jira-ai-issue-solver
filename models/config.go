package models

import (
	"errors"
	"fmt"
	"os"
	"strings"

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

// Config represents the application configuration
type Config struct {
	// Server configuration
	Server struct {
		Port int `yaml:"port" default:"8080"`
	} `yaml:"server"`

	// Logging configuration
	Logging struct {
		Level  LogLevel  `yaml:"level" default:"info"`
		Format LogFormat `yaml:"format" default:"console"`
	} `yaml:"logging"`

	// Jira configuration
	Jira struct {
		BaseURL                 string `yaml:"base_url"`
		Username                string `yaml:"username"`
		APIToken                string `yaml:"api_token"`
		IntervalSeconds         int    `yaml:"interval_seconds" default:"300"`
		DisableErrorComments    bool   `yaml:"disable_error_comments" default:"false"`
		GitPullRequestFieldName string `yaml:"git_pull_request_field_name"`
		StatusTransitions       struct {
			Todo       string `yaml:"todo" default:"To Do"`
			InProgress string `yaml:"in_progress" default:"In Progress"`
			InReview   string `yaml:"in_review" default:"In Review"`
		} `yaml:"status_transitions"`
	} `yaml:"jira"`

	// GitHub configuration
	GitHub struct {
		PersonalAccessToken string `yaml:"personal_access_token"`
		BotUsername         string `yaml:"bot_username"`
		BotEmail            string `yaml:"bot_email"`
		TargetBranch        string `yaml:"target_branch" default:"main"`
		PRLabel             string `yaml:"pr_label" default:"ai-pr"`
		SSHKeyPath          string `yaml:"ssh_key_path"` // Path to SSH private key for commit signing
	} `yaml:"github"`

	// AI Provider selection
	AIProvider string `yaml:"ai_provider" default:"claude"` // "claude" or "gemini"

	// Claude CLI configuration
	Claude struct {
		CLIPath                    string `yaml:"cli_path" default:"claude-cli"`
		Timeout                    int    `yaml:"timeout" default:"300"`
		DangerouslySkipPermissions bool   `yaml:"dangerously_skip_permissions" default:"false"`
		AllowedTools               string `yaml:"allowed_tools" default:"Bash Edit"`
		DisallowedTools            string `yaml:"disallowed_tools" default:"Python"`
	} `yaml:"claude"`

	// Gemini CLI configuration
	Gemini struct {
		CLIPath  string `yaml:"cli_path" default:"gemini"`
		Timeout  int    `yaml:"timeout" default:"300"`
		Model    string `yaml:"model" default:"gemini-2.5-pro"`
		AllFiles bool   `yaml:"all_files" default:"false"`
		Sandbox  bool   `yaml:"sandbox" default:"false"`
		APIKey   string `yaml:"api_key"`
	} `yaml:"gemini"`

	// Component to Repository mapping
	ComponentToRepo map[string]string `yaml:"component_to_repo"`

	// Temporary directory for cloning repositories
	TempDir string `yaml:"temp_dir" default:"/tmp/jira-ai-issue-solver"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(configPath string) (*Config, error) {
	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Set default for TargetBranch if not set
	if config.GitHub.TargetBranch == "" {
		config.GitHub.TargetBranch = "main"
	}

	// Validate AI provider configuration
	if err := config.validateAIProvider(); err != nil {
		return nil, err
	}

	// Validate status transitions configuration
	if err := config.validateStatusTransitions(); err != nil {
		return nil, err
	}

	// Validate logging configuration
	if err := config.validateLogging(); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateAIProvider ensures only one AI provider is configured
func (c *Config) validateAIProvider() error {
	if c.AIProvider != "claude" && c.AIProvider != "gemini" {
		return errors.New("ai_provider must be either 'claude' or 'gemini'")
	}
	return nil
}

// validateStatusTransitions ensures status transitions are properly configured
func (c *Config) validateStatusTransitions() error {
	if c.Jira.StatusTransitions.Todo == "" {
		return errors.New("jira.status_transitions.todo cannot be empty")
	}
	if c.Jira.StatusTransitions.InProgress == "" {
		return errors.New("jira.status_transitions.in_progress cannot be empty")
	}
	if c.Jira.StatusTransitions.InReview == "" {
		return errors.New("jira.status_transitions.in_review cannot be empty")
	}
	return nil
}

// validateLogging ensures logging configuration is valid
func (c *Config) validateLogging() error {
	if !c.Logging.Level.IsValid() {
		return fmt.Errorf("invalid log level: %s. Valid options are: debug, info, warn, error", c.Logging.Level)
	}
	if !c.Logging.Format.IsValid() {
		return fmt.Errorf("invalid log format: %s. Valid options are: console, json", c.Logging.Format)
	}
	return nil
}
