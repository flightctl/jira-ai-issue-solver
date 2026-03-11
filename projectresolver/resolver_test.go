package projectresolver_test

import (
	"strings"
	"testing"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/projectresolver"
)

// --- NewConfigResolver ---

func TestNewConfigResolver_NilConfig(t *testing.T) {
	_, err := projectresolver.NewConfigResolver(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewConfigResolver_ValidConfig(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
}

// --- ResolveProject ---

func TestResolveProject_HappyPath(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-42",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Owner != "my-org" {
		t.Errorf("owner = %q, want %q", ps.Owner, "my-org")
	}
	if ps.Repo != "backend" {
		t.Errorf("repo = %q, want %q", ps.Repo, "backend")
	}
	if ps.CloneURL != "https://github.com/my-org/backend.git" {
		t.Errorf("clone URL = %q, want %q", ps.CloneURL, "https://github.com/my-org/backend.git")
	}
	if ps.BaseBranch != "main" {
		t.Errorf("base branch = %q, want %q", ps.BaseBranch, "main")
	}
	if ps.InProgressStatus != "In Progress" {
		t.Errorf("in-progress status = %q, want %q", ps.InProgressStatus, "In Progress")
	}
	if ps.InReviewStatus != "In Review" {
		t.Errorf("in-review status = %q, want %q", ps.InReviewStatus, "In Review")
	}
	if ps.TodoStatus != "To Do" {
		t.Errorf("todo status = %q, want %q", ps.TodoStatus, "To Do")
	}
	if ps.PRURLFieldName != "customfield_10100" {
		t.Errorf("PR field = %q, want %q", ps.PRURLFieldName, "customfield_10100")
	}
	if ps.DisableErrorComments {
		t.Error("disable error comments should be false")
	}
	if ps.AIProvider != "claude" {
		t.Errorf("AI provider = %q, want %q", ps.AIProvider, "claude")
	}
}

func TestResolveProject_MultipleComponents_FirstMatchUsed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].ComponentToRepo["frontend"] = "https://github.com/my-org/frontend.git"

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "backend" appears first and matches, so it should be used.
	wi := models.WorkItem{
		Key:        "PROJ-10",
		Type:       "Bug",
		Components: []string{"backend", "frontend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Owner != "my-org" || ps.Repo != "backend" {
		t.Errorf("expected my-org/backend, got %s/%s", ps.Owner, ps.Repo)
	}
}

func TestResolveProject_MultipleComponents_SecondMatches(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].ComponentToRepo["frontend"] = "https://github.com/my-org/frontend"

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "unknown" does not match, but "frontend" does.
	wi := models.WorkItem{
		Key:        "PROJ-10",
		Type:       "Bug",
		Components: []string{"unknown", "frontend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Owner != "my-org" || ps.Repo != "frontend" {
		t.Errorf("expected my-org/frontend, got %s/%s", ps.Owner, ps.Repo)
	}
}

func TestResolveProject_NoComponents(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-5",
		Type:       "Bug",
		Components: []string{},
	}

	_, err = r.ResolveProject(wi)
	if err == nil {
		t.Fatal("expected error for empty components")
	}
	assertContains(t, err.Error(), "no components")
}

func TestResolveProject_NoMatchingComponent(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-5",
		Type:       "Bug",
		Components: []string{"unknown-component"},
	}

	_, err = r.ResolveProject(wi)
	if err == nil {
		t.Fatal("expected error for non-matching component")
	}
	assertContains(t, err.Error(), "no component-to-repo mapping")
}

func TestResolveProject_NoProjectConfig(t *testing.T) {
	// Config with no projects at all. GetProjectConfigForTicket returns nil.
	cfg := &models.Config{}
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "NOPE-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	_, err = r.ResolveProject(wi)
	if err == nil {
		t.Fatal("expected error for missing project config")
	}
	assertContains(t, err.Error(), "no project configuration")
}

func TestResolveProject_FallbackProject(t *testing.T) {
	// GetProjectConfigForTicket falls back to the first project when
	// no project key matches. Verify the resolver handles this.
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "OTHER-99",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Falls back to the first (and only) project config.
	if ps.Owner != "my-org" || ps.Repo != "backend" {
		t.Errorf("expected my-org/backend from fallback, got %s/%s", ps.Owner, ps.Repo)
	}
}

func TestResolveProject_URLWithGitSuffix(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Repo != "backend" {
		t.Errorf("repo = %q, want %q (should strip .git)", ps.Repo, "backend")
	}
}

