package models

import (
	"os"
	"testing"
)

func TestConfig_validateStatusTransitions(t *testing.T) {
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
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
					ProjectKeys: ProjectKeys{"PROJ1"},
					StatusTransitions: TicketTypeStatusTransitions{
						"Bug": StatusTransitions{
							Todo:       "To Do",
							InProgress: "In Progress",
							InReview:   "In Review",
						},
					},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: false,
		},
		{
			name: "empty todo status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
					ProjectKeys: ProjectKeys{"PROJ1"},
					StatusTransitions: TicketTypeStatusTransitions{
						"Bug": StatusTransitions{
							Todo:       "",
							InProgress: "In Progress",
							InReview:   "In Review",
						},
					},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: true,
		},
		{
			name: "empty in_progress status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
					ProjectKeys: ProjectKeys{"PROJ1"},
					StatusTransitions: TicketTypeStatusTransitions{
						"Bug": StatusTransitions{
							Todo:       "To Do",
							InProgress: "",
							InReview:   "In Review",
						},
					},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: true,
		},
		{
			name: "empty in_review status",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
					ProjectKeys: ProjectKeys{"PROJ1"},
					StatusTransitions: TicketTypeStatusTransitions{
						"Bug": StatusTransitions{
							Todo:       "To Do",
							InProgress: "In Progress",
							InReview:   "",
						},
					},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: true,
		},
		{
			name: "no ticket types configured",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:           "https://example.com",
					Username:          "testuser",
					APIToken:          "testtoken",
					ProjectKeys:       ProjectKeys{"PROJ1"},
					StatusTransitions: TicketTypeStatusTransitions{},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: true,
		},
		{
			name: "no project keys configured",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
					ProjectKeys: ProjectKeys{},
					StatusTransitions: TicketTypeStatusTransitions{
						"Bug": StatusTransitions{
							Todo:       "To Do",
							InProgress: "In Progress",
							InReview:   "In Review",
						},
					},
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
			},
			wantErr: true,
		},
		{
			name: "one ticket type valid, one invalid",
			config: Config{
				AIProvider: "claude",
				Jira: JiraConfig{
					BaseURL:     "https://example.com",
					Username:    "testuser",
					APIToken:    "testtoken",
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
				},
				ComponentToRepo: ComponentToRepoMap{"test": "https://github.com/test/repo.git"},
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
	// Create a temporary config file
	configContent := `
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  project_keys:
    - "PROJ1"
  status_transitions:
    Bug:
      todo: "To Do"
      in_progress: "In Progress"
      in_review: "In Review"
github:
  target_branch: "develop"
component_to_repo:
  test: https://github.com/test/repo.git
`
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

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
	bugTransitions := config.Jira.StatusTransitions.GetStatusTransitions("Bug")
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
	// Create a temporary config file without target_branch (should default to "main")
	configContent := `
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  project_keys:
    - "PROJ1"
  status_transitions:
    Bug:
      todo: "To Do"
      in_progress: "In Progress"
      in_review: "In Review"
component_to_repo:
  test: https://github.com/test/repo.git
`
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

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
	// Create a temporary config file with mixed case component names
	configContent := `
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  project_keys:
    - "PROJ1"
  status_transitions:
    Bug:
      todo: "To Do"
      in_progress: "In Progress"
      in_review: "In Review"
component_to_repo:
  FlightCtl: https://github.com/your-org/flightctl.git
  flightctl: https://github.com/your-org/flightctl-lowercase.git
  Backend: https://github.com/your-org/backend.git
  backend: https://github.com/your-org/backend-lowercase.git
`
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

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

	// Verify case sensitivity is preserved
	if config.ComponentToRepo["FlightCtl"] != "https://github.com/your-org/flightctl.git" {
		t.Errorf("Expected FlightCtl to map to flightctl.git, got '%s'", config.ComponentToRepo["FlightCtl"])
	}
	if config.ComponentToRepo["flightctl"] != "https://github.com/your-org/flightctl-lowercase.git" {
		t.Errorf("Expected flightctl to map to flightctl-lowercase.git, got '%s'", config.ComponentToRepo["flightctl"])
	}
	if config.ComponentToRepo["Backend"] != "https://github.com/your-org/backend.git" {
		t.Errorf("Expected Backend to map to backend.git, got '%s'", config.ComponentToRepo["Backend"])
	}
	if config.ComponentToRepo["backend"] != "https://github.com/your-org/backend-lowercase.git" {
		t.Errorf("Expected backend to map to backend-lowercase.git, got '%s'", config.ComponentToRepo["backend"])
	}

	// Verify that both keys exist and are different
	if config.ComponentToRepo["FlightCtl"] == config.ComponentToRepo["flightctl"] {
		t.Error("FlightCtl and flightctl should map to different repositories")
	}
	if config.ComponentToRepo["Backend"] == config.ComponentToRepo["backend"] {
		t.Error("Backend and backend should map to different repositories")
	}
}

func TestLoadConfig_WithTicketTypeSpecificStatusTransitions(t *testing.T) {
	// Create a temporary config file with ticket-type-specific status transitions
	configContent := `
logging:
  level: info
  format: console
ai_provider: "claude"
jira:
  base_url: "https://example.com"
  username: "testuser"
  api_token: "testtoken"
  project_keys:
    - "PROJ1"
  status_transitions:
    Bug:
      todo: "Open"
      in_progress: "In Progress"
      in_review: "Code Review"
    Story:
      todo: "Backlog"
      in_progress: "Development"
      in_review: "Testing"
github:
  target_branch: "develop"
component_to_repo:
  test: https://github.com/test/repo.git
`
	tmpfile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

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

	// Verify Bug-specific status transitions
	bugTransitions := config.Jira.StatusTransitions.GetStatusTransitions("Bug")
	if bugTransitions.Todo != "Open" {
		t.Errorf("Expected Bug todo status 'Open', got '%s'", bugTransitions.Todo)
	}
	if bugTransitions.InProgress != "In Progress" {
		t.Errorf("Expected Bug in_progress status 'In Progress', got '%s'", bugTransitions.InProgress)
	}
	if bugTransitions.InReview != "Code Review" {
		t.Errorf("Expected Bug in_review status 'Code Review', got '%s'", bugTransitions.InReview)
	}

	// Verify Story-specific status transitions
	storyTransitions := config.Jira.StatusTransitions.GetStatusTransitions("Story")
	if storyTransitions.Todo != "Backlog" {
		t.Errorf("Expected Story todo status 'Backlog', got '%s'", storyTransitions.Todo)
	}
	if storyTransitions.InProgress != "Development" {
		t.Errorf("Expected Story in_progress status 'Development', got '%s'", storyTransitions.InProgress)
	}
	if storyTransitions.InReview != "Testing" {
		t.Errorf("Expected Story in_review status 'Testing', got '%s'", storyTransitions.InReview)
	}

	// Verify that unknown ticket type returns empty transitions (no fallback)
	unknownTransitions := config.Jira.StatusTransitions.GetStatusTransitions("Unknown")
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
	// Create a temporary config file
	configContent := `
ai_provider: claude
ai:
  generate_documentation: false
claude:
  cli_path: claude
  timeout: 300
component_to_repo:
  "test-component": "https://github.com/test/repo"
jira:
  base_url: https://test.atlassian.net
  username: test-user
  api_token: test-token
  project_keys:
    - "PROJ1"
  status_transitions:
    Bug:
      todo: "To Do"
      in_progress: "In Progress"
      in_review: "In Review"
`

	tempFile, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write config content: %v", err)
	}
	tempFile.Close()

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
