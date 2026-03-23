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
	aiBotDir := filepath.Join(dir, filepath.Dir(taskfile.CommentResponsesPath))
	if err := os.MkdirAll(aiBotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, taskfile.CommentResponsesPath)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
