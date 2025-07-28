package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jira-ai-issue-solver/models"

	"go.uber.org/zap"
)

// getContentAsString safely converts content to string, handling both string and array types
func getContentAsString(content interface{}) string {
	if content == nil {
		return ""
	}

	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		// Handle array of content objects
		var result strings.Builder
		for i, item := range v {
			if i > 0 {
				result.WriteString(", ")
			}
			if itemMap, ok := item.(map[string]interface{}); ok {
				if text, exists := itemMap["text"]; exists {
					if textStr, ok := text.(string); ok {
						result.WriteString(textStr)
					}
				}
			}
		}
		return result.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ClaudeService defines the interface for interacting with Claude CLI
type ClaudeService interface {
	AIService
	// GenerateCodeClaude generates code using Claude CLI and returns ClaudeResponse
	GenerateCodeClaude(prompt string, repoDir string) (*models.ClaudeResponse, error)
}

// ClaudeServiceImpl implements the ClaudeService interface
type ClaudeServiceImpl struct {
	config   *models.Config
	executor models.CommandExecutor
	logger   *zap.Logger
}

// NewClaudeService creates a new ClaudeService
func NewClaudeService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) ClaudeService {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}
	return &ClaudeServiceImpl{
		config:   config,
		executor: commandExecutor,
		logger:   logger,
	}
}

// GenerateCode implements the AIService interface
func (s *ClaudeServiceImpl) GenerateCode(prompt string, repoDir string) (interface{}, error) {
	return s.GenerateCodeClaude(prompt, repoDir)
}

// GenerateDocumentation implements the AIService interface
func (s *ClaudeServiceImpl) GenerateDocumentation(repoDir string) error {
	// Check if CLAUDE.md already exists
	claudePath := filepath.Join(repoDir, "CLAUDE.md")
	if _, err := os.Stat(claudePath); err == nil {
		s.logger.Info("CLAUDE.md already exists, skipping generation", zap.String("repo_dir", repoDir))
		return nil
	}

	s.logger.Info("CLAUDE.md not found, generating documentation", zap.String("repo_dir", repoDir))

	// Create prompt for generating CLAUDE.md
	prompt := `Create a comprehensive CLAUDE.md file in the root of the project that serves as an index and guide to all markdown documentation in this repository.

## Requirements:
1. **File Structure**: Create a well-organized document with clear sections and subsections
2. **File Index**: List all markdown files found in the repository (including nested folders) with:
   - Proper headlines for each file
   - Brief descriptions of what each file contains
   - Links to the actual files rather than copying their content
3. **Organization**: Group files logically (e.g., by directory, by purpose)
4. **Navigation**: Include a table of contents at the top
5. **Context**: Provide context about how the files relate to each other
6. **Keep it short and concise**: Keep the file short and concise, don't include any unnecessary details

## Format:
- Use clear, descriptive headlines for each file entry
- Include a brief description (1-2 sentences) explaining what each file covers
- Use relative links to the actual markdown files
- Organize files in a logical structure
- Make it easy for users to find relevant documentation

## Example structure:
# CLAUDE.md

## Table of Contents
- [Getting Started](#getting-started)
- [Documentation](#documentation)
- [Contributing](#contributing)

## Getting Started
- [README.md](./README.md) - Main project overview and setup instructions
- [INSTALL.md](./docs/INSTALL.md) - Detailed installation guide

## Documentation
- [API.md](./docs/API.md) - API reference and usage examples
- [ARCHITECTURE.md](./docs/ARCHITECTURE.md) - System architecture overview

## Contributing
- [CONTRIBUTING.md](./CONTRIBUTING.md) - Guidelines for contributors
- [STYLE.md](./docs/STYLE.md) - Code style and formatting guidelines

Search the entire repository for all .md files and create a comprehensive index following this structure.
IMPORTANT: Verify that you actually created and wrote CLAUDE.md at the root of the project!`

	// Generate the documentation using Claude
	response, err := s.GenerateCodeClaude(prompt, repoDir)
	if err != nil {
		return fmt.Errorf("failed to generate CLAUDE.md: %w", err)
	}

	// Log the documentation generation response
	if response != nil && response.Message != nil && len(response.Message.Content) > 0 {
		for _, contentItem := range response.Message.Content {
			if contentItem.Type == "text" {
				s.logger.Debug("Documentation response", zap.String("response", contentItem.Text))
				break
			}
		}
	}

	s.logger.Info("Generated CLAUDE.md content", zap.String("repo_dir", repoDir))

	// Instead of writing to the file, just ensure CLAUDE.md exists (create if missing, but don't write content)
	// Check if CLAUDE.md exists, but do not create it.
	if _, err := os.Stat(claudePath); os.IsNotExist(err) {
		return fmt.Errorf("CLAUDE.md does not exist at path: %s", claudePath)
	} else if err != nil {
		return fmt.Errorf("failed to check CLAUDE.md: %w", err)
	}

	s.logger.Info("Successfully generated CLAUDE.md", zap.String("repo_dir", repoDir))
	return nil
}

