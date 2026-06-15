package executor

import (
	"os"
	"path/filepath"
	"testing"

	"jira-ai-issue-solver/taskfile"
)

func TestReadCommentResponses_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	writeCommentResponsesFile(t, dir, `[
		{"comment_id": 123, "response": "Switched to Optional pattern."},
		{"comment_id": 456, "response": "Kept fallback path for compat."}
	]`)

	m := readCommentResponses(dir)

	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}
	if m[123] != "Switched to Optional pattern." {
		t.Errorf("response for 123 = %q", m[123])
	}
	if m[456] != "Kept fallback path for compat." {
		t.Errorf("response for 456 = %q", m[456])
	}
}

func TestReadCommentResponses_MissingFile(t *testing.T) {
	dir := t.TempDir()

	m := readCommentResponses(dir)

	if m != nil {
		t.Errorf("expected nil, got %v", m)
	}
}

func TestReadCommentResponses_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeCommentResponsesFile(t, dir, `not valid json`)

	m := readCommentResponses(dir)

	if m != nil {
		t.Errorf("expected nil for malformed JSON, got %v", m)
	}
}

func TestReadCommentResponses_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	writeCommentResponsesFile(t, dir, `[]`)

	m := readCommentResponses(dir)

	if m != nil {
		t.Errorf("expected nil for empty array, got %v", m)
	}
}

func TestReadCommentResponses_SkipsZeroIDAndEmptyResponse(t *testing.T) {
	dir := t.TempDir()
	writeCommentResponsesFile(t, dir, `[
		{"comment_id": 0, "response": "should be skipped"},
		{"comment_id": 123, "response": ""},
		{"comment_id": 456, "response": "valid response"}
	]`)

	m := readCommentResponses(dir)

	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if len(m) != 1 {
		t.Fatalf("len = %d, want 1 (only valid entry)", len(m))
	}
	if m[456] != "valid response" {
		t.Errorf("response for 456 = %q", m[456])
	}
}

func writeCommentResponsesFile(t *testing.T, dir, content string) {
	t.Helper()
	sessionDir := filepath.Join(dir, filepath.Dir(taskfile.CommentResponsesPath))
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, taskfile.CommentResponsesPath)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSessionFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	sessionDir := filepath.Join(dir, ".ai-session")
	if err := os.MkdirAll(sessionDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnrichFromCLIOutput_Claude(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "cli-output.json", `{
		"type": "result",
		"total_cost_usd": 0.36888,
		"is_error": false
	}`)

	var output SessionOutput
	enrichFromCLIOutput(&output, dir)

	if output.CostUSD != 0.36888 {
		t.Errorf("CostUSD = %v, want 0.36888", output.CostUSD)
	}
	if output.InputTokens != 0 {
		t.Error("InputTokens should be zero for Claude output")
	}
}

func TestEnrichFromCLIOutput_Gemini(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "cli-output.json", `{
		"session_id": "abc",
		"stats": {
			"models": {
				"gemini-2.5-flash-lite": {
					"tokens": {
						"input": 825,
						"candidates": 52,
						"cached": 0
					}
				},
				"gemini-3-flash-preview": {
					"tokens": {
						"input": 25617,
						"candidates": 771,
						"cached": 16265
					}
				}
			}
		}
	}`)

	var output SessionOutput
	enrichFromCLIOutput(&output, dir)

	if output.CostUSD != 0 {
		t.Error("CostUSD should be zero for Gemini (computed later)")
	}
	wantInput := 825 + 25617
	if output.InputTokens != wantInput {
		t.Errorf("InputTokens = %d, want %d", output.InputTokens, wantInput)
	}
	wantOutput := 52 + 771
	if output.OutputTokens != wantOutput {
		t.Errorf("OutputTokens = %d, want %d", output.OutputTokens, wantOutput)
	}
	if output.CachedTokens != 16265 {
		t.Errorf("CachedTokens = %d, want 16265", output.CachedTokens)
	}
}

func TestEnrichFromCLIOutput_MissingFile(t *testing.T) {
	dir := t.TempDir()

	var output SessionOutput
	enrichFromCLIOutput(&output, dir)

	if output.CostUSD != 0 || output.InputTokens != 0 {
		t.Error("should leave output unchanged when file is missing")
	}
}

func TestEnrichFromCLIOutput_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "cli-output.json", "not json at all")

	var output SessionOutput
	enrichFromCLIOutput(&output, dir)

	if output.CostUSD != 0 || output.InputTokens != 0 {
		t.Error("should leave output unchanged on invalid JSON")
	}
}

func TestReadSessionOutput_WithCLIOutput(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "session-output.json", `{"exit_code": 0}`)
	writeSessionFile(t, dir, "cli-output.json", `{"total_cost_usd": 1.23}`)

	output := readSessionOutput(dir)

	if output.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", output.ExitCode)
	}
	if output.CostUSD != 1.23 {
		t.Errorf("CostUSD = %v, want 1.23", output.CostUSD)
	}
}
