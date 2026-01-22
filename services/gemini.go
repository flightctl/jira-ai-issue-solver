package services

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

//go:embed templates/gemini_prompt.tmpl
var geminiPromptTemplate string

//go:embed templates/gemini_pr_feedback_prompt.tmpl
var geminiPRFeedbackPromptTemplate string

var (
	geminiPromptTmpl           *template.Template
	geminiPRFeedbackPromptTmpl *template.Template
)

func init() {
	var err error

	geminiPromptTmpl, err = template.New("gemini_prompt").Parse(geminiPromptTemplate)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse Gemini prompt template: %v", err))
	}

	geminiPRFeedbackPromptTmpl, err = template.New("gemini_pr_feedback_prompt").Parse(geminiPRFeedbackPromptTemplate)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse Gemini PR feedback prompt template: %v", err))
	}
}

// GeminiService interface for code generation using Gemini CLI
type GeminiService interface {
	AIService
	// GenerateCodeGemini generates code using Gemini CLI and returns GeminiResponse
	GenerateCodeGemini(prompt string, repoDir string) (*models.GeminiResponse, error)
}

// GeminiServiceImpl implements the GeminiService interface
type GeminiServiceImpl struct {
	config   *models.Config
	executor models.CommandExecutor
	logger   *zap.Logger
}

// NewGeminiService creates a new GeminiService
func NewGeminiService(config *models.Config, logger *zap.Logger, executor ...models.CommandExecutor) GeminiService {
	commandExecutor := exec.Command
	if len(executor) > 0 {
		commandExecutor = executor[0]
	}
	return &GeminiServiceImpl{
		config:   config,
		executor: commandExecutor,
		logger:   logger,
	}
}

// GenerateCode implements the AIService interface
func (s *GeminiServiceImpl) GenerateCode(prompt string, repoDir string) (interface{}, error) {
	resp, err := s.GenerateCodeGemini(prompt, repoDir)
	if err != nil {
		return nil, err
	}
	// Return the Result string for compatibility with consumers that expect string output
	// (e.g., PR review processor that parses comment responses)
	return resp.Result, nil
}

// GenerateDocumentation implements the AIService interface
func (s *GeminiServiceImpl) GenerateDocumentation(repoDir string) error {
	// Check if GEMINI.md already exists
	geminiPath := filepath.Join(repoDir, "GEMINI.md")
	if _, err := os.Stat(geminiPath); err == nil {
		s.logger.Info("GEMINI.md already exists, skipping generation", zap.String("repo_dir", repoDir))
		return nil
	}

	s.logger.Info("GEMINI.md not found, generating documentation", zap.String("repo_dir", repoDir))

	// Create prompt for generating GEMINI.md
	prompt := `Create a comprehensive GEMINI.md file in the root of the project that serves as an index and guide to all markdown documentation in this repository.

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
# GEMINI.md

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
IMPORTANT: Verify that you actually created and wrote GEMINI.md at the root of the project!`

	// Generate the documentation using Gemini
	response, err := s.GenerateCodeGemini(prompt, repoDir)
	if err != nil {
		return fmt.Errorf("failed to generate GEMINI.md: %w", err)
	}

	// Debug: Print the response to see what we got
	s.logger.Debug("Gemini documentation generation response",
		zap.String("response_type", response.Type),
		zap.Bool("is_error", response.IsError),
		zap.String("result", response.Result))

	if response.Message != nil {
		s.logger.Debug("Message content", zap.String("content", response.Message.Content))
	}

	// Extract content from the response and write to file
	var content string
	if response != nil && response.Message != nil {
		content = response.Message.Content
	} else if response != nil && response.Result != "" {
		content = response.Result
	} else {
		content = "# GEMINI.md\n\nThis file was generated by the Gemini AI service.\n"
	}

	s.logger.Debug("Generated GEMINI.md content", zap.String("content", content))

	// Instead of writing to the file, just ensure GEMINI.md exists (create if missing, but don't write content)
	// Check if GEMINI.md exists, but do not create it.
	if _, err := os.Stat(geminiPath); os.IsNotExist(err) {
		return fmt.Errorf("GEMINI.md does not exist at path: %s", geminiPath)
	} else if err != nil {
		return fmt.Errorf("failed to check GEMINI.md: %w", err)
	}

	s.logger.Info("Successfully generated GEMINI.md", zap.String("repo_dir", repoDir))
	return nil
}

// ensureGeminiSettings creates .gemini/settings.json in the repository if it doesn't exist
// This configures gemini-cli to allow shell command execution
func (s *GeminiServiceImpl) ensureGeminiSettings(repoDir string) error {
	geminiDir := filepath.Join(repoDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0750); err != nil {
		return fmt.Errorf("failed to create .gemini directory: %w", err)
	}

	settingsPath := filepath.Join(geminiDir, "settings.json")
	settingsContent := `{
  "tools": {
    "allowed": ["run_shell_command"]
  }
}
`
	if err := os.WriteFile(settingsPath, []byte(settingsContent), 0600); err != nil {
		return fmt.Errorf("failed to write .gemini/settings.json: %w", err)
	}
	s.logger.Debug("Created .gemini/settings.json", zap.String("path", settingsPath))
	return nil
}