// GenerateCodeClaude generates code using Claude CLI
func (s *ClaudeServiceImpl) GenerateCodeClaude(prompt string, repoDir string) (*models.ClaudeResponse, error) {
	// Build command arguments based on configuration
	s.logger.Info("Generating code for repo", zap.String("repo_dir", repoDir))
	args := []string{"--output-format", "stream-json", "--verbose", "-p", prompt}

	// Add dangerous permissions flag if configured
	if s.config.Claude.DangerouslySkipPermissions {
		args = append([]string{"--dangerously-skip-permissions"}, args...)
	}

	// Add allowed tools if configured
	if s.config.Claude.AllowedTools != "" {
		args = append([]string{"--allowedTools", s.config.Claude.AllowedTools}, args...)
	}

	// Add disallowed tools if configured
	if s.config.Claude.DisallowedTools != "" {
		args = append([]string{"--disallowedTools", s.config.Claude.DisallowedTools}, args...)
	}

	// Set up a context with timeout
	timeout := time.Duration(s.config.Claude.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create the command with context
	cmd := exec.CommandContext(ctx, s.config.Claude.CLIPath, args...)
	cmd.Dir = repoDir

	// Print the actual command being executed
	s.logger.Debug("Executing Claude CLI",
		zap.String("command", s.config.Claude.CLIPath),
		zap.Strings("args", args),
		zap.String("directory", repoDir))

	// Set environment variables
	cmd.Env = os.Environ()

	// Create pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Claude CLI: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2) // We have two goroutines for logging (stdout and stderr)

	// Channel to collect the final response
	resultChan := make(chan *models.ClaudeResponse, 1)
	errorChan := make(chan error, 1)

	// Log stderr concurrently
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		// Increase buffer size to handle large output
		buf := make([]byte, 1024*1024) // 1MB buffer
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			s.logger.Error("stderr", zap.String("line", scanner.Text()))
		}
	}()

	// Log stdout and process stream-json concurrently
	go func() {
		defer wg.Done()
		s.logger.Info("Starting Claude stream processing...")
		var finalResponse *models.ClaudeResponse
		scanner := bufio.NewScanner(stdoutPipe)
		// Increase buffer size to handle large output from Claude CLI
		buf := make([]byte, 1024*1024) // 1MB buffer
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var response models.ClaudeResponse
			if err := json.Unmarshal([]byte(line), &response); err != nil {
				s.logger.Error("Failed to parse JSON line", zap.String("line", line), zap.Error(err))
				continue
			}

			// Log each message in a concise format
			var role string
			var contents []string

			if response.Message != nil {
				role = response.Message.Role
				if len(response.Message.Content) > 0 {
					for _, contentItem := range response.Message.Content {
						switch contentItem.Type {
						case "text":
							contents = append(contents, contentItem.Text)
						case "tool_use":
							contents = append(contents, fmt.Sprintf("tool_use: %s(%s)", contentItem.Name, contentItem.ID))
						case "tool_result":
							contents = append(contents, fmt.Sprintf("tool_result: %s", getContentAsString(contentItem.Content)))
						default:
							contents = append(contents, fmt.Sprintf("%s: %+v", contentItem.Type, contentItem))
						}
					}
				}
			} else {
				role = response.Type
				if response.Result != "" {
					contents = append(contents, response.Result)
				}
			}

			// Log in concise format: Role: content (one line per content item with tab prefix)
			if role != "" && len(contents) > 0 {
				for _, content := range contents {
					s.logger.Debug("Claude response", zap.String("role", role), zap.String("content", content))
				}
			} else if response.IsError {
				s.logger.Error("Claude error", zap.String("result", response.Result))
			}

			// Check if there was an error
			if response.IsError {
				errorChan <- fmt.Errorf("claude CLI returned an error: %s", response.Result)
				return
			}

			// For stream-json, we want to capture the final response
			// The final response typically has the complete result
			if response.Type == "assistant" && response.Message != nil {
				// Always update finalResponse to the latest assistant message
				finalResponse = &response
			}
		}

		if err := scanner.Err(); err != nil {
			errorChan <- fmt.Errorf("error reading stream-json output: %w", err)
			return
		}

		if finalResponse == nil {
			errorChan <- fmt.Errorf("no valid response found in stream-json output")
			return
		}

		s.logger.Info("Stream processing complete.")
		resultChan <- finalResponse
	}()

	// Wait for the command to finish or for the timeout to be reached
	err = cmd.Wait()
	s.logger.Info("Claude CLI finished")

	// Wait for the logging goroutines to finish
	// This ensures we capture all output before the function exits
	wg.Wait()

	if err != nil {
		// The context being canceled will result in an error
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude CLI timed out after %d seconds", s.config.Claude.Timeout)
		}
		return nil, fmt.Errorf("claude CLI failed: %w", err)
	}

	// Wait for the result or error from the streaming goroutine
	select {
	case result := <-resultChan:
		return result, nil
	case err := <-errorChan:
		return nil, err
	case <-time.After(5 * time.Second): // Additional timeout for result processing
		return nil, fmt.Errorf("timeout waiting for stream processing result")
	}
}

