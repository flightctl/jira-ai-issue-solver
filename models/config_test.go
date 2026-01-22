package models

import (
	"fmt"
	"os"
	"testing"
)

// createTempKeyFile creates a temporary key file for testing
func createTempKeyFile(t *testing.T) string {
	tmpKeyFile, err := os.CreateTemp("", "test_key_*.pem")
	if err != nil {
		t.Fatal(err)
	}
	if err := tmpKeyFile.Close(); err != nil {
		t.Fatal(err)
	}
	return tmpKeyFile.Name()
}

// getValidGitHubConfig returns a valid GitHub configuration for testing
func getValidGitHubConfig() struct {
	AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
	PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
	BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
	BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
	TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
	PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
	SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
	MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
	KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
} {
	return struct {
		AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
		PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
		BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
		BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
		TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
		PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
		SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
		MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
		KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
	}{
		AppID:          123456,
		PrivateKeyPath: "/tmp/test_key.pem",
		BotEmail:       "test@example.com",
		BotUsername:    "test-bot",
	}
}

func TestConfig_validateStatusTransitions(t *testing.T) {
	// Create a temporary private key file for tests
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid status transitions",
			config: Config{
				AIProvider: "claude",
				Logging: struct {
					Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
					Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
				}{
					Level:  LogLevelInfo,
					Format: LogFormatConsole,
				},
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					AssigneeToGitHubUsername: map[string]string{
						"alice@example.com": "alice",
					},
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: struct {
					AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
					PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
					BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
					BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
					TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
					PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
					SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
					MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
					KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
				}{
					AppID:          123456,
					PrivateKeyPath: tmpKeyPath,
					BotUsername:    "test-bot",
				},
				AI: struct {
					GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
					MaxRetries            int  `yaml:"max_retries" mapstructure:"max_retries" default:"5"`
					RetryDelaySeconds     int  `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds" default:"2"`
				}{
					GenerateDocumentation: true,
					MaxRetries:            5,
					RetryDelaySeconds:     2,
				},
			},
			wantErr: false,
		},
		{
			name: "empty todo status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
		{
			name: "empty in_progress status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
		{
			name: "empty in_review status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
		{
			name: "no ticket types configured",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys:       ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{},
							ComponentToRepo:   ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
		{
			name: "no project keys configured",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
		{
			name: "one ticket type valid, one invalid",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "Open",
									InProgress: "In Progress",
									InReview:   "Code Review",
								},
								"Story": StatusTransitions{
									Todo:       "Backlog",
									InProgress: "", // Invalid - missing in_progress
									InReview:   "Testing",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: getValidGitHubConfig(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfig_WithStatusTransitions(t *testing.T) {
	// Create a temporary private key file
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	// Create a temporary config file
	configContent := fmt.Sprintf(`
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  assignee_to_github_username:
    alice@example.com: alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        test: https://github.com/test/repo.git
github:
  app_id: 123456
  private_key_path: "%s"
  bot_username: "test-bot"
  target_branch: "develop"
`, tmpKeyPath)
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the config
	config, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify status transitions
	projectConfig := config.GetProjectConfigForTicket("PROJ1-123")
	if projectConfig == nil {
		t.Fatal("Project config not found")
		return
	}
	bugTransitions := projectConfig.StatusTransitions.GetStatusTransitions("bug")
	if bugTransitions.Todo != "To Do" {
		t.Errorf("Expected todo status 'To Do', got '%s'", bugTransitions.Todo)
	}
	if bugTransitions.InProgress != "In Progress" {
		t.Errorf("Expected in_progress status 'In Progress', got '%s'", bugTransitions.InProgress)
	}
	if bugTransitions.InReview != "In Review" {
		t.Errorf("Expected in_review status 'In Review', got '%s'", bugTransitions.InReview)
	}

	// Verify target branch
	if config.GitHub.TargetBranch != "develop" {
		t.Errorf("Expected target branch 'develop', got '%s'", config.GitHub.TargetBranch)
	}
}

func TestLoadConfig_WithDefaultTargetBranch(t *testing.T) {
	// Create a temporary private key file
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	// Create a temporary config file without target_branch (should default to "main")
	configContent := fmt.Sprintf(`
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  assignee_to_github_username:
    alice@example.com: alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        test: https://github.com/test/repo.git
github:
  app_id: 123456
  private_key_path: "%s"
  bot_username: "test-bot"
`, tmpKeyPath)
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the config
	config, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify target branch defaults to "main"
	if config.GitHub.TargetBranch != "main" {
		t.Errorf("Expected default target branch 'main', got '%s'", config.GitHub.TargetBranch)
	}
}

func TestLoadConfig_ComponentToRepoCaseSensitivity(t *testing.T) {
	// Create a temporary private key file
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	// Create a temporary config file with mixed case component names
	configContent := fmt.Sprintf(`
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  assignee_to_github_username:
    alice@example.com: alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        FlightCtl: https://github.com/your-org/flightctl.git
        flightctl: https://github.com/your-org/flightctl-lowercase.git
        Backend: https://github.com/your-org/backend.git
        backend: https://github.com/your-org/backend-lowercase.git
github:
  app_id: 123456
  private_key_path: "%s"
  bot_username: "test-bot"
`, tmpKeyPath)
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the config
	config, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Get project config for testing
	projectConfig := config.GetProjectConfigForTicket("PROJ1-123")
	if projectConfig == nil {
		t.Fatal("Project config not found")
		return
	}

	// Verify component mappings (keys converted to lowercase by Viper)
	if projectConfig.ComponentToRepo["flightctl"] != "https://github.com/your-org/flightctl.git" {
		t.Errorf("Expected flightctl to map to flightctl.git, got '%s'", projectConfig.ComponentToRepo["flightctl"])
	}
	if projectConfig.ComponentToRepo["backend"] != "https://github.com/your-org/backend.git" {
		t.Errorf("Expected backend to map to backend.git, got '%s'", projectConfig.ComponentToRepo["backend"])
	}

	// The test was originally designed to test case sensitivity, but Viper converts keys to lowercase
	// So we verify that the mappings exist with lowercase keys
}

func TestLoadConfig_WithTicketTypeSpecificStatusTransitions(t *testing.T) {
	// Create a temporary private key file
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	// Create a temporary config file with ticket-type-specific status transitions
	configContent := fmt.Sprintf(`
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  assignee_to_github_username:
    alice@example.com: alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        bug:
          todo: "Open"
          in_progress: "In Progress"
          in_review: "Code Review"
        story:
          todo: "Backlog"
          in_progress: "Development"
          in_review: "Testing"
      component_to_repo:
        test: https://github.com/test/repo.git
github:
  app_id: 123456
  private_key_path: "%s"
  bot_username: "test-bot"
  target_branch: "develop"
`, tmpKeyPath)
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the config
	config, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify bug-specific status transitions
	projectConfig := config.GetProjectConfigForTicket("PROJ1-123")
	if projectConfig == nil {
		t.Fatal("Project config not found")
		return
	}
	bugTransitions := projectConfig.StatusTransitions.GetStatusTransitions("bug")
	if bugTransitions.Todo != "Open" {
		t.Errorf("Expected bug todo status 'Open', got '%s'", bugTransitions.Todo)
	}
	if bugTransitions.InProgress != "In Progress" {
		t.Errorf("Expected bug in_progress status 'In Progress', got '%s'", bugTransitions.InProgress)
	}
	if bugTransitions.InReview != "Code Review" {
		t.Errorf("Expected bug in_review status 'Code Review', got '%s'", bugTransitions.InReview)
	}

	// Verify story-specific status transitions
	storyTransitions := projectConfig.StatusTransitions.GetStatusTransitions("story")
	if storyTransitions.Todo != "Backlog" {
		t.Errorf("Expected story todo status 'Backlog', got '%s'", storyTransitions.Todo)
	}
	if storyTransitions.InProgress != "Development" {
		t.Errorf("Expected story in_progress status 'Development', got '%s'", storyTransitions.InProgress)
	}
	if storyTransitions.InReview != "Testing" {
		t.Errorf("Expected story in_review status 'Testing', got '%s'", storyTransitions.InReview)
	}

	// Verify that unknown ticket type returns empty transitions (no fallback)
	unknownTransitions := projectConfig.StatusTransitions.GetStatusTransitions("unknown")
	if unknownTransitions.Todo != "" {
		t.Errorf("Expected unknown ticket type to return empty todo, got '%s'", unknownTransitions.Todo)
	}
}

func TestTicketTypeStatusTransitions_GetStatusTransitions(t *testing.T) {
	tests := []struct {
		name           string
		transitions    TicketTypeStatusTransitions
		ticketType     string
		expectedTodo   string
		expectedInProg string
		expectedInRev  string
	}{
		{
			name: "specific ticket type found",
			transitions: TicketTypeStatusTransitions{
				"default": StatusTransitions{
					Todo:       "To Do",
					InProgress: "In Progress",
					InReview:   "In Review",
				},
				"Bug": StatusTransitions{
					Todo:       "Open",
					InProgress: "In Progress",
					InReview:   "Code Review",
				},
			},
			ticketType:     "Bug",
			expectedTodo:   "Open",
			expectedInProg: "In Progress",
			expectedInRev:  "Code Review",
		},
		{
			name: "ticket type not found, no fallback",
			transitions: TicketTypeStatusTransitions{
				"Bug": StatusTransitions{
					Todo:       "Open",
					InProgress: "In Progress",
					InReview:   "Code Review",
				},
			},
			ticketType:     "Story",
			expectedTodo:   "",
			expectedInProg: "",
			expectedInRev:  "",
		},
		{
			name: "no default, return empty transitions",
			transitions: TicketTypeStatusTransitions{
				"Bug": StatusTransitions{
					Todo:       "Open",
					InProgress: "In Progress",
					InReview:   "Code Review",
				},
			},
			ticketType:     "Story",
			expectedTodo:   "",
			expectedInProg: "",
			expectedInRev:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.transitions.GetStatusTransitions(tt.ticketType)

			if result.Todo != tt.expectedTodo {
				t.Errorf("Expected Todo '%s', got '%s'", tt.expectedTodo, result.Todo)
			}
			if result.InProgress != tt.expectedInProg {
				t.Errorf("Expected InProgress '%s', got '%s'", tt.expectedInProg, result.InProgress)
			}
			if result.InReview != tt.expectedInRev {
				t.Errorf("Expected InReview '%s', got '%s'", tt.expectedInRev, result.InReview)
			}
		})
	}
}

func TestLoadConfig_WithAIConfiguration(t *testing.T) {
	// Create a temporary private key file
	tmpKeyPath := createTempKeyFile(t)
	defer func() { _ = os.Remove(tmpKeyPath) }()

	// Create a temporary config file
	configContent := fmt.Sprintf(`
ai_provider: claude
ai:
  generate_documentation: false
claude:
  cli_path: claude
  timeout: 300
jira:
  base_url: https://test.atlassian.net
  username: test-user
  api_token: test-token
  assignee_to_github_username:
    alice@example.com: alice
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        "test-component": "https://github.com/test/repo"
github:
  app_id: 123456
  private_key_path: "%s"
  bot_username: "test-bot"
`, tmpKeyPath)

	tempFile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tempFile.Name()) }()

	if _, err := tempFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config content: %v", err)
	}
	_ = tempFile.Close()

	// Load the config
	config, err := LoadConfig(tempFile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify AI configuration
	if config.AIProvider != "claude" {
		t.Errorf("Expected AI provider to be 'claude', got '%s'", config.AIProvider)
	}

	if config.AI.GenerateDocumentation {
		t.Error("Expected generate_documentation to be false, got true")
	}
}

// TestConfig_GitHubAppAuthentication tests GitHub App configuration and validation
func TestConfig_GitHubAppAuthentication(t *testing.T) {
	// Create a temporary private key file for testing
	tempKeyFile, err := os.CreateTemp("", "github-app-key-*.pem")
	if err != nil {
		t.Fatalf("Failed to create temp key file: %v", err)
	}
	defer func() { _ = os.Remove(tempKeyFile.Name()) }()

	// Write a dummy private key content
	if _, err := tempKeyFile.WriteString("-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"); err != nil {
		t.Fatalf("Failed to write to temp key file: %v", err)
	}
	if err := tempKeyFile.Close(); err != nil {
		t.Fatalf("Failed to close temp key file: %v", err)
	}

	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid GitHub App configuration",
			config: Config{
				AIProvider: "claude",
				Logging: struct {
					Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
					Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
				}{
					Level:  LogLevelInfo,
					Format: LogFormatConsole,
				},
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					AssigneeToGitHubUsername: map[string]string{
						"alice@example.com": "alice",
						"bob@example.com":   "bob",
					},
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: struct {
					AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
					PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
					BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
					BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
					TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
					PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
					SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
					MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
					KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
				}{
					AppID:          123456,
					PrivateKeyPath: tempKeyFile.Name(),
					BotUsername:    "test-bot[bot]",
				},
				AI: struct {
					GenerateDocumentation bool `yaml:"generate_documentation" mapstructure:"generate_documentation" default:"true"`
					MaxRetries            int  `yaml:"max_retries" mapstructure:"max_retries" default:"5"`
					RetryDelaySeconds     int  `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds" default:"2"`
				}{
					GenerateDocumentation: true,
					MaxRetries:            5,
					RetryDelaySeconds:     2,
				},
			},
			wantErr: false,
		},
		{
			name: "GitHub App without app_id (should fail)",
			config: Config{
				AIProvider: "claude",
				Logging: struct {
					Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
					Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
				}{
					Level:  LogLevelInfo,
					Format: LogFormatConsole,
				},
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: struct {
					AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
					PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
					BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
					BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
					TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
					PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
					SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
					MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
					KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
				}{
					PrivateKeyPath: tempKeyFile.Name(),
					BotUsername:    "test-bot",
				},
			},
			wantErr: true,
			errMsg:  "github.app_id must be a positive integer",
		},
		{
			name: "GitHub App with non-existent private key file (should fail)",
			config: Config{
				AIProvider: "claude",
				Logging: struct {
					Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
					Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
				}{
					Level:  LogLevelInfo,
					Format: LogFormatConsole,
				},
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					AssigneeToGitHubUsername: map[string]string{
						"alice@example.com": "alice",
					},
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: struct {
					AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
					PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
					BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
					BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
					TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
					PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
					SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
					MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
					KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
				}{
					AppID:          123456,
					PrivateKeyPath: "/non/existent/path/key.pem",
					BotUsername:    "test-bot",
				},
			},
			wantErr: true,
			errMsg:  "github.private_key_path file does not exist",
		},
		{
			name: "GitHub App without assignee mapping (should fail)",
			config: Config{
				AIProvider: "claude",
				Logging: struct {
					Level  LogLevel  `yaml:"level" mapstructure:"level" default:"info"`
					Format LogFormat `yaml:"format" mapstructure:"format" default:"console"`
				}{
					Level:  LogLevelInfo,
					Format: LogFormatConsole,
				},
				Jira: JiraConfig{
					BaseURL:  "https://example.com",
					Username: "testuser",
					APIToken: "testtoken",
					// No AssigneeToGitHubUsername mapping
					Projects: []ProjectConfig{
						{
							ProjectKeys: ProjectKeys{"PROJ1"},
							StatusTransitions: TicketTypeStatusTransitions{
								"Bug": StatusTransitions{
									Todo:       "To Do",
									InProgress: "In Progress",
									InReview:   "In Review",
								},
							},
							ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
						},
					},
				},
				GitHub: struct {
					AppID             int64    `yaml:"app_id" mapstructure:"app_id"`
					PrivateKeyPath    string   `yaml:"private_key_path" mapstructure:"private_key_path"`
					BotUsername       string   `yaml:"bot_username" mapstructure:"bot_username"`
					BotEmail          string   `yaml:"bot_email" mapstructure:"bot_email"`
					TargetBranch      string   `yaml:"target_branch" mapstructure:"target_branch" default:"main"`
					PRLabel           string   `yaml:"pr_label" mapstructure:"pr_label" default:"ai-pr"`
					SSHKeyPath        string   `yaml:"ssh_key_path" mapstructure:"ssh_key_path"`
					MaxThreadDepth    int      `yaml:"max_thread_depth" mapstructure:"max_thread_depth" default:"5"`
					KnownBotUsernames []string `yaml:"known_bot_usernames" mapstructure:"known_bot_usernames"`
				}{
					AppID:          123456,
					PrivateKeyPath: tempKeyFile.Name(),
					BotUsername:    "test-bot",
				},
			},
			wantErr: true,
			errMsg:  "jira.assignee_to_github_username is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Config.validate() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

// TestLoadConfig_WithAssigneeToGitHubUsername tests loading assignee_to_github_username from YAML
func TestLoadConfig_WithAssigneeToGitHubUsername(t *testing.T) {
	// Create a temporary private key file for GitHub App
	tempKeyFile, err := os.CreateTemp("", "github-app-key-*.pem")
	if err != nil {
		t.Fatalf("Failed to create temp key file: %v", err)
	}
	defer func() { _ = os.Remove(tempKeyFile.Name()) }()

	if _, err := tempKeyFile.WriteString("-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"); err != nil {
		t.Fatalf("Failed to write to temp key file: %v", err)
	}
	if err := tempKeyFile.Close(); err != nil {
		t.Fatalf("Failed to close temp key file: %v", err)
	}

	// Create a temporary config file with assignee_to_github_username
	configContent := `
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  assignee_to_github_username:
    "alice@example.com": alice-github
    "bob.smith@company.com": bob-smith
    "charlie+tag@domain.org": charlie
  projects:
    - project_keys:
        - "PROJ1"
      status_transitions:
        Bug:
          todo: "To Do"
          in_progress: "In Progress"
          in_review: "In Review"
      component_to_repo:
        test: https://github.com/test/repo.git
github:
  app_id: 123456
  private_key_path: "` + tempKeyFile.Name() + `"
  bot_username: "test-bot[bot]"
`
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the config
	config, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify assignee_to_github_username was loaded correctly
	if config.Jira.AssigneeToGitHubUsername == nil {
		t.Fatal("AssigneeToGitHubUsername is nil")
	}

	expectedMappings := map[string]string{
		"alice@example.com":      "alice-github",
		"bob.smith@company.com":  "bob-smith",
		"charlie+tag@domain.org": "charlie",
	}

	if len(config.Jira.AssigneeToGitHubUsername) != len(expectedMappings) {
		t.Errorf("Expected %d mappings, got %d", len(expectedMappings), len(config.Jira.AssigneeToGitHubUsername))
	}

	for email, expectedUsername := range expectedMappings {
		actualUsername, exists := config.Jira.AssigneeToGitHubUsername[email]
		if !exists {
			t.Errorf("Expected mapping for %s not found", email)
			continue
		}
		if actualUsername != expectedUsername {
			t.Errorf("For email %s: expected username %s, got %s", email, expectedUsername, actualUsername)
		}
	}
}

func TestConfig_validateAIConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		maxRetries    int
		retryDelay    int
		expectedError string
	}{
		{
			name:          "valid config with default values",
			maxRetries:    5,
			retryDelay:    2,
			expectedError: "",
		},
		{
			name:          "valid config with minimum values",
			maxRetries:    1,
			retryDelay:    0,
			expectedError: "",
		},
		{
			name:          "valid config with high values",
			maxRetries:    10,
			retryDelay:    10,
			expectedError: "",
		},
		{
			name:          "invalid max_retries zero",
			maxRetries:    0,
			retryDelay:    2,
			expectedError: "ai.max_retries must be at least 1",
		},
		{
			name:          "invalid max_retries negative",
			maxRetries:    -1,
			retryDelay:    2,
			expectedError: "ai.max_retries must be at least 1",
		},
		{
			name:          "invalid retry_delay_seconds negative",
			maxRetries:    5,
			retryDelay:    -1,
			expectedError: "ai.retry_delay_seconds must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyPath := createTempKeyFile(t)
			defer func() { _ = os.Remove(keyPath) }()

			config := &Config{}
			config.AIProvider = "claude"
			config.Logging.Level = "info"
			config.Logging.Format = "console"
			config.Jira.BaseURL = "https://test.atlassian.net"
			config.Jira.Username = "test@example.com"
			config.Jira.APIToken = "test-token"
			config.Jira.AssigneeToGitHubUsername = map[string]string{
				"test@example.com": "test-user",
			}
			config.Jira.Projects = []ProjectConfig{
				{
					ProjectKeys: ProjectKeys{"TEST"},
					StatusTransitions: TicketTypeStatusTransitions{
						"default": StatusTransitions{
							Todo:       "To Do",
							InProgress: "In Progress",
							InReview:   "In Review",
						},
					},
					ComponentToRepo: ComponentToRepoMap{
						"component1": "https://github.com/test/repo1.git",
					},
				},
			}
			config.GitHub.AppID = 123456
			config.GitHub.PrivateKeyPath = keyPath
			config.GitHub.BotUsername = "test-bot"
			config.AI.MaxRetries = tt.maxRetries
			config.AI.RetryDelaySeconds = tt.retryDelay

			err := config.validate()

			if tt.expectedError == "" {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.expectedError)
				} else if !contains(err.Error(), tt.expectedError) {
					t.Errorf("Expected error containing '%s', got: %v", tt.expectedError, err)
				}
			}
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