// addToGitExclude adds an entry to .git/info/exclude (local ignore, never committed)
func (s *GeminiServiceImpl) addToGitExclude(repoDir, entry string) error {
	// Use .git/info/exclude instead of .gitignore so we don't modify tracked files
	gitDir := filepath.Join(repoDir, ".git")

	// Check if this is a git repository
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Not a git repo, skip silently (happens in tests)
		s.logger.Debug("Skipping git exclude, not a git repository", zap.String("dir", repoDir))
		return nil
	}

	// Ensure .git/info directory exists
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0750); err != nil {
		return fmt.Errorf("failed to create .git/info directory: %w", err)
	}

	excludePath := filepath.Join(infoDir, "exclude")

	// Check if exclude file exists and if it already contains the entry
	// #nosec G304 - excludePath is constructed from validated repoDir and hardcoded git paths, not user input
	existingContent, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read .git/info/exclude: %w", err)
	}

	// Only append if the entry doesn't already exist
	if !strings.Contains(string(existingContent), entry) {
		// #nosec G302 G304 - excludePath is validated (constructed from repoDir + hardcoded paths), 0600 is appropriate for git files
		f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open .git/info/exclude: %w", err)
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				s.logger.Warn("Failed to close .git/info/exclude", zap.Error(closeErr))
			}
		}()

		// Ensure entry ends with newline
		if !strings.HasSuffix(entry, "\n") {
			entry += "\n"
		}

		if _, err := f.WriteString(entry); err != nil {
			return fmt.Errorf("failed to write to .git/info/exclude: %w", err)
		}
		s.logger.Debug("Added entry to .git/info/exclude", zap.String("entry", strings.TrimSpace(entry)))
	}
	return nil
}

// GenerateCodeGemini generates code using Gemini CLI
func (s *GeminiServiceImpl) GenerateCodeGemini(prompt string, repoDir string) (*models.GeminiResponse, error) {
	s.logger.Info("Generating code with Gemini", zap.String("repo_dir", repoDir), zap.String("prompt", prompt))

	// Set up Gemini configuration files
	if err := s.ensureGeminiSettings(repoDir); err != nil {
		return nil, err
	}
	// Add .gemini/ to local git exclude (doesn't modify tracked files)
	if err := s.addToGitExclude(repoDir, ".gemini/"); err != nil {
		return nil, err
	}

	args := []string{"--debug", "--y"}
	// Add model if configured
	if s.config.Gemini.Model != "" {
		args = append(args, "-m", s.config.Gemini.Model)
	}
	// Add all files flag if configured
	if s.config.Gemini.AllFiles {
		args = append(args, "-a")
	}
	// Add sandbox flag if configured
	if s.config.Gemini.Sandbox {
		args = append(args, "-s")
	}
	// Note: --allowed-tools flag is not available in published npm versions of gemini-cli
	// YOLO mode (--y) automatically approves all tools
	// Add prompt
	args = append(args, "-p", prompt)

	// Set up a context with timeout
	timeout := time.Duration(s.config.Gemini.Timeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create the command using the executor (allows for testing)
	cmd := s.executor(s.config.Gemini.CLIPath, args...)
	cmd.Dir = repoDir

	// Print the actual command being executed
	s.logger.Debug("Executing Gemini CLI",
		zap.String("command", s.config.Gemini.CLIPath),
		zap.Strings("args", args),
		zap.String("directory", repoDir))

	// Set environment variables
	cmd.Env = os.Environ()

	// Set Gemini API key if configured
	if s.config.Gemini.APIKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("GEMINI_API_KEY=%s", s.config.Gemini.APIKey))
		s.logger.Debug("Gemini API key configured")
	} else {
		s.logger.Debug("Gemini API key not set")
	}

	// Capture stdout and stderr using buffers to avoid pipe race conditions
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Gemini CLI: %w", err)
	}

	// Wait for the command to finish or for the timeout to be reached
	// Use a channel to make Wait() cancellable
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
		// Command completed normally (or with error)
		s.logger.Debug("Gemini CLI finished")
	case <-ctx.Done():
		// Timeout reached - kill the process
		s.logger.Warn("Gemini CLI timeout reached, killing process",
			zap.Int("timeout_seconds", s.config.Gemini.Timeout))
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Wait a bit for kill to complete, then return timeout error
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
			s.logger.Error("Gemini process did not exit after kill signal")
		}
		return nil, fmt.Errorf("gemini CLI timed out after %d seconds", s.config.Gemini.Timeout)
	}

	// Get the captured output
	stdoutData := stdoutBuf.Bytes()
	stderrData := stderrBuf.Bytes()

	// Log stdout for debugging
	if len(stdoutData) > 0 {
		lines := strings.Split(string(stdoutData), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				cleaned := strings.ReplaceAll(trimmed, "Flushing log events to Clearcut.", "")
				cleaned = strings.TrimSpace(cleaned)
				if cleaned != "" {
					s.logger.Debug(cleaned)
				}
			}
		}
	}
	s.logger.Debug("Gemini stdout captured", zap.Int("bytes_read", len(stdoutData)))

	// Log stderr for debugging
	if len(stderrData) > 0 {
		s.logger.Debug("=== Gemini stderr ===\n" + string(stderrData) + "\n===================")
	}
	s.logger.Debug("Gemini stderr captured", zap.Int("bytes_read", len(stderrData)))

	// Print the command exit code if possible
	exitCode := -1
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(interface{ ExitStatus() int }); ok {
			exitCode = status.ExitStatus()
		}
		s.logger.Debug("Gemini CLI exited with code", zap.Int("exit_code", exitCode))
	} else if waitErr == nil {
		// If no error, exit code is 0
		exitCode = 0
		s.logger.Debug("Gemini CLI exited with code", zap.Int("exit_code", exitCode))
	}

	if waitErr != nil {
		// The context being canceled will result in an error
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gemini CLI timed out after %d seconds", s.config.Gemini.Timeout)
		}
		return nil, fmt.Errorf("gemini CLI failed: %w", waitErr)
	}

	// Create response with the actual accumulated output
	finalOutput := string(stdoutData)
	response := &models.GeminiResponse{
		Type:    "assistant",
		IsError: false,
		Result:  finalOutput,
		Message: &models.GeminiMessage{
			Type:    "message",
			Role:    "assistant",
			Model:   s.config.Gemini.Model,
			Content: finalOutput,
		},
	}

	s.logger.Debug("Capturing final Gemini response...",
		zap.Int("output_length", len(finalOutput)))
	s.logger.Debug("Output processing complete. Final response captured.")
	return response, nil
}

