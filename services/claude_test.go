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
		expectedResult := "Generated code here"
		mockClaude := &mocks.MockClaudeService{
			GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
				return &models.ClaudeResponse{
					Type:    "assistant",
					IsError: false,
					Result:  expectedResult,
					Message: &models.ClaudeMessage{
						Content: []models.ClaudeContent{{Type: "text", Text: expectedResult}},
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
		// The mock's GenerateCode method returns the Result field as a string
		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("Expected string, got %T", result)
		}
		if resultStr != expectedResult {
			t.Errorf("Expected '%s', got '%s'", expectedResult, resultStr)
		}
	})

	t.Run("Error response from Claude", func(t *testing.T) {
		expectedResult := "Error: something went wrong"
		mockClaude := &mocks.MockClaudeService{
			GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
				return &models.ClaudeResponse{
					Type:    "assistant",
					IsError: true,
					Result:  expectedResult,
				}, nil
			},
		}
		var ai services.AIService = mockClaude
		result, err := ai.GenerateCode("Test prompt", tempDir)
		if err != nil {
			t.Fatalf("GenerateCode returned an error: %v", err)
		}
		// The mock's GenerateCode method returns the Result field as a string
		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("Expected string, got %T", result)
		}
		if resultStr != expectedResult {
			t.Errorf("Expected '%s', got '%s'", expectedResult, resultStr)
		}
	})

	t.Run("Mock extracts Result field correctly", func(t *testing.T) {
		expectedResult := "Test output"
		mockClaude := &mocks.MockClaudeService{
			GenerateCodeFunc: func(prompt string, repoDir string) (*models.ClaudeResponse, error) {
				return &models.ClaudeResponse{
					Type:    "completion",
					IsError: false,
					Result:  expectedResult,
					// Other fields that should be ignored by the mock
					SessionID:    "test-session",
					NumTurns:     5,
					TotalCostUsd: 0.01,
				}, nil
			},
		}
		var ai services.AIService = mockClaude
		result, err := ai.GenerateCode("prompt", tempDir)
		if err != nil {
			t.Fatalf("GenerateCode returned an error: %v", err)
		}

		// Should return the Result field as a string, not the full ClaudeResponse
		resultStr, ok := result.(string)
		if !ok {
			t.Fatalf("Expected string (extracted Result field), got %T", result)
		}
		if resultStr != expectedResult {
			t.Errorf("Expected '%s', got '%s'", expectedResult, resultStr)
		}
	})
}
