package executor

import (
	"strings"
	"testing"
)

func TestBuildExecCommand_Claude_Default(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "claude"})

	if len(cmd) != 3 {
		t.Fatalf("expected 3 elements [bash -c script], got %d", len(cmd))
	}
	if cmd[0] != "bash" || cmd[1] != "-c" {
		t.Errorf("expected [bash -c ...], got %v", cmd[:2])
	}

	script := cmd[2]
	if !strings.Contains(script, "claude") {
		t.Error("script should contain claude command")
	}
	if !strings.Contains(script, "--dangerously-skip-permissions") {
		t.Error("script should contain --dangerously-skip-permissions")
	}
	if !strings.Contains(script, taskPrompt) {
		t.Error("script should contain task prompt")
	}
	if !strings.Contains(script, "PIPESTATUS[0]") {
		t.Error("script should capture exit code via PIPESTATUS")
	}
	if !strings.Contains(script, sessionOutputPath) {
		t.Error("script should write session output file")
	}
}

func TestBuildExecCommand_Claude_WithAllowedTools(t *testing.T) {
	cmd := buildExecCommand(scriptParams{
		Provider:     "claude",
		AllowedTools: "Bash Edit Read Write",
	})

	script := cmd[2]
	if !strings.Contains(script, "--allowedTools") {
		t.Error("script should contain --allowedTools flag")
	}
	if !strings.Contains(script, "Bash Edit Read Write") {
		t.Error("script should contain allowed tools list")
	}
}

func TestBuildExecCommand_Claude_NoAllowedTools(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "claude"})

	script := cmd[2]
	if strings.Contains(script, "--allowedTools") {
		t.Error("script should not contain --allowedTools when empty")
	}
}

func TestBuildExecCommand_Gemini_Default(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "gemini"})

	script := cmd[2]
	if !strings.Contains(script, "gemini") {
		t.Error("script should contain gemini command")
	}
	if !strings.Contains(script, "-y") {
		t.Error("script should contain -y flag for non-interactive mode")
	}
	if !strings.Contains(script, taskPrompt) {
		t.Error("script should contain task prompt")
	}
	if strings.Contains(script, "--model") {
		t.Error("script should not contain --model when no model specified")
	}
}

func TestBuildExecCommand_Gemini_WithModel(t *testing.T) {
	cmd := buildExecCommand(scriptParams{
		Provider: "gemini",
		Model:    "gemini-2.5-pro",
	})

	script := cmd[2]
	if !strings.Contains(script, "--model") {
		t.Error("script should contain --model flag")
	}
	if !strings.Contains(script, "gemini-2.5-pro") {
		t.Error("script should contain model name")
	}
}

func TestBuildExecCommand_GenericProvider(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "custom-ai"})

	script := cmd[2]
	if !strings.Contains(script, "custom-ai") {
		t.Error("script should contain provider name as CLI command")
	}
	if !strings.Contains(script, taskPrompt) {
		t.Error("script should contain task prompt")
	}
}

func TestBuildExecCommand_SessionLogRedirect(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "claude"})

	script := cmd[2]
	if !strings.Contains(script, "tee /workspace/.ai-bot/session.log") {
		t.Error("script should tee output to session.log")
	}
}

func TestBuildExecCommand_ExitCodePreservation(t *testing.T) {
	cmd := buildExecCommand(scriptParams{Provider: "claude"})

	script := cmd[2]
	if !strings.Contains(script, "AI_EXIT=${PIPESTATUS[0]}") {
		t.Error("script should capture AI exit code from PIPESTATUS")
	}
	if !strings.Contains(script, "exit ${AI_EXIT}") {
		t.Error("script should exit with the AI's exit code")
	}
}

func TestBuildClaudeCommand(t *testing.T) {
	cmd := buildClaudeCommand("")
	if !strings.HasPrefix(cmd, "claude ") {
		t.Errorf("expected command to start with 'claude ', got %q", cmd)
	}
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("missing --dangerously-skip-permissions")
	}
	if !strings.Contains(cmd, "-p") {
		t.Error("missing -p flag")
	}
}

func TestBuildGeminiCommand(t *testing.T) {
	cmd := buildGeminiCommand("")
	if !strings.HasPrefix(cmd, "gemini ") {
		t.Errorf("expected command to start with 'gemini ', got %q", cmd)
	}
	if !strings.Contains(cmd, "-y") {
		t.Error("missing -y flag")
	}
	if !strings.Contains(cmd, "-p") {
		t.Error("missing -p flag")
	}
}

func TestBuildScriptParams(t *testing.T) {
	t.Run("nil repo config", func(t *testing.T) {
		params := buildScriptParams("claude", nil)
		if params.Provider != "claude" {
			t.Errorf("provider = %q, want %q", params.Provider, "claude")
		}
		if params.AllowedTools != "" {
			t.Errorf("allowed tools = %q, want empty", params.AllowedTools)
		}
		if params.Model != "" {
			t.Errorf("model = %q, want empty", params.Model)
		}
	})
}
