package jira_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/tracker"
	"jira-ai-issue-solver/tracker/jira"
	"jira-ai-issue-solver/tracker/jira/jiratest"
)

// Compile-time check: *jira.Adapter satisfies tracker.IssueTracker.
var _ tracker.IssueTracker = (*jira.Adapter)(nil)

// mustNewAdapter creates an Adapter or fails the test immediately.
func mustNewAdapter(t *testing.T, svc *jiratest.Stub) *jira.Adapter {
	t.Helper()
	adapter, err := jira.NewAdapter(svc, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAdapter: unexpected error: %v", err)
	}
	return adapter
}

// ---------------------------------------------------------------------------
// NewAdapter
// ---------------------------------------------------------------------------

func TestNewAdapter(t *testing.T) {
	t.Run("returns error for nil JiraService", func(t *testing.T) {
		_, err := jira.NewAdapter(nil, zap.NewNop())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "jira service must not be nil") {
			t.Errorf("unexpected error: %q", got)
		}
	})

	t.Run("returns error for nil logger", func(t *testing.T) {
		mock := &jiratest.Stub{}
		_, err := jira.NewAdapter(mock, nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "logger must not be nil") {
			t.Errorf("unexpected error: %q", got)
		}
	})

	t.Run("succeeds with valid arguments", func(t *testing.T) {
		mock := &jiratest.Stub{}
		adapter, err := jira.NewAdapter(mock, zap.NewNop())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if adapter == nil {
			t.Fatal("expected non-nil adapter")
		}
	})
}

// ---------------------------------------------------------------------------
// NewAdapter — contributor field resolution
// ---------------------------------------------------------------------------

