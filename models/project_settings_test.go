package models

import "testing"

func TestProjectSettings_ForkOwner(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     string
	}{
		{name: "with username", username: "alice", want: "alice"},
		{name: "empty username", username: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{GitHubUsername: tt.username}
			if got := s.ForkOwner(); got != tt.want {
				t.Errorf("ForkOwner() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectSettings_CommitOwner(t *testing.T) {
	tests := []struct {
		name     string
		owner    string
		username string
		want     string
	}{
		{
			name:     "fork mode uses GitHub username",
			owner:    "upstream-org",
			username: "alice",
			want:     "alice",
		},
		{
			name:     "no fork uses upstream owner",
			owner:    "upstream-org",
			username: "",
			want:     "upstream-org",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{Owner: tt.owner, GitHubUsername: tt.username}
			if got := s.CommitOwner(); got != tt.want {
				t.Errorf("CommitOwner() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectSettings_PRHead(t *testing.T) {
	tests := []struct {
		name     string
		username string
		branch   string
		want     string
	}{
		{
			name:     "fork mode prefixes owner",
			username: "alice",
			branch:   "bot/TICKET-1",
			want:     "alice:bot/TICKET-1",
		},
		{
			name:     "no fork returns branch only",
			username: "",
			branch:   "bot/TICKET-1",
			want:     "bot/TICKET-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{GitHubUsername: tt.username}
			if got := s.PRHead(tt.branch); got != tt.want {
				t.Errorf("PRHead(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}
