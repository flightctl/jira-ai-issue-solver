package services

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

//go:embed templates/claude_prompt.tmpl
var claudePromptTemplate string

//go:embed templates/claude_pr_feedback_prompt.tmpl
var claudePRFeedbackPromptTemplate string

var (
	claudePromptTmpl           *template.Template
	claudePRFeedbackPromptTmpl *template.Template
)

func init() {
	var err error

	claudePromptTmpl, err = template.New("claude_prompt").Parse(claudePromptTemplate)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse Claude prompt template: %v", err))
	}

	claudePRFeedbackPromptTmpl, err = template.New("claude_pr_feedback_prompt").Parse(claudePRFeedbackPromptTemplate)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse Claude PR feedback prompt template: %v", err))
	}
}

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
	resp, err := s.GenerateCodeClaude(prompt, repoDir)
	if err != nil {
		return nil, err
	}
	// Return the Result string for compatibility with consumers that expect string output
	// (e.g., PR review processor that parses comment responses)
	return resp.Result, nil
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

	// Add Anthropic API key if configured (for headless/container environments)
	if s.config.Claude.APIKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("ANTHROPIC_API_KEY=%s", s.config.Claude.APIKey))
		s.logger.Debug("Added ANTHROPIC_API_KEY to environment")
	}

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
			if localErr := json.Unmarshal([]byte(line), &response); localErr != nil {
				s.logger.Error("Failed to parse JSON line", zap.String("line", line), zap.Error(localErr))
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

		if localErr := scanner.Err(); localErr != nil {
			errorChan <- fmt.Errorf("error reading stream-json output: %w", localErr)
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
type claudePromptData struct {
	Ticket      *models.JiraTicketResponse
	Comments    []models.JiraComment
	HasComments bool
}

func PreparePrompt(ticket *models.JiraTicketResponse) (string, error) {
	data := claudePromptData{
		Ticket:      ticket,
		Comments:    ticket.Fields.Comment.Comments,
		HasComments: len(ticket.Fields.Comment.Comments) > 0,
	}

	var buf bytes.Buffer
	if err := claudePromptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute Claude prompt template: %w", err)
	}

	return buf.String(), nil
}

type claudePRFeedbackPromptData struct {
	PR     *models.GitHubPullRequest
	Review *models.GitHubReview
	Diff   string
}

// PreparePromptForPRFeedback prepares a prompt for Claude CLI based on PR feedback
func PreparePromptForPRFeedback(pr *models.GitHubPullRequest, review *models.GitHubReview, repoDir string) (string, error) {
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

	data := claudePRFeedbackPromptData{
		PR:     pr,
		Review: review,
		Diff:   stdout.String(),
	}

	var buf bytes.Buffer
	if err := claudePRFeedbackPromptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute PR feedback prompt template: %w", err)
	}

	return buf.String(), nil
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
