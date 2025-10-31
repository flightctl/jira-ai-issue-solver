package services_test

import (
	"os"
	"testing"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
)

func TestGenerateCode(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-repo")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	t.Run("Successful code generation", func(t *testing.T) {
		mockClaude := &mocks.MockClaudeService{
			GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
				return &models.ClaudeResponse{
					Type:    "assistant",
					IsError: false,
					Message: &models.ClaudeMessage{
						Content: []models.ClaudeContent{{Type: "text", Text: "Generated code here"}},
						Usage:   models.ClaudeUsage{InputTokens: 100, OutputTokens: 200, ServiceTier: "claude-3-opus-20240229"},
					},
				}, nil
			},
		}
		var ai services.AIService = mockClaude
		result, err := ai.GenerateCode("Test prompt", tempDir)
		if err != nil {
			t.Fatalf("GenerateCode returned an error: %v", err)
		}
		response, ok := result.(*models.ClaudeResponse)
		if !ok {
			t.Fatalf("Expected *models.ClaudeResponse, got %T", result)
		}
		if response.Type != "assistant" {
			t.Errorf("Expected type assistant, got %s", response.Type)
		}
		if response.IsError {
			t.Errorf("Expected IsError false, got true")
		}
		if response.Message == nil || len(response.Message.Content) == 0 {
			t.Errorf("Expected message with content, but got nil or empty content")
		} else {
			if response.Message.Content[0].Text != "Generated code here" {
				t.Errorf("Expected content 'Generated code here', got '%s'", response.Message.Content[0].Text)
			}
			if response.Message.Usage.InputTokens != 100 {
				t.Errorf("Expected InputTokens 100, got %d", response.Message.Usage.InputTokens)
			}
			if response.Message.Usage.OutputTokens != 200 {
				t.Errorf("Expected OutputTokens 200, got %d", response.Message.Usage.OutputTokens)
			}
		}
	})

	t.Run("Error response from Claude", func(t *testing.T) {
		mockClaude := &mocks.MockClaudeService{
			GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
				return &models.ClaudeResponse{
					Type:    "assistant",
					IsError: true,
					Result:  "Error: something went wrong",
				}, nil
			},
		}
		var ai services.AIService = mockClaude
		result, err := ai.GenerateCode("Test prompt", tempDir)
		if err != nil {
			t.Fatalf("GenerateCode returned an error: %v", err)
		}
		response, ok := result.(*models.ClaudeResponse)
		if !ok {
			t.Fatalf("Expected *models.ClaudeResponse, got %T", result)
		}
		if !response.IsError {
			t.Errorf("Expected IsError true, got false")
		}
		if response.Result != "Error: something went wrong" {
			t.Errorf("Expected error message, got '%s'", response.Result)
		}
	})
}