// geminiToolUsageInstructions returns critical instructions for Gemini CLI tool usage
// This is added at the start of every prompt to ensure Gemini uses tools correctly
func geminiToolUsageInstructions() string {
	return `CRITICAL INSTRUCTIONS - READ FIRST:

**EXECUTION REQUIREMENTS:**
1. You MUST actually execute commands using the run_shell_command tool - DO NOT just describe what you would do
2. DO NOT create any files named "response.txt" or similar - your responses should be text output, not files
3. DO NOT write responses to files - provide them as text in your output
4. ACTUALLY run the commands - this is not a simulation or planning exercise

**FORBIDDEN GIT OPERATIONS - DO NOT EXECUTE:**
- DO NOT run: git push
- DO NOT run: git pull
- DO NOT run: git fetch
These operations are handled by the system. You may use OTHER git commands (merge, add, commit, status, etc.) but NEVER push/pull/fetch.

**Tool Usage:**
When using run_shell_command:
- ONLY provide the 'command' argument
- Do NOT provide a 'description' argument
- Example: run_shell_command(command="git merge origin/main")

**Response Format:**
After executing the commands, provide your response as text output in the required format.
DO NOT create files with your responses.

---

`
}

// PreparePrompt prepares a prompt for Gemini CLI based on the Jira ticket
type geminiPromptData struct {
	Ticket                *models.JiraTicketResponse
	Comments              []models.JiraComment
	HasComments           bool
	ToolUsageInstructions string
}

func PreparePromptForGemini(ticket *models.JiraTicketResponse) string {
	data := geminiPromptData{
		Ticket:                ticket,
		Comments:              ticket.Fields.Comment.Comments,
		HasComments:           len(ticket.Fields.Comment.Comments) > 0,
		ToolUsageInstructions: geminiToolUsageInstructions(),
	}

	var buf bytes.Buffer
	if err := geminiPromptTmpl.Execute(&buf, data); err != nil {
		// Fallback to simple prompt on template error
		return fmt.Sprintf("# Task\n\n## %s\n\n%s\n\n# Instructions\n\nImplement the changes described above.",
			ticket.Fields.Summary, ticket.Fields.Description)
	}

	return buf.String()
}

type geminiPRFeedbackPromptData struct {
	PR                    *models.GitHubPullRequest
	Review                *models.GitHubReview
	Diff                  string
	ToolUsageInstructions string
}

// PreparePromptForPRFeedbackGemini prepares a prompt for Gemini CLI based on PR feedback
func PreparePromptForPRFeedbackGemini(pr *models.GitHubPullRequest, review *models.GitHubReview, repoDir string) (string, error) {
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

	data := geminiPRFeedbackPromptData{
		PR:                    pr,
		Review:                review,
		Diff:                  stdout.String(),
		ToolUsageInstructions: geminiToolUsageInstructions(),
	}

	var buf bytes.Buffer
	if err := geminiPRFeedbackPromptTmpl.Execute(&buf, data); err != nil {
		// Fallback to simple prompt on template error
		return fmt.Sprintf("# Pull Request Feedback\n\n## PR: %s\n\n%s\n\n## Review Feedback\n\n**%s**:\n%s\n\nPlease address the feedback.",
			pr.Title, pr.Body, review.User.Login, review.Body), nil
	}

	return buf.String(), nil
}
