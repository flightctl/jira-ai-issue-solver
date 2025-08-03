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

	// Handle the case where status_transitions is a simple struct (backward compatibility)
	if value.Kind == yaml.MappingNode {
		// Check if the first key is one of the expected status transition keys
		for i := 0; i < len(value.Content); i += 2 {
			if i+1 < len(value.Content) {
				key := value.Content[i].Value
				if key == "todo" || key == "in_progress" || key == "in_review" {
					// This is the old format, decode as simple transitions
					var simpleTransitions StatusTransitions
					if err := value.Decode(&simpleTransitions); err != nil {
						return err
					}
					(*t)["default"] = simpleTransitions
					return nil
				}
			}
		}
	}

	// Handle the case where status_transitions is a map of ticket types
	return value.Decode((*map[string]StatusTransitions)(t))
}

// GetStatusTransitions returns the status transitions for a specific ticket type
// Falls back to "default" if the ticket type is not found
func (t TicketTypeStatusTransitions) GetStatusTransitions(ticketType string) StatusTransitions {
	if transitions, exists := t[ticketType]; exists {
		return transitions
	}
	// Fall back to default if the ticket type is not found
	if transitions, exists := t["default"]; exists {
		return transitions
	}
	// Return empty transitions if no default is found
	return StatusTransitions{}
}

// UnmarshalMapstructure implements custom mapstructure decoding for backward compatibility
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
		// Check if this is the old format (has todo, in_progress, in_review keys)
		if _, hasTodo := mapData["todo"]; hasTodo {
			// Old format - convert to default transitions
			transitions := StatusTransitions{}
			if todo, ok := mapData["todo"].(string); ok {
				transitions.Todo = todo
			}
			if inProgress, ok := mapData["in_progress"].(string); ok {
				transitions.InProgress = inProgress
			}
			if inReview, ok := mapData["in_review"].(string); ok {
				transitions.InReview = inReview
			}
			(*t)["default"] = transitions
			return nil
		}

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

type JiraConfig struct {
	BaseURL                 string                      `yaml:"base_url" mapstructure:"base_url"`
	Username                string                      `yaml:"username" mapstructure:"username"`
	APIToken                string                      `yaml:"api_token" mapstructure:"api_token"`
	IntervalSeconds         int                         `yaml:"interval_seconds" mapstructure:"interval_seconds" default:"300"`
	DisableErrorComments    bool                        `yaml:"disable_error_comments" mapstructure:"disable_error_comments" default:"false"`
	GitPullRequestFieldName string                      `yaml:"git_pull_request_field_name" mapstructure:"git_pull_request_field_name"`
	StatusTransitions       TicketTypeStatusTransitions `yaml:"status_transitions" mapstructure:"status_transitions"`
	ProjectKeys             ProjectKeys                 `yaml:"project_keys" mapstructure:"project_keys"`
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
	v.BindEnv("jira.status_transitions")
	v.BindEnv("jira.project_keys")

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

	// AI configuration
	v.BindEnv("ai.generate_documentation")

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

	// Handle component_to_repo parsing manually to preserve case sensitivity
	// Viper's mapstructure tag converts keys to lowercase, so we need to parse this manually
	if configPath != "" {
		// Read the raw YAML file to extract component_to_repo with case sensitivity
		yamlData, err := os.ReadFile(configPath)
		if err == nil {
			var rawConfig map[string]interface{}
			if err := yaml.Unmarshal(yamlData, &rawConfig); err == nil {
				// Handle component_to_repo
				if componentToRepoRaw, exists := rawConfig["component_to_repo"]; exists {
					if componentToRepoMap, ok := componentToRepoRaw.(map[string]interface{}); ok {
						result := make(map[string]string)
						for key, value := range componentToRepoMap {
							if strValue, ok := value.(string); ok {
								result[key] = strValue
							}
						}
						config.ComponentToRepo = ComponentToRepoMap(result)
					}
				}

				// Handle status_transitions for new format
				if jiraRaw, exists := rawConfig["jira"]; exists {
					if jiraMap, ok := jiraRaw.(map[string]interface{}); ok {
						if statusTransitionsRaw, exists := jiraMap["status_transitions"]; exists {
							if statusTransitionsMap, ok := statusTransitionsRaw.(map[string]interface{}); ok {
								// Check if this is the new format (has nested structure)
								hasNestedStructure := false
								for _, value := range statusTransitionsMap {
									if _, ok := value.(map[string]interface{}); ok {
										hasNestedStructure = true
										break
									}
								}

								if hasNestedStructure {
									// New format - parse nested structure
									result := make(TicketTypeStatusTransitions)
									for ticketType, transitionData := range statusTransitionsMap {
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
											result[ticketType] = transitions
										}
									}
									config.Jira.StatusTransitions = result
								}
							}
						}
					}
				}
			}
		}
	}

	// Fallback to environment variable parsing if still empty
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

	// Fallback to individual environment variables for status transitions if still empty
	if len(config.Jira.StatusTransitions) == 0 {
		config.Jira.StatusTransitions = reconstructStatusTransitionsFromEnv(v)
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

	// Validate component to repo mapping (required for functionality)
	if len(c.ComponentToRepo) == 0 {
		return errors.New("at least one component_to_repo mapping is required")
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

	// Validate status transitions - every configured ticket type must have all required status transitions
	for ticketType, transitions := range c.Jira.StatusTransitions {
		if transitions.Todo == "" {
			return fmt.Errorf("jira.status_transitions.%s.todo cannot be empty", ticketType)
		}
		if transitions.InProgress == "" {
			return fmt.Errorf("jira.status_transitions.%s.in_progress cannot be empty", ticketType)
		}
		if transitions.InReview == "" {
			return fmt.Errorf("jira.status_transitions.%s.in_review cannot be empty", ticketType)
		}
	}

	// Ensure at least one ticket type is configured
	if len(c.Jira.StatusTransitions) == 0 {
		return errors.New("at least one ticket type must be configured in jira.status_transitions")
	}

	// Validate project keys - at least one project key must be configured
	if len(c.Jira.ProjectKeys) == 0 {
		return errors.New("at least one project key must be configured in jira.project_keys")
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
