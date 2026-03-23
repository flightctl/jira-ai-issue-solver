package services

import "testing"

func TestIsExcludedPath(t *testing.T) {
	excludes := mergeExcludes([]string{".artifacts/"})

	tests := []struct {
		path string
		want bool
	}{
		// Built-in .ai-bot exclusion
		{".ai-bot", true},
		{".ai-bot/", true},
		{".ai-bot/task.md", true},
		{".ai-bot/session-output.json", true},
		{".ai-bot/run.sh", true},
		{".ai-bot/nested/file.txt", true},

		// Import-declared .artifacts exclusion
		{".artifacts", true},
		{".artifacts/", true},
		{".artifacts/bugfix/diagnosis.md", true},

		// Not excluded
		{"src/main.go", false},
		{"README.md", false},
		{".github/workflows/ci.yaml", false},
		{"ai-bot/file.txt", false},    // missing leading dot
		{".ai-bots/file.txt", false},  // different directory name
		{".ai-bot-extra/file", false}, // different directory name
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExcludedPath(tt.path, excludes)
			if got != tt.want {
				t.Errorf("isExcludedPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsExcludedPath_NoImportExcludes(t *testing.T) {
	excludes := mergeExcludes(nil)

	// .ai-bot is always excluded (builtin)
	if !isExcludedPath(".ai-bot/task.md", excludes) {
		t.Error("expected .ai-bot to be excluded even with no import excludes")
	}

	// .artifacts is NOT excluded when not declared by imports
	if isExcludedPath(".artifacts/diagnosis.md", excludes) {
		t.Error("expected .artifacts to NOT be excluded when not in import excludes")
	}
}

func TestMergeExcludes_NormalizesTrailingSlash(t *testing.T) {
	excludes := mergeExcludes([]string{".artifacts", "output/"})

	// Both should work regardless of trailing slash in input
	if !isExcludedPath(".artifacts/file.md", excludes) {
		t.Error("expected .artifacts/file.md to be excluded")
	}
	if !isExcludedPath("output/results.json", excludes) {
		t.Error("expected output/results.json to be excluded")
	}
}
