package models

import "testing"

func TestProjectSettings_ForkOwner(t *testing.T) {
	tests := []struct {
		name     string
		forkMode bool
		username string
		want     string
	}{
		{name: "fork mode with username", forkMode: true, username: "alice", want: "alice"},
		{name: "fork mode without username", forkMode: true, username: "", want: ""},
		{name: "direct mode with username", forkMode: false, username: "alice", want: ""},
		{name: "direct mode without username", forkMode: false, username: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{ForkMode: tt.forkMode, GitHubUsername: tt.username}
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
		forkMode bool
		username string
		want     string
	}{
		{
			name:     "fork mode uses GitHub username",
			owner:    "upstream-org",
			forkMode: true,
			username: "alice",
			want:     "alice",
		},
		{
			name:     "fork mode without username uses upstream owner",
			owner:    "upstream-org",
			forkMode: true,
			username: "",
			want:     "upstream-org",
		},
		{
			name:     "direct mode uses upstream owner even with username",
			owner:    "upstream-org",
			forkMode: false,
			username: "alice",
			want:     "upstream-org",
		},
		{
			name:     "direct mode uses upstream owner",
			owner:    "upstream-org",
			forkMode: false,
			username: "",
			want:     "upstream-org",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{
				Repos:          []RepoSettings{{Owner: tt.owner}},
				ForkMode:       tt.forkMode,
				GitHubUsername: tt.username,
			}
			if got := s.CommitOwner(); got != tt.want {
				t.Errorf("CommitOwner() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectSettings_CommitOwnerFor(t *testing.T) {
	tests := []struct {
		name     string
		repoOwn  string
		forkMode bool
		username string
		want     string
	}{
		{
			name:     "fork mode uses GitHub username",
			repoOwn:  "upstream-org",
			forkMode: true,
			username: "alice",
			want:     "alice",
		},
		{
			name:     "fork mode without username uses repo owner",
			repoOwn:  "upstream-org",
			forkMode: true,
			username: "",
			want:     "upstream-org",
		},
		{
			name:     "direct mode uses repo owner even with username",
			repoOwn:  "upstream-org",
			forkMode: false,
			username: "alice",
			want:     "upstream-org",
		},
		{
			name:     "direct mode uses repo owner",
			repoOwn:  "upstream-org",
			forkMode: false,
			username: "",
			want:     "upstream-org",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{ForkMode: tt.forkMode, GitHubUsername: tt.username}
			repo := RepoSettings{Owner: tt.repoOwn}
			if got := s.CommitOwnerFor(repo); got != tt.want {
				t.Errorf("CommitOwnerFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectSettings_PRHead(t *testing.T) {
	tests := []struct {
		name     string
		forkMode bool
		username string
		branch   string
		want     string
	}{
		{
			name:     "fork mode prefixes owner",
			forkMode: true,
			username: "alice",
			branch:   "bot/TICKET-1",
			want:     "alice:bot/TICKET-1",
		},
		{
			name:     "fork mode without username returns branch only",
			forkMode: true,
			username: "",
			branch:   "bot/TICKET-1",
			want:     "bot/TICKET-1",
		},
		{
			name:     "direct mode returns branch only even with username",
			forkMode: false,
			username: "alice",
			branch:   "bot/TICKET-1",
			want:     "bot/TICKET-1",
		},
		{
			name:     "direct mode returns branch only",
			forkMode: false,
			username: "",
			branch:   "bot/TICKET-1",
			want:     "bot/TICKET-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{ForkMode: tt.forkMode, GitHubUsername: tt.username}
			if got := s.PRHead(tt.branch); got != tt.want {
				t.Errorf("PRHead(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestProjectSettings_PRHeads(t *testing.T) {
	tests := []struct {
		name     string
		forkMode bool
		username string
		branch   string
		want     []string
	}{
		{
			name:     "fork mode returns fork head then direct fallback",
			forkMode: true,
			username: "alice",
			branch:   "bot/TICKET-1",
			want:     []string{"alice:bot/TICKET-1", "bot/TICKET-1"},
		},
		{
			name:     "fork mode without username returns branch only",
			forkMode: true,
			username: "",
			branch:   "bot/TICKET-1",
			want:     []string{"bot/TICKET-1"},
		},
		{
			name:     "direct mode returns branch only even with username",
			forkMode: false,
			username: "alice",
			branch:   "bot/TICKET-1",
			want:     []string{"bot/TICKET-1"},
		},
		{
			name:     "direct mode returns branch only",
			forkMode: false,
			username: "",
			branch:   "bot/TICKET-1",
			want:     []string{"bot/TICKET-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{ForkMode: tt.forkMode, GitHubUsername: tt.username}
			got := s.PRHeads(tt.branch)
			if len(got) != len(tt.want) {
				t.Fatalf("PRHeads(%q) = %v, want %v", tt.branch, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("PRHeads(%q)[%d] = %q, want %q", tt.branch, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestProjectSettings_IsMultiRepo(t *testing.T) {
	tests := []struct {
		name  string
		repos []RepoSettings
		want  bool
	}{
		{name: "no repos", repos: nil, want: false},
		{name: "single repo", repos: []RepoSettings{{Name: "a"}}, want: false},
		{name: "multi repo", repos: []RepoSettings{{Name: "a"}, {Name: "b"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProjectSettings{Repos: tt.repos}
			if got := s.IsMultiRepo(); got != tt.want {
				t.Errorf("IsMultiRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectSettings_ResolvedContainer(t *testing.T) {
	t.Run("workspace container takes precedence", func(t *testing.T) {
		s := &ProjectSettings{
			Container: ContainerSettings{Image: "workspace-image:latest"},
			Repos: []RepoSettings{
				{Container: ContainerSettings{Image: "repo-image:latest"}},
			},
		}
		got := s.ResolvedContainer()
		if got.Image != "workspace-image:latest" {
			t.Errorf("ResolvedContainer().Image = %q, want %q", got.Image, "workspace-image:latest")
		}
	})

	t.Run("falls back to first repo container", func(t *testing.T) {
		s := &ProjectSettings{
			Repos: []RepoSettings{
				{Container: ContainerSettings{Image: "repo-image:latest"}},
			},
		}
		got := s.ResolvedContainer()
		if got.Image != "repo-image:latest" {
			t.Errorf("ResolvedContainer().Image = %q, want %q", got.Image, "repo-image:latest")
		}
	})

	t.Run("returns zero value when no container configured", func(t *testing.T) {
		s := &ProjectSettings{
			Repos: []RepoSettings{{}},
		}
		got := s.ResolvedContainer()
		if got.Image != "" {
			t.Errorf("ResolvedContainer().Image = %q, want empty", got.Image)
		}
	})
}
