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

// buildExecCommand returns the command to pass to container Exec.
// The command runs the AI CLI, captures its exit code, writes
// session-output.json, and exits with the CLI's exit code.
func buildExecCommand(params scriptParams) []string {
	var cmd string

	switch params.Provider {
	case "claude":
		cmd = buildClaudeCommand(params.AllowedTools, params.Model)
	case "gemini":
		cmd = buildGeminiCommand(params.Model)
	default:
		// Generic fallback: assume the provider CLI accepts -p
		cmd = fmt.Sprintf("%s -p %q", params.Provider, taskPrompt)
	}

	script := fmt.Sprintf(`%s \
    2>&1 | tee /workspace/.ai-bot/session.log
AI_EXIT=${PIPESTATUS[0]}

# Write session metadata (always, even on failure).
cat > /workspace/%s <<'ENDJSON'
{"exit_code": EXITCODE_PLACEHOLDER}
ENDJSON
sed -i "s/EXITCODE_PLACEHOLDER/${AI_EXIT}/" /workspace/%s

exit ${AI_EXIT}
`, cmd, sessionOutputPath, sessionOutputPath)

	return []string{"bash", "-c", script}
}

const taskPrompt = "Read /workspace/.ai-bot/task.md and complete the task described there."

func buildClaudeCommand(allowedTools, model string) string {
	var parts []string
	parts = append(parts, "claude", "--dangerously-skip-permissions")

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
	parts = append(parts, "gemini", "-y")

	if model != "" {
		parts = append(parts, "--model", fmt.Sprintf("%q", model))
	}

	parts = append(parts, "-p", fmt.Sprintf("%q", taskPrompt))
	return strings.Join(parts, " ")
}