func TestNewAdapter_ContributorFieldResolution(t *testing.T) {
	t.Run("resolves custom field to cf[ID] syntax", func(t *testing.T) {
		var capturedJQL string
		mock := &jiratest.Stub{
			GetFieldIDByNameFunc: func(name string) (string, error) {
				if name == "Contributors" {
					return "customfield_10466", nil
				}
				return name, nil
			},
			SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
				capturedJQL = jql
				return &models.JiraSearchResponse{}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.SearchWorkItems(models.SearchCriteria{
			ContributorIsCurrentUser: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedJQL != `cf[10466] = currentUser()` {
			t.Errorf("JQL mismatch\ngot:  %q\nwant: %q", capturedJQL, `cf[10466] = currentUser()`)
		}
	})

	t.Run("falls back to display name on lookup error", func(t *testing.T) {
		var capturedJQL string
		mock := &jiratest.Stub{
			GetFieldIDByNameFunc: func(string) (string, error) {
				return "", errors.New("field not found")
			},
			SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
				capturedJQL = jql
				return &models.JiraSearchResponse{}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.SearchWorkItems(models.SearchCriteria{
			ContributorIsCurrentUser: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedJQL != `Contributors = currentUser()` {
			t.Errorf("JQL mismatch\ngot:  %q\nwant: %q", capturedJQL, `Contributors = currentUser()`)
		}
	})

	t.Run("uses system field ID directly when not a custom field", func(t *testing.T) {
		var capturedJQL string
		mock := &jiratest.Stub{
			GetFieldIDByNameFunc: func(string) (string, error) {
				return "contributors", nil
			},
			SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
				capturedJQL = jql
				return &models.JiraSearchResponse{}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.SearchWorkItems(models.SearchCriteria{
			ContributorIsCurrentUser: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedJQL != `contributors = currentUser()` {
			t.Errorf("JQL mismatch\ngot:  %q\nwant: %q", capturedJQL, `contributors = currentUser()`)
		}
	})
}

// ---------------------------------------------------------------------------
// GetWorkItem
// ---------------------------------------------------------------------------

func TestAdapter_GetWorkItem(t *testing.T) {
	t.Run("maps all fields from a complete ticket", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-123",
					Fields: models.JiraFields{
						Summary:     "Fix the bug",
						Description: "Detailed description of the bug",
						Status:      models.JiraStatus{Name: "Open"},
						IssueType:   models.JiraIssueType{Name: "Bug"},
						Project:     models.JiraProject{Key: "PROJ"},
						Components: []models.JiraComponent{
							{Name: "backend"},
							{Name: "api"},
						},
						Labels: []string{"good-for-ai", "priority-high"},
						Assignee: &models.JiraUser{
							DisplayName:  "Jane Doe",
							EmailAddress: "jane@example.com",
							Name:         "jdoe",
						},
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(key string) (*models.JiraSecurity, error) {
				return &models.JiraSecurity{Name: "Internal"}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := &models.WorkItem{
			Key:           "PROJ-123",
			Summary:       "Fix the bug",
			Description:   "Detailed description of the bug",
			Type:          "Bug",
			Status:        "Open",
			ProjectKey:    "PROJ",
			Components:    []string{"backend", "api"},
			Labels:        []string{"good-for-ai", "priority-high"},
			Assignee:      &models.Author{Name: "Jane Doe", Email: "jane@example.com", Username: "jdoe"},
			SecurityLevel: "Internal",
		}

		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetWorkItem() mismatch\ngot:  %+v\nwant: %+v", got, want)
		}
	})

	t.Run("returns nil assignee when ticket has no assignee", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(key string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-456",
					Fields: models.JiraFields{
						Summary:   "Unassigned task",
						Status:    models.JiraStatus{Name: "Open"},
						IssueType: models.JiraIssueType{Name: "Task"},
						Project:   models.JiraProject{Key: "PROJ"},
						Assignee:  nil,
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return nil, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Assignee != nil {
			t.Errorf("expected nil Assignee, got %+v", got.Assignee)
		}
	})

	t.Run("normalizes security level None to empty string", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-789",
					Fields: models.JiraFields{
						Status:    models.JiraStatus{Name: "Open"},
						IssueType: models.JiraIssueType{Name: "Bug"},
						Project:   models.JiraProject{Key: "PROJ"},
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return &models.JiraSecurity{Name: "None"}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-789")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SecurityLevel != "" {
			t.Errorf("expected empty SecurityLevel, got %q", got.SecurityLevel)
		}
	})

	t.Run("normalizes security level none (lowercase) to empty string", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-789",
					Fields: models.JiraFields{
						Status:    models.JiraStatus{Name: "Open"},
						IssueType: models.JiraIssueType{Name: "Bug"},
						Project:   models.JiraProject{Key: "PROJ"},
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return &models.JiraSecurity{Name: "none"}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-789")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SecurityLevel != "" {
			t.Errorf("expected empty SecurityLevel, got %q", got.SecurityLevel)
		}
	})

	t.Run("returns empty security level when no security set", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-100",
					Fields: models.JiraFields{
						Status:    models.JiraStatus{Name: "Open"},
						IssueType: models.JiraIssueType{Name: "Bug"},
						Project:   models.JiraProject{Key: "PROJ"},
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return nil, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-100")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SecurityLevel != "" {
			t.Errorf("expected empty SecurityLevel, got %q", got.SecurityLevel)
		}
	})

	t.Run("returns empty slices for nil components and labels", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-200",
					Fields: models.JiraFields{
						Status:     models.JiraStatus{Name: "Open"},
						IssueType:  models.JiraIssueType{Name: "Task"},
						Project:    models.JiraProject{Key: "PROJ"},
						Components: nil,
						Labels:     nil,
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return nil, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.GetWorkItem("PROJ-200")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.Components == nil {
			t.Error("Components should be non-nil empty slice, got nil")
		}
		if len(got.Components) != 0 {
			t.Errorf("Components should be empty, got %v", got.Components)
		}
		if got.Labels == nil {
			t.Error("Labels should be non-nil empty slice, got nil")
		}
		if len(got.Labels) != 0 {
			t.Errorf("Labels should be empty, got %v", got.Labels)
		}
	})

	t.Run("propagates GetTicket error", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return nil, errors.New("connection refused")
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.GetWorkItem("PROJ-999")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "connection refused") {
			t.Errorf("expected error to contain %q, got %q", "connection refused", got)
		}
	})

	t.Run("propagates GetTicketSecurityLevel error", func(t *testing.T) {
		mock := &jiratest.Stub{
			GetTicketFunc: func(string) (*models.JiraTicketResponse, error) {
				return &models.JiraTicketResponse{
					Key: "PROJ-300",
					Fields: models.JiraFields{
						Status:    models.JiraStatus{Name: "Open"},
						IssueType: models.JiraIssueType{Name: "Bug"},
						Project:   models.JiraProject{Key: "PROJ"},
					},
				}, nil
			},
			GetTicketSecurityLevelFunc: func(string) (*models.JiraSecurity, error) {
				return nil, errors.New("security lookup failed")
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.GetWorkItem("PROJ-300")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "security lookup failed") {
			t.Errorf("expected error to contain %q, got %q", "security lookup failed", got)
		}
	})
}

// ---------------------------------------------------------------------------
// SearchWorkItems — JQL generation
// ---------------------------------------------------------------------------

func TestAdapter_SearchWorkItems_JQL(t *testing.T) {
	tests := []struct {
		name     string
		criteria models.SearchCriteria
		wantJQL  string
	}{
		{
			name: "project filter",
			criteria: models.SearchCriteria{
				ProjectKeys: []string{"PROJ1", "PROJ2"},
			},
			wantJQL: `project IN ("PROJ1", "PROJ2")`,
		},
		{
			name: "single type-status pair",
			criteria: models.SearchCriteria{
				StatusByType: map[string][]string{
					"Bug": {"Open"},
				},
			},
			wantJQL: `((issuetype = "Bug" AND status = "Open"))`,
		},
		{
			name: "multiple type-status pairs sorted by type",
			criteria: models.SearchCriteria{
				StatusByType: map[string][]string{
					"Story": {"To Do"},
					"Bug":   {"Open"},
				},
			},
			wantJQL: `((issuetype = "Bug" AND status = "Open") OR (issuetype = "Story" AND status = "To Do"))`,
		},
		{
			name: "type with multiple statuses uses IN",
			criteria: models.SearchCriteria{
				StatusByType: map[string][]string{
					"Bug": {"Open", "Reopened"},
				},
			},
			wantJQL: `((issuetype = "Bug" AND status IN ("Open", "Reopened")))`,
		},
		{
			name: "simple status filter",
			criteria: models.SearchCriteria{
				Statuses: []string{"In Progress"},
			},
			wantJQL: `status IN ("In Progress")`,
		},
		{
			name: "multiple simple statuses",
			criteria: models.SearchCriteria{
				Statuses: []string{"In Progress", "In Review"},
			},
			wantJQL: `status IN ("In Progress", "In Review")`,
		},
		{
			name: "contributor filter uses resolved field reference",
			criteria: models.SearchCriteria{
				ContributorIsCurrentUser: true,
			},
			wantJQL: `Contributors = currentUser()`,
		},
		{
			name: "labels filter",
			criteria: models.SearchCriteria{
				Labels: []string{"good-for-ai", "priority-high"},
			},
			wantJQL: `labels IN ("good-for-ai", "priority-high")`,
		},
		{
			name: "order by",
			criteria: models.SearchCriteria{
				ProjectKeys: []string{"PROJ1"},
				OrderBy:     "updated DESC",
			},
			wantJQL: `project IN ("PROJ1") ORDER BY updated DESC`,
		},
		{
			name: "order by with no other conditions",
			criteria: models.SearchCriteria{
				OrderBy: "created ASC",
			},
			wantJQL: `ORDER BY created ASC`,
		},
		{
			name: "all criteria combined",
			criteria: models.SearchCriteria{
				ProjectKeys: []string{"PROJ1"},
				StatusByType: map[string][]string{
					"Bug": {"Open"},
				},
				ContributorIsCurrentUser: true,
				Labels:                   []string{"good-for-ai"},
				OrderBy:                  "updated DESC",
			},
			wantJQL: `project IN ("PROJ1") AND ((issuetype = "Bug" AND status = "Open")) AND Contributors = currentUser() AND labels IN ("good-for-ai") ORDER BY updated DESC`,
		},
		{
			name:     "empty criteria produces empty JQL",
			criteria: models.SearchCriteria{},
			wantJQL:  "",
		},
		{
			name: "skips type with empty status list",
			criteria: models.SearchCriteria{
				StatusByType: map[string][]string{
					"Bug": {},
				},
			},
			wantJQL: "",
		},
		{
			name: "escapes double quotes in values",
			criteria: models.SearchCriteria{
				Labels: []string{`has"quote`},
			},
			wantJQL: `labels IN ("has\"quote")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedJQL string
			mock := &jiratest.Stub{
				SearchTicketsFunc: func(jql string) (*models.JiraSearchResponse, error) {
					capturedJQL = jql
					return &models.JiraSearchResponse{}, nil
				},
			}

			adapter := mustNewAdapter(t, mock)
			_, err := adapter.SearchWorkItems(tt.criteria)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedJQL != tt.wantJQL {
				t.Errorf("JQL mismatch\ngot:  %q\nwant: %q", capturedJQL, tt.wantJQL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SearchWorkItems — result mapping
// ---------------------------------------------------------------------------

func TestAdapter_SearchWorkItems_ResultMapping(t *testing.T) {
	t.Run("maps search results to work items", func(t *testing.T) {
		mock := &jiratest.Stub{
			SearchTicketsFunc: func(string) (*models.JiraSearchResponse, error) {
				return &models.JiraSearchResponse{
					IsLast: true,
					Issues: []models.JiraIssue{
						{
							Key: "PROJ-1",
							Fields: models.JiraFields{
								Summary:    "First issue",
								Status:     models.JiraStatus{Name: "Open"},
								IssueType:  models.JiraIssueType{Name: "Bug"},
								Project:    models.JiraProject{Key: "PROJ"},
								Components: []models.JiraComponent{{Name: "backend"}},
								Labels:     []string{"label-a"},
								Assignee: &models.JiraUser{
									DisplayName:  "Alice",
									EmailAddress: "alice@example.com",
									Name:         "alice",
								},
							},
						},
						{
							Key: "PROJ-2",
							Fields: models.JiraFields{
								Summary:   "Second issue",
								Status:    models.JiraStatus{Name: "To Do"},
								IssueType: models.JiraIssueType{Name: "Story"},
								Project:   models.JiraProject{Key: "PROJ"},
							},
						},
					},
				}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.SearchWorkItems(models.SearchCriteria{ProjectKeys: []string{"PROJ"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(got) != 2 {
			t.Fatalf("expected 2 results, got %d", len(got))
		}

		// First item: full fields
		if got[0].Key != "PROJ-1" {
			t.Errorf("got[0].Key = %q, want %q", got[0].Key, "PROJ-1")
		}
		if got[0].Summary != "First issue" {
			t.Errorf("got[0].Summary = %q, want %q", got[0].Summary, "First issue")
		}
		if got[0].Assignee == nil || got[0].Assignee.Username != "alice" {
			t.Errorf("got[0].Assignee unexpected: %+v", got[0].Assignee)
		}
		if !reflect.DeepEqual(got[0].Components, []string{"backend"}) {
			t.Errorf("got[0].Components = %v, want [backend]", got[0].Components)
		}

		// Second item: minimal fields, nil slices normalized
		if got[1].Key != "PROJ-2" {
			t.Errorf("got[1].Key = %q, want %q", got[1].Key, "PROJ-2")
		}
		if got[1].Assignee != nil {
			t.Errorf("got[1].Assignee should be nil, got %+v", got[1].Assignee)
		}
		if got[1].Components == nil {
			t.Error("got[1].Components should be non-nil empty slice")
		}
		if got[1].Labels == nil {
			t.Error("got[1].Labels should be non-nil empty slice")
		}
	})

	t.Run("returns empty slice for no results", func(t *testing.T) {
		mock := &jiratest.Stub{
			SearchTicketsFunc: func(string) (*models.JiraSearchResponse, error) {
				return &models.JiraSearchResponse{IsLast: true}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.SearchWorkItems(models.SearchCriteria{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("expected 0 results, got %d", len(got))
		}
	})

	t.Run("includes security level from search results when available", func(t *testing.T) {
		mock := &jiratest.Stub{
			SearchTicketsFunc: func(string) (*models.JiraSearchResponse, error) {
				return &models.JiraSearchResponse{
					IsLast: true,
					Issues: []models.JiraIssue{
						{
							Key: "SEC-1",
							Fields: models.JiraFields{
								Summary:   "Secure issue",
								Status:    models.JiraStatus{Name: "Open"},
								IssueType: models.JiraIssueType{Name: "Bug"},
								Project:   models.JiraProject{Key: "SEC"},
								Security:  &models.JiraSecurity{Name: "Confidential"},
							},
						},
					},
				}, nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		got, err := adapter.SearchWorkItems(models.SearchCriteria{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].SecurityLevel != "Confidential" {
			t.Errorf("SecurityLevel = %q, want %q", got[0].SecurityLevel, "Confidential")
		}
	})

	t.Run("propagates search error", func(t *testing.T) {
		mock := &jiratest.Stub{
			SearchTicketsFunc: func(string) (*models.JiraSearchResponse, error) {
				return nil, errors.New("jira unavailable")
			},
		}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.SearchWorkItems(models.SearchCriteria{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "jira unavailable") {
			t.Errorf("expected error to contain %q, got %q", "jira unavailable", got)
		}
	})

	t.Run("rejects invalid criteria", func(t *testing.T) {
		mock := &jiratest.Stub{}

		adapter := mustNewAdapter(t, mock)
		_, err := adapter.SearchWorkItems(models.SearchCriteria{
			StatusByType: map[string][]string{"Bug": {"Open"}},
			Statuses:     []string{"In Progress"},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "mutually exclusive") {
			t.Errorf("expected error to contain %q, got %q", "mutually exclusive", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TransitionStatus
// ---------------------------------------------------------------------------

func TestAdapter_TransitionStatus(t *testing.T) {
	t.Run("delegates to JiraService with correct arguments", func(t *testing.T) {
		var gotKey, gotStatus string
		mock := &jiratest.Stub{
			UpdateTicketStatusFunc: func(key, status string) error {
				gotKey = key
				gotStatus = status
				return nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.TransitionStatus("PROJ-123", "In Progress")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotKey != "PROJ-123" {
			t.Errorf("key = %q, want %q", gotKey, "PROJ-123")
		}
		if gotStatus != "In Progress" {
			t.Errorf("status = %q, want %q", gotStatus, "In Progress")
		}
	})

	t.Run("propagates error", func(t *testing.T) {
		mock := &jiratest.Stub{
			UpdateTicketStatusFunc: func(string, string) error {
				return errors.New("transition denied")
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.TransitionStatus("PROJ-123", "In Progress")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "transition denied") {
			t.Errorf("expected error to contain %q, got %q", "transition denied", got)
		}
	})
}

// ---------------------------------------------------------------------------
// AddComment
// ---------------------------------------------------------------------------

func TestAdapter_AddComment(t *testing.T) {
	t.Run("delegates to JiraService with correct arguments", func(t *testing.T) {
		var gotKey, gotBody string
		mock := &jiratest.Stub{
			AddCommentFunc: func(key, comment string) error {
				gotKey = key
				gotBody = comment
				return nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.AddComment("PROJ-123", "Processing started")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotKey != "PROJ-123" {
			t.Errorf("key = %q, want %q", gotKey, "PROJ-123")
		}
		if gotBody != "Processing started" {
			t.Errorf("body = %q, want %q", gotBody, "Processing started")
		}
	})

	t.Run("propagates error", func(t *testing.T) {
		mock := &jiratest.Stub{
			AddCommentFunc: func(string, string) error {
				return errors.New("comment rejected")
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.AddComment("PROJ-123", "text")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "comment rejected") {
			t.Errorf("expected error to contain %q, got %q", "comment rejected", got)
		}
	})
}

// ---------------------------------------------------------------------------
// SetFieldValue
// ---------------------------------------------------------------------------

func TestAdapter_SetFieldValue(t *testing.T) {
	t.Run("wraps value in ADF and delegates to JiraService", func(t *testing.T) {
		var gotKey, gotField string
		var gotValue any
		mock := &jiratest.Stub{
			UpdateTicketFieldByNameFunc: func(key, fieldName string, value any) error {
				gotKey = key
				gotField = fieldName
				gotValue = value
				return nil
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.SetFieldValue("PROJ-123", "Git Pull Request", "https://github.com/org/repo/pull/42")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotKey != "PROJ-123" {
			t.Errorf("key = %q, want %q", gotKey, "PROJ-123")
		}
		if gotField != "Git Pull Request" {
			t.Errorf("field = %q, want %q", gotField, "Git Pull Request")
		}

		// Value should be ADF wrapping the original string.
		wantValue := models.TextToADF("https://github.com/org/repo/pull/42")
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("value mismatch\ngot:  %v\nwant: %v", gotValue, wantValue)
		}
	})

	t.Run("propagates error", func(t *testing.T) {
		mock := &jiratest.Stub{
			UpdateTicketFieldByNameFunc: func(string, string, any) error {
				return errors.New("field update denied")
			},
		}

		adapter := mustNewAdapter(t, mock)
		err := adapter.SetFieldValue("PROJ-123", "field", "value")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); !strings.Contains(got, "field update denied") {
			t.Errorf("expected error to contain %q, got %q", "field update denied", got)
		}
	})
}
