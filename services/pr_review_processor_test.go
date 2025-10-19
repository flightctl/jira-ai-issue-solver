package services

import (
	"strings"
	"testing"
	"time"

	"jira-ai-issue-solver/mocks"
	"jira-ai-issue-solver/models"
)

func TestPRReviewProcessor_ExtractPRInfoFromURL(t *testing.T) {
	processor := &PRReviewProcessorImpl{}

	tests := []struct {
		name      string
		prURL     string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantErr   bool
	}{
		{
			name:      "valid GitHub PR URL",
			prURL:     "https://github.com/owner/repo/pull/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   123,
			wantErr:   false,
		},
		{
			name:    "invalid URL format",
			prURL:   "https://github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "invalid PR number",
			prURL:   "https://github.com/owner/repo/pull/abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, num, err := processor.extractPRInfoFromURL(tt.prURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractPRInfoFromURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if owner != tt.wantOwner {
					t.Errorf("extractPRInfoFromURL() owner = %v, want %v", owner, tt.wantOwner)
				}
				if repo != tt.wantRepo {
					t.Errorf("extractPRInfoFromURL() repo = %v, want %v", repo, tt.wantRepo)
				}
				if num != tt.wantNum {
					t.Errorf("extractPRInfoFromURL() num = %v, want %v", num, tt.wantNum)
				}
			}
		})
	}
}