// PreparePrompt prepares a prompt for Claude CLI based on the Jira ticket
func PreparePrompt(ticket *models.JiraTicketResponse) string {
	var sb strings.Builder

	sb.WriteString("# Task\n\n")
	sb.WriteString(fmt.Sprintf("## %s\n\n", ticket.Fields.Summary))
	sb.WriteString(fmt.Sprintf("%s\n\n", ticket.Fields.Description))

	// Add comments if available
	if len(ticket.Fields.Comment.Comments) > 0 {
		sb.WriteString("## Comments\n\n")
		for _, comment := range ticket.Fields.Comment.Comments {
			sb.WriteString(fmt.Sprintf("**%s** (%s):\n%s\n\n",
				comment.Author.DisplayName,
				comment.Created.Format("2006-01-02 15:04:05"),
				comment.Body))
		}
	}

	sb.WriteString("# Instructions\n\n")
	sb.WriteString("1. First, examine any relevant *.md files (README.md, CONTRIBUTING.md, etc.) in the repository (these might be nested so search the entire repo!) to understand the project structure, testing conventions, and how to run tests.\n")
	sb.WriteString("2. Analyze the task description and comments.\n")
	sb.WriteString("3. Implement the necessary changes to fulfill the requirements.\n")
	sb.WriteString("4. Write tests for the implemented functionality if appropriate.\n")
	sb.WriteString("5. Update documentation if necessary.\n")
	sb.WriteString("6. Make sure the project builds successfully before running tests.\n")
	sb.WriteString("7. Review the markdown files (README.md, CONTRIBUTING.md, etc.) to understand how tests should be run for this project. These files might be nested inside directories, so search the entire repository structure.\n")
	sb.WriteString("8. Verify your changes by running the relevant tests to ensure they work correctly.\n")
	sb.WriteString("9. Provide a summary of the changes made.\n")
	sb.WriteString("10. IMPORTANT: Do NOT perform any git operations (commit, push, pull, etc.). Git handling is managed by the system.\n\n")

	sb.WriteString("# Output Format\n\n")
	sb.WriteString("Please provide your response in the following format:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Summary\n")
	sb.WriteString("<A brief summary of the changes made>\n\n")
	sb.WriteString("## Changes Made\n")
	sb.WriteString("<List of files modified and a description of the changes>\n\n")
	sb.WriteString("## Testing\n")
	sb.WriteString("<Description of how the changes were tested>\n")
	sb.WriteString("```\n")

	return sb.String()
}

