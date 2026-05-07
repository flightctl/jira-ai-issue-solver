package executor

import (
	"fmt"
	"strings"
)

// scriptParams controls the AI command construction.
type scriptParams struct {
	// Provider is the AI provider name ("claude" or "gemini").
	Provider string

	// AllowedTools is a Claude-specific space-separated list of
	// allowed tools (e.g., "Bash Edit Read Write"). Empty means
	// no restriction.
	AllowedTools string

	// Model is a provider-specific model override (e.g.,
	// "claude-sonnet-4-6", "gemini-2.5-pro"). Empty means
	// use the provider's default.
	Model string
}

// cliOutputPath is the path, relative to the workspace root, where the
// wrapper script saves the raw JSON output from the AI CLI. The Go code
// parses this to extract cost and token data — no jq required in the
// container.
const cliOutputPath = ".ai-bot/cli-output.json"

// buildExecCommand returns the command to pass to container Exec.
// The command runs the AI CLI with --output-format json, saves the
// raw JSON output for the Go code to parse, and exits with the CLI's
// exit code.
func buildExecCommand(params scriptParams) []string {
	var cmd string

	switch params.Provider {
	case "claude":
		cmd = buildClaudeCommand(params.AllowedTools, params.Model)
	case "gemini":
		cmd = buildGeminiCommand(params.Model)
	default:
		cmd = fmt.Sprintf("%s -p %q", params.Provider, taskPrompt)
	}

	script := fmt.Sprintf(`%s \
    > /workspace/%s \
    2> >(tee /workspace/.ai-bot/session.log >&2)
AI_EXIT=${PIPESTATUS[0]}

printf '{"exit_code": %%d}\n' "$AI_EXIT" > /workspace/%s

exit ${AI_EXIT}
`, cmd, cliOutputPath, sessionOutputPath)

	return []string{"bash", "-c", script}
}

const taskPrompt = "Read /workspace/.ai-bot/task.md and complete the task described there."

func buildClaudeCommand(allowedTools, model string) string {
	var parts []string
	parts = append(parts, "claude", "--dangerously-skip-permissions", "--output-format", "json", "--verbose")

	if model != "" {
		parts = append(parts, "--model", fmt.Sprintf("%q", model))
	}

	if allowedTools != "" {
		parts = append(parts, "--allowedTools", fmt.Sprintf("%q", allowedTools))
	}

	parts = append(parts, "-p", fmt.Sprintf("%q", taskPrompt))
	return strings.Join(parts, " ")
}

func buildGeminiCommand(model string) string {
	var parts []string
	parts = append(parts, "gemini", "-y", "--output-format", "json")

	if model != "" {
		parts = append(parts, "--model", fmt.Sprintf("%q", model))
	}

	parts = append(parts, "-p", fmt.Sprintf("%q", taskPrompt))
	return strings.Join(parts, " ")
}
