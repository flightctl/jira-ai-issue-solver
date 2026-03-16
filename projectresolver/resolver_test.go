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
	cfg.Jira.Projects[0].Components["frontend"] = models.ComponentConfig{
		Repo: "https://github.com/my-org/frontend.git", Profile: "default",
	}

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
	cfg.Jira.Projects[0].Components["frontend"] = models.ComponentConfig{
		Repo: "https://github.com/my-org/frontend", Profile: "default",
	}

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
	assertContains(t, err.Error(), "no component mapping found")
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
	cfg.Jira.Projects[0].Components["backend"] = models.ComponentConfig{
		Repo: "https://github.com/my-org/backend", Profile: "default",
	}

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
	cfg.Jira.Projects[0].Components["backend"] = models.ComponentConfig{
		Repo: "https://github.com/my-org/backend/", Profile: "default",
	}

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
			cfg.Jira.Projects[0].Components["backend"] = models.ComponentConfig{
				Repo: tt.url, Profile: "default",
			}

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

// --- Case-insensitive component matching ---

func TestResolveProject_ComponentMatchingCaseInsensitive(t *testing.T) {
	// Viper lowercases YAML map keys, so "FlightCtl" in YAML becomes
	// "flightctl" in the loaded config. The component from Jira retains
	// original casing ("FlightCtl-Core"). Case-insensitive matching
	// bridges this gap.
	cfg := minimalConfig()
	cfg.Jira.Projects[0].Components["flightctl"] = models.ComponentConfig{
		Repo: "https://github.com/org/flightctl.git", Profile: "default",
	}

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"FlightCtl"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Repo != "flightctl" {
		t.Errorf("repo = %q, want %q", ps.Repo, "flightctl")
	}
}

func TestResolveProject_ComponentMatchingExactTakesPriority(t *testing.T) {
	cfg := minimalConfig()
	// Exact match for "Backend" and case-insensitive match for "backend"
	// should both exist. Exact match should win.
	cfg.Jira.Projects[0].Components["Backend"] = models.ComponentConfig{
		Repo: "https://github.com/org/exact.git", Profile: "default",
	}
	// "backend" already exists from minimalConfig

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"Backend"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Repo != "exact" {
		t.Errorf("repo = %q, want %q (exact match should take priority)", ps.Repo, "exact")
	}
}

func TestResolveProject_ComponentMatchingUppercase(t *testing.T) {
	cfg := minimalConfig()
	// Config has lowercase "backend", Jira component is "BACKEND"
	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"BACKEND"},
	}

	ps, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Repo != "backend" {
		t.Errorf("repo = %q, want %q", ps.Repo, "backend")
	}
}

// --- Container settings passthrough ---

func TestResolveProject_ContainerSettingsPassedThrough(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].Profiles["default"] = models.Profile{
		Container: models.ContainerSettings{
			Image: "custom-image:latest",
			ResourceLimits: models.ContainerResourceLimits{
				Memory: "16g",
				CPUs:   "8",
			},
		},
	}

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

	if ps.Container.Image != "custom-image:latest" {
		t.Errorf("container image = %q, want %q", ps.Container.Image, "custom-image:latest")
	}
	if ps.Container.ResourceLimits.Memory != "16g" {
		t.Errorf("container memory = %q, want %q", ps.Container.ResourceLimits.Memory, "16g")
	}
	if ps.Container.ResourceLimits.CPUs != "8" {
		t.Errorf("container cpus = %q, want %q", ps.Container.ResourceLimits.CPUs, "8")
	}
}

// --- Imports propagation ---

func TestResolveProject_PropagatesImports(t *testing.T) {
	cfg := minimalConfig()
	cfg.Jira.Projects[0].Profiles["default"] = models.Profile{
		Imports: []models.ImportConfig{
			{Repo: "https://github.com/org/workflows", Path: ".ai-workflows", Ref: "main"},
			{Repo: "https://github.com/org/tools", Path: ".tools"},
		},
	}

	r, err := projectresolver.NewConfigResolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wi := models.WorkItem{
		Key:        "PROJ-1",
		Type:       "Bug",
		Components: []string{"backend"},
	}
	settings, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(settings.Imports) != 2 {
		t.Fatalf("len(Imports) = %d, want 2", len(settings.Imports))
	}
	if settings.Imports[0].Repo != "https://github.com/org/workflows" {
		t.Errorf("Imports[0].Repo = %q, want workflows URL", settings.Imports[0].Repo)
	}
	if settings.Imports[0].Ref != "main" {
		t.Errorf("Imports[0].Ref = %q, want %q", settings.Imports[0].Ref, "main")
	}
	if settings.Imports[1].Path != ".tools" {
		t.Errorf("Imports[1].Path = %q, want %q", settings.Imports[1].Path, ".tools")
	}
}

func TestResolveProject_NoImports_EmptySlice(t *testing.T) {
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
	settings, err := r.ResolveProject(wi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if settings.Imports == nil {
		t.Error("Imports should be non-nil empty slice")
	}
	if len(settings.Imports) != 0 {
		t.Errorf("len(Imports) = %d, want 0", len(settings.Imports))
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
					Components: models.ComponentMap{
						"backend": models.ComponentConfig{
							Repo:    "https://github.com/my-org/backend.git",
							Profile: "default",
						},
					},
					Profiles: map[string]models.Profile{
						"default": {},
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