// PreparePromptForPRFeedback prepares a prompt for Claude CLI based on PR feedback
func PreparePromptForPRFeedback(pr *models.GitHubPullRequest, review *models.GitHubReview, repoDir string) (string, error) {
	var sb strings.Builder

	sb.WriteString("# Pull Request Feedback\n\n")
	sb.WriteString(fmt.Sprintf("## PR: %s\n\n", pr.Title))
	sb.WriteString(fmt.Sprintf("%s\n\n", pr.Body))

	sb.WriteString("## Review Feedback\n\n")
	sb.WriteString(fmt.Sprintf("**%s**:\n%s\n\n", review.User.Login, review.Body))

	// Get the diff of the PR
	cmd := exec.Command("git", "diff", "origin/main...HEAD")
	cmd.Dir = repoDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get PR diff: %w, stderr: %s", err, stderr.String())
	}

	sb.WriteString("## Current Changes\n\n")
	sb.WriteString("```diff\n")
	sb.WriteString(stdout.String())
	sb.WriteString("\n```\n\n")

	sb.WriteString("# Instructions\n\n")
	sb.WriteString("1. Analyze the PR feedback and the current changes.\n")
	sb.WriteString("2. Implement the necessary changes to address the feedback.\n")
	sb.WriteString("3. Update tests if necessary.\n")
	sb.WriteString("4. Update documentation if necessary.\n")
	sb.WriteString("5. Make sure the project builds successfully before running tests.\n")
	sb.WriteString("6. Review the markdown files (README.md, CONTRIBUTING.md, etc.) to understand how tests should be run for this project. These files might be nested inside directories, so search the entire repository structure.\n")
	sb.WriteString("7. Verify your changes by running the relevant tests to ensure they work correctly.\n")
	sb.WriteString("8. Provide a summary of the changes made.\n")
	sb.WriteString("9. IMPORTANT: Do NOT perform any git operations (commit, push, pull, etc.). Git handling is managed by the system.\n\n")

	sb.WriteString("# Output Format\n\n")
	sb.WriteString("Please provide your response in the following format:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Summary\n")
	sb.WriteString("<A brief summary of the changes made>\n\n")
	sb.WriteString("## Changes Made\n")
	sb.WriteString("<List of files modified and a description of the changes>\n\n")
	sb.WriteString("## Feedback Addressed\n")
	sb.WriteString("<Description of how the feedback was addressed>\n")
	sb.WriteString("```\n")

	return sb.String(), nil
}

// GetChangedFiles gets a list of files changed in the current branch
func GetChangedFiles(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "origin/main...HEAD")
	cmd.Dir = repoDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to get changed files: %w, stderr: %s", err, stderr.String())
	}

	files := strings.Split(strings.TrimSpace(stdout.String()), "\n")

	// Filter out empty strings
	var result []string
	for _, file := range files {
		if file != "" {
			// Get the absolute path
			absPath := filepath.Join(repoDir, file)
			result = append(result, absPath)
		}
	}

	return result, nil
}
