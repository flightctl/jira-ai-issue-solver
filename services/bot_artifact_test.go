package services

import "testing"

func TestIsBotArtifact(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".ai-bot", true},
		{".ai-bot/", true},
		{".ai-bot/task.md", true},
		{".ai-bot/session-output.json", true},
		{".ai-bot/run.sh", true},
		{".ai-bot/nested/file.txt", true},
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
			got := isBotArtifact(tt.path)
			if got != tt.want {
				t.Errorf("isBotArtifact(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