func TestResolveProject_URLWithoutGitSuffix(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].ComponentToRepo["backend"] = "https://github.com/my-org/backend"

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Owner != "my-org" || ps.Repo != "backend" {
		t.Errorf("expected my-org/backend, got %s/%s", ps.Owner, ps.Repo)
	}
}

func TestResolveProject_URLWithTrailingSlash(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].ComponentToRepo["backend"] = "https://github.com/my-org/backend/"

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Owner != "my-org" || ps.Repo != "backend" {
		t.Errorf("expected my-org/backend, got %s/%s", ps.Owner, ps.Repo)
	}
}

func TestResolveProject_StatusTransitions_DifferentTypes(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].StatusTransitions["Story"] = models.StatusTransitions{
		Todo:       "Backlog",
		InProgress: "Working",
		InReview:   "Reviewing",
	}

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		ticketType string
		wantTodo   string
		wantInProg string
		wantReview string
	}{
		{"Bug", "To Do", "In Progress", "In Review"},
		{"Story", "Backlog", "Working", "Reviewing"},
	}

	for _, tt := range tests {
		t.Run(tt.ticketType, func(t *testing.T) {
			wi := models.WorkItem{
				Key:        "PROJ-1",
				Type:       tt.ticketType,
				Components: []string{"backend"},
			}

			ps, err := r.ResolveProject(wi)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if ps.TodoStatus != tt.wantTodo {
				t.Errorf("todo = %q, want %q", ps.TodoStatus, tt.wantTodo)
			}
			if ps.InProgressStatus != tt.wantInProg {
				t.Errorf("in_progress = %q, want %q", ps.InProgressStatus, tt.wantInProg)
			}
			if ps.InReviewStatus != tt.wantReview {
				t.Errorf("in_review = %q, want %q", ps.InReviewStatus, tt.wantReview)
			}
		})
	}
}

func TestResolveProject_DisableErrorComments(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].DisableErrorComments = true

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ps.DisableErrorComments {
		t.Error("expected DisableErrorComments to be true")
	}
}

// --- LocateRepo ---

func TestLocateRepo_HappyPath(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-42",
		Type:       "Bug",
		Components: []string{"backend"},
	}

	owner, repo, err := r.LocateRepo(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if owner != "my-org" {
		t.Errorf("owner = %q, want %q", owner, "my-org")
	}
	if repo != "backend" {
		t.Errorf("repo = %q, want %q", repo, "backend")
	}
}

func TestLocateRepo_NoMatchingComponent(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-5",
		Type:       "Bug",
		Components: []string{"nonexistent"},
	}

	_, _, err = r.LocateRepo(wi)
	if err == nil {
		t.Fatal("expected error for non-matching component")
	}
}

func TestLocateRepo_NoComponents(t *testing.T) {
	cfg := minimalConfig()
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-5",
		Type:       "Bug",
		Components: []string{},
	}

	_, _, err = r.LocateRepo(wi)
	if err == nil {
		t.Fatal("expected error for empty components")
	}
}

func TestLocateRepo_URLParsing(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
	}{
		{
			name:      "with .git suffix",
			url:       "https://github.com/org/repo.git",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "without .git suffix",
			url:       "https://github.com/org/repo",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "with trailing slash",
			url:       "https://github.com/org/repo/",
			wantOwner: "org",
			wantRepo:  "repo",
		},
		{
			name:      "with .git and trailing slash",
			url:       "https://github.com/org/repo.git/",
			wantOwner: "org",
			wantRepo:  "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Jira.Projects[0].ComponentToRepo["backend"] = tt.url

			r, err := projectresolver.NewConfigResolver(cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			wi := models.WorkItem{
				Key:        "PROJ-1",
				Type:       "Bug",
				Components: []string{"backend"},
			}

			owner, repo, err := r.LocateRepo(wi)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

// --- helpers ---

// minimalConfig returns a Config with a single project, one component
// mapping, and Bug status transitions -- enough for most tests.
func minimalConfig() *models.Config {
	cfg := &models.Config{
		AIProvider: "claude",
		Jira: models.JiraConfig{
			Projects: []models.ProjectConfig{
				{
					ProjectKeys: models.ProjectKeys{"PROJ"},
					StatusTransitions: models.TicketTypeStatusTransitions{
						"Bug": models.StatusTransitions{
							Todo:       "To Do",
							InProgress: "In Progress",
							InReview:   "In Review",
						},
					},
					GitPullRequestFieldName: "customfield_10100",
					ComponentToRepo: models.ComponentToRepoMap{
						"backend": "https://github.com/my-org/backend.git",
					},
				},
			},
		},
	}
	cfg.GitHub.TargetBranch = "main"
	return cfg
}

// assertContains is a test helper that fails if s does not contain substr.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}