func TestPRReviewProcessor_HasRequestChangesReviews(t *testing.T) {
	processor := &PRReviewProcessorImpl{}

	tests := []struct {
		name    string
		reviews []models.GitHubReview
		want    bool
	}{
		{
			name: "has changes requested",
			reviews: []models.GitHubReview{
				{
					State: "CHANGES_REQUESTED",
				},
			},
			want: true,
		},
		{
			name: "has changes requested lowercase",
			reviews: []models.GitHubReview{
				{
					State: "changes_requested",
				},
			},
			want: true,
		},
		{
			name: "no changes requested",
			reviews: []models.GitHubReview{
				{
					State: "APPROVED",
				},
				{
					State: "COMMENTED",
				},
			},
			want: false,
		},
		{
			name:    "no reviews",
			reviews: []models.GitHubReview{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := processor.hasRequestChangesReviews(tt.reviews)
			if got != tt.want {
				t.Errorf("hasRequestChangesReviews() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRReviewProcessor_CollectFeedback(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
	}

	pr := &models.GitHubPRDetails{
		Reviews: []models.GitHubReview{
			{
				User: models.GitHubUser{
					Login: "reviewer1",
				},
				Body:  "Please fix the formatting",
				State: "CHANGES_REQUESTED",
			},
		},
		Comments: []models.GitHubPRComment{
			{
				User: models.GitHubUser{
					Login: "commenter1",
				},
				Body: "This line needs improvement",
				Path: "src/main.go",
				Line: 42,
			},
		},
	}

	feedback := processor.collectFeedback(pr.Reviews, pr.Comments, time.Time{})

	// Check that feedback contains expected content
	if !strings.Contains(feedback, "PR Review Feedback") {
		t.Error("Feedback should contain 'PR Review Feedback'")
	}
	if !strings.Contains(feedback, "reviewer1") {
		t.Error("Feedback should contain reviewer name")
	}
	if !strings.Contains(feedback, "commenter1") {
		t.Error("Feedback should contain commenter name")
	}
	if !strings.Contains(feedback, "src/main.go") {
		t.Error("Feedback should contain file name")
	}
	if !strings.Contains(feedback, "Please fix the formatting") {
		t.Error("Feedback should contain review body")
	}
	if !strings.Contains(feedback, "This line needs improvement") {
		t.Error("Feedback should contain comment body")
	}
}

func TestPRReviewProcessor_GenerateFeedbackPrompt(t *testing.T) {
	processor := &PRReviewProcessorImpl{}

	pr := &models.GitHubPRDetails{
		Number:  123,
		Title:   "Test PR",
		Body:    "Test description",
		HTMLURL: "https://github.com/owner/repo/pull/123",
		Head: models.GitHubRef{
			Ref: "feature-branch",
			Repo: models.GitHubRepository{
				CloneURL: "https://github.com/botuser/repo.git",
			},
		},
		Files: []models.GitHubPRFile{
			{
				Filename: "src/main.go",
				Status:   "modified",
				Patch:    "@@ -1,3 +1,4 @@\n func main() {\n+    fmt.Println(\"Hello\")\n     return\n }",
			},
		},
	}

	feedback := "Please fix the formatting"

	prompt := processor.generateFeedbackPrompt(pr, feedback)

	// Check that prompt contains expected content
	if !strings.Contains(prompt, "Test PR") {
		t.Error("Prompt should contain PR title")
	}
	if !strings.Contains(prompt, "Test description") {
		t.Error("Prompt should contain PR description")
	}
	if !strings.Contains(prompt, "src/main.go") {
		t.Error("Prompt should contain file name")
	}
	if !strings.Contains(prompt, "Please fix the formatting") {
		t.Error("Prompt should contain feedback")
	}
	if !strings.Contains(prompt, "Apply the necessary fixes") {
		t.Error("Prompt should contain instructions")
	}
}

func TestPRReviewProcessor_GetRepositoryURLFromPR(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "test-bot"

	processor := &PRReviewProcessorImpl{
		config: config,
	}

	pr := &models.GitHubPRDetails{
		Head: models.GitHubRef{
			Repo: models.GitHubRepository{
				CloneURL: "https://github.com/test-bot/repo.git",
			},
		},
	}

	repoURL, err := processor.getRepositoryURLFromPR(pr)
	if err != nil {
		t.Errorf("getRepositoryURLFromPR() error = %v", err)
		return
	}

	expected := "https://github.com/test-bot/repo.git"
	if repoURL != expected {
		t.Errorf("getRepositoryURLFromPR() = %v, want %v", repoURL, expected)
	}
}

func TestPRReviewProcessor_GetRepositoryURLFromPR_EmptyCloneURL(t *testing.T) {
	processor := &PRReviewProcessorImpl{}

	pr := &models.GitHubPRDetails{
		Head: models.GitHubRef{
			Repo: models.GitHubRepository{
				CloneURL: "",
			},
		},
	}

	_, err := processor.getRepositoryURLFromPR(pr)
	if err == nil {
		t.Error("getRepositoryURLFromPR() should return error for empty clone URL")
	}
}

func TestPRReviewProcessor_GetLastProcessingTimestamp(t *testing.T) {
	mockGitHub := &mocks.MockGitHubService{
		ListPRCommentsFunc: func(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
			return []models.GitHubPRComment{
				{
					User: models.GitHubUser{Login: "ai-bot"},
					Body: "🤖 AI Processing Timestamp: 2024-07-10T12:00:00Z\n\nAI has processed feedback for ticket TEST-123 at this time.",
				},
				{
					User: models.GitHubUser{Login: "reviewer"},
					Body: "Some other comment",
				},
			}, nil
		},
	}
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		githubService: mockGitHub,
		config:        config,
	}
	ts, err := processor.getLastProcessingTimestamp("owner", "repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Format(time.RFC3339) != "2024-07-10T12:00:00Z" {
		t.Errorf("expected timestamp 2024-07-10T12:00:00Z, got %s", ts.Format(time.RFC3339))
	}
}

func TestPRReviewProcessor_GetLastProcessingTimestamp_MultipleTimestamps(t *testing.T) {
	mockGitHub := &mocks.MockGitHubService{
		ListPRCommentsFunc: func(owner, repo string, prNumber int) ([]models.GitHubPRComment, error) {
			return []models.GitHubPRComment{
				{
					User:      models.GitHubUser{Login: "ai-bot"},
					Body:      "🤖 AI Processing Timestamp: 2024-07-10T10:00:00Z\n\nAI has processed feedback for ticket TEST-123 at this time.",
					CreatedAt: time.Date(2024, 7, 10, 10, 0, 0, 0, time.UTC),
				},
				{
					User:      models.GitHubUser{Login: "reviewer"},
					Body:      "Some other comment",
					CreatedAt: time.Date(2024, 7, 10, 11, 0, 0, 0, time.UTC),
				},
				{
					User:      models.GitHubUser{Login: "ai-bot"},
					Body:      "🤖 AI Processing Timestamp: 2024-07-10T12:00:00Z\n\nAI has processed feedback for ticket TEST-123 at this time.",
					CreatedAt: time.Date(2024, 7, 10, 12, 0, 0, 0, time.UTC),
				},
				{
					User:      models.GitHubUser{Login: "ai-bot"},
					Body:      "🤖 AI Processing Timestamp: 2024-07-10T09:00:00Z\n\nAI has processed feedback for ticket TEST-123 at this time.",
					CreatedAt: time.Date(2024, 7, 10, 9, 0, 0, 0, time.UTC),
				},
			}, nil
		},
	}
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		githubService: mockGitHub,
		config:        config,
	}
	ts, err := processor.getLastProcessingTimestamp("owner", "repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 7, 10, 12, 0, 0, 0, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("expected timestamp %v, got %v", expected, ts)
	}
}

func TestPRReviewProcessor_UpdateProcessingTimestamp(t *testing.T) {
	var called bool
	mockGitHub := &mocks.MockGitHubService{
		AddPRCommentFunc: func(owner, repo string, prNumber int, body string) error {
			called = true
			if !strings.Contains(body, "🤖 AI Processing Timestamp:") {
				t.Errorf("body should contain timestamp")
			}
			return nil
		},
	}
	mockJira := &mocks.MockJiraService{
		HasSecurityLevelFunc: func(ticketKey string) (bool, error) {
			return false, nil // No security level for test
		},
	}
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		jiraService:   mockJira,
		githubService: mockGitHub,
		config:        config,
	}
	err := processor.updateProcessingTimestamp("owner", "repo", 1, "TEST-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("AddPRComment was not called")
	}
}

func TestPRReviewProcessor_CollectFeedbackWithHandlingStatus(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
	}

	baseTime := time.Date(2024, 7, 10, 12, 0, 0, 0, time.UTC)
	oldTime := baseTime.Add(-1 * time.Hour)
	newTime := baseTime.Add(1 * time.Hour)

	reviews := []models.GitHubReview{
		{
			User:        models.GitHubUser{Login: "reviewer1"},
			Body:        "Old review",
			State:       "CHANGES_REQUESTED",
			SubmittedAt: oldTime,
		},
		{
			User:        models.GitHubUser{Login: "reviewer2"},
			Body:        "New review",
			State:       "CHANGES_REQUESTED",
			SubmittedAt: newTime,
		},
		{
			User:        models.GitHubUser{Login: "ai-bot"},
			Body:        "Bot review",
			State:       "APPROVED",
			SubmittedAt: newTime,
		},
	}

	comments := []models.GitHubPRComment{
		{
			User:      models.GitHubUser{Login: "commenter1"},
			Body:      "Old comment",
			Path:      "src/main.go",
			Line:      42,
			CreatedAt: oldTime,
		},
		{
			User:      models.GitHubUser{Login: "commenter2"},
			Body:      "New comment",
			Path:      "src/main.go",
			Line:      50,
			CreatedAt: newTime,
		},
		{
			User:      models.GitHubUser{Login: "ai-bot"},
			Body:      "Bot comment",
			Path:      "src/main.go",
			Line:      60,
			CreatedAt: newTime,
		},
	}

	feedback := processor.collectFeedback(reviews, comments, baseTime)

	// Check that feedback contains handling status
	if !strings.Contains(feedback, "✅ HANDLED") {
		t.Error("Feedback should contain '✅ HANDLED' for old items")
	}
	if !strings.Contains(feedback, "🔄 NEW") {
		t.Error("Feedback should contain '🔄 NEW' for new items")
	}
	if !strings.Contains(feedback, "Old review") {
		t.Error("Feedback should contain old review content")
	}
	if !strings.Contains(feedback, "New review") {
		t.Error("Feedback should contain new review content")
	}
	if !strings.Contains(feedback, "Old comment") {
		t.Error("Feedback should contain old comment content")
	}
	if !strings.Contains(feedback, "New comment") {
		t.Error("Feedback should contain new comment content")
	}
	if strings.Contains(feedback, "Bot review") {
		t.Error("Feedback should not contain bot review")
	}
	if strings.Contains(feedback, "Bot comment") {
		t.Error("Feedback should not contain bot comment")
	}
}
