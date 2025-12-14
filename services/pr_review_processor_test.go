package services

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

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
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
	}

	tests := []struct {
		name    string
		reviews []models.GitHubReview
		want    bool
	}{
		{
			name: "has changes requested",
			reviews: []models.GitHubReview{
				{
					User:  models.GitHubUser{Login: "reviewer1"},
					State: "CHANGES_REQUESTED",
				},
			},
			want: true,
		},
		{
			name: "has changes requested lowercase",
			reviews: []models.GitHubReview{
				{
					User:  models.GitHubUser{Login: "reviewer1"},
					State: "changes_requested",
				},
			},
			want: true,
		},
		{
			name: "bot changes requested should be ignored",
			reviews: []models.GitHubReview{
				{
					User:  models.GitHubUser{Login: "ai-bot"},
					State: "CHANGES_REQUESTED",
				},
			},
			want: false,
		},
		{
			name: "no changes requested",
			reviews: []models.GitHubReview{
				{
					User:  models.GitHubUser{Login: "reviewer1"},
					State: "APPROVED",
				},
				{
					User:  models.GitHubUser{Login: "reviewer2"},
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
		logger: zap.NewNop(),
	}

	reviews := []models.GitHubReview{
		{
			User: models.GitHubUser{
				Login: "reviewer1",
			},
			Body:        "Please fix the formatting",
			State:       "CHANGES_REQUESTED",
			SubmittedAt: time.Now(),
		},
	}

	comments := []models.GitHubPRComment{
		{
			User: models.GitHubUser{
				Login: "commenter1",
			},
			Body:      "This line needs improvement",
			Path:      "src/main.go",
			Line:      42,
			CreatedAt: time.Now(),
		},
		{
			User: models.GitHubUser{
				Login: "commenter2",
			},
			Body:      "Please add more tests",
			Path:      "",
			Line:      0,
			CreatedAt: time.Now(),
		},
	}

	feedbackData := processor.collectFeedback(reviews, comments, time.Time{})

	// Check that feedbackData structure is correct
	if feedbackData == nil {
		t.Fatal("feedbackData should not be nil")
	}

	// Check NewFeedback contains expected content
	if !strings.Contains(feedbackData.NewFeedback, "NEW Review Feedback") {
		t.Error("NewFeedback should contain 'NEW Review Feedback'")
	}
	if !strings.Contains(feedbackData.NewFeedback, "reviewer1") {
		t.Error("NewFeedback should contain reviewer name")
	}
	if !strings.Contains(feedbackData.NewFeedback, "commenter1") {
		t.Error("NewFeedback should contain commenter name")
	}
	if !strings.Contains(feedbackData.NewFeedback, "src/main.go") {
		t.Error("NewFeedback should contain file name")
	}
	if !strings.Contains(feedbackData.NewFeedback, "Please fix the formatting") {
		t.Error("NewFeedback should contain review body")
	}
	if !strings.Contains(feedbackData.NewFeedback, "This line needs improvement") {
		t.Error("NewFeedback should contain line-based comment body")
	}
	if !strings.Contains(feedbackData.NewFeedback, "Please add more tests") {
		t.Error("NewFeedback should contain general comment body")
	}
	if !strings.Contains(feedbackData.NewFeedback, "commenter2") {
		t.Error("NewFeedback should contain second commenter name")
	}

	// Check that comment/review IDs are present
	if !strings.Contains(feedbackData.NewFeedback, "REVIEW_1") {
		t.Error("NewFeedback should contain REVIEW_1 ID")
	}
	if !strings.Contains(feedbackData.NewFeedback, "COMMENT_1") {
		t.Error("NewFeedback should contain COMMENT_1 ID")
	}
	if !strings.Contains(feedbackData.NewFeedback, "COMMENT_2") {
		t.Error("NewFeedback should contain COMMENT_2 ID")
	}

	// Check that maps are populated
	if len(feedbackData.CommentMap) != 2 {
		t.Errorf("Expected 2 comments in CommentMap, got %d", len(feedbackData.CommentMap))
	}
	if len(feedbackData.ReviewCommentMap) != 1 {
		t.Errorf("Expected 1 review in ReviewCommentMap, got %d", len(feedbackData.ReviewCommentMap))
	}

	// Check that Summary is empty (no old items)
	if feedbackData.Summary != "" {
		t.Error("Summary should be empty when there are no old items")
	}
}

func TestPRReviewProcessor_CollectFeedback_CommentFormatting(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
		logger: zap.NewNop(),
	}

	comments := []models.GitHubPRComment{
		{
			User:      models.GitHubUser{Login: "reviewer1"},
			Body:      "Single line comment",
			Path:      "src/main.go",
			Line:      42,
			StartLine: 0, // Single line - no start line
			CreatedAt: time.Now(),
		},
		{
			User:      models.GitHubUser{Login: "reviewer2"},
			Body:      "Multi-line comment",
			Path:      "src/util.go",
			Line:      100,
			StartLine: 95, // Multi-line range: 95-100
			CreatedAt: time.Now(),
		},
		{
			User:      models.GitHubUser{Login: "reviewer3"},
			Body:      "General conversation comment",
			Path:      "", // No path
			Line:      0,  // No line
			StartLine: 0,
			CreatedAt: time.Now(),
		},
	}

	feedbackData := processor.collectFeedback([]models.GitHubReview{}, comments, time.Time{})

	// Test single-line comment formatting
	expectedSingleLine := "**Comment by reviewer1 on src/main.go:42:**"
	if !strings.Contains(feedbackData.NewFeedback, expectedSingleLine) {
		t.Errorf("Single-line comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedSingleLine, feedbackData.NewFeedback)
	}

	// Test multi-line comment formatting
	expectedMultiLine := "**Comment by reviewer2 on src/util.go:95-100:**"
	if !strings.Contains(feedbackData.NewFeedback, expectedMultiLine) {
		t.Errorf("Multi-line comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedMultiLine, feedbackData.NewFeedback)
	}

	// Test general conversation comment formatting (no path/line)
	expectedGeneral := "**Comment by reviewer3 General comment:**"
	if !strings.Contains(feedbackData.NewFeedback, expectedGeneral) {
		t.Errorf("General comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedGeneral, feedbackData.NewFeedback)
	}

	// Verify we don't have ":0" anywhere in general comment
	if strings.Contains(feedbackData.NewFeedback, "reviewer3 on :0") || strings.Contains(feedbackData.NewFeedback, "reviewer3 on 0") {
		t.Error("General comment should not contain ':0' or spurious path/line references")
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

	feedbackData := &FeedbackData{
		Summary:          "Previously addressed (for context only - do not re-fix):\n- Old issue was fixed\n",
		NewFeedback:      "## NEW Review Feedback (Action Required)\n\n### COMMENT_1\n**Comment by reviewer on src/main.go:42:**\nPlease fix the formatting\n\n",
		CommentMap:       make(map[string]*models.GitHubPRComment),
		ReviewCommentMap: make(map[string]*models.GitHubReview),
	}

	prompt := processor.generateFeedbackPrompt(pr, feedbackData)

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
	if !strings.Contains(prompt, "Previously addressed") {
		t.Error("Prompt should contain summary of previously addressed items")
	}
	if !strings.Contains(prompt, "NEW Review Feedback") {
		t.Error("Prompt should contain NEW feedback section")
	}
	if !strings.Contains(prompt, "COMMENT_1_RESPONSE:") {
		t.Error("Prompt should contain instructions for structured response format")
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

func TestPRReviewProcessor_ParseCommentResponses(t *testing.T) {
	processor := &PRReviewProcessorImpl{
		logger: zap.NewNop(),
	}

	// Test AI output with multiple comment responses
	aiOutput := `I've made the following changes to address the feedback:

COMMENT_1_RESPONSE:
I fixed the formatting issue by adding proper indentation to the function. The code now follows the project's style guide.

COMMENT_2_RESPONSE:
Added comprehensive unit tests for the new feature in test/feature_test.go. Coverage is now at 95%.

REVIEW_1_RESPONSE:
Updated the error handling as suggested. Now using structured errors with proper context wrapping.

Some other text here that should be ignored.

COMMENT_3_RESPONSE:
Refactored the logic to use a more efficient algorithm. Time complexity is now O(n) instead of O(n²).
`

	expectedIDs := []string{"COMMENT_1", "COMMENT_2", "COMMENT_3", "REVIEW_1"}
	responses := processor.parseCommentResponses(aiOutput, expectedIDs)

	// Check that all responses were parsed
	if len(responses) != 4 {
		t.Errorf("Expected 4 responses, got %d", len(responses))
	}

	// Check COMMENT_1
	if response, ok := responses["COMMENT_1"]; ok {
		expected := "I fixed the formatting issue by adding proper indentation to the function. The code now follows the project's style guide."
		if response != expected {
			t.Errorf("COMMENT_1 response mismatch.\nExpected: %s\nGot: %s", expected, response)
		}
	} else {
		t.Error("COMMENT_1 response not found")
	}

	// Check COMMENT_2
	if response, ok := responses["COMMENT_2"]; ok {
		expected := "Added comprehensive unit tests for the new feature in test/feature_test.go. Coverage is now at 95%."
		if response != expected {
			t.Errorf("COMMENT_2 response mismatch.\nExpected: %s\nGot: %s", expected, response)
		}
	} else {
		t.Error("COMMENT_2 response not found")
	}

	// Check REVIEW_1
	if response, ok := responses["REVIEW_1"]; ok {
		// Parser should stop at double newline, excluding extraneous text
		expected := "Updated the error handling as suggested. Now using structured errors with proper context wrapping."
		if response != expected {
			t.Errorf("REVIEW_1 response mismatch.\nExpected: %s\nGot: %s", expected, response)
		}
	} else {
		t.Error("REVIEW_1 response not found")
	}

	// Check COMMENT_3
	if response, ok := responses["COMMENT_3"]; ok {
		expected := "Refactored the logic to use a more efficient algorithm. Time complexity is now O(n) instead of O(n²)."
		if response != expected {
			t.Errorf("COMMENT_3 response mismatch.\nExpected: %s\nGot: %s", expected, response)
		}
	} else {
		t.Error("COMMENT_3 response not found")
	}
}

func TestPRReviewProcessor_ParseCommentResponses_EmptyOutput(t *testing.T) {
	processor := &PRReviewProcessorImpl{
		logger: zap.NewNop(),
	}

	responses := processor.parseCommentResponses("", []string{})

	if len(responses) != 0 {
		t.Errorf("Expected 0 responses for empty output, got %d", len(responses))
	}
}

func TestPRReviewProcessor_ParseCommentResponses_NoValidResponses(t *testing.T) {
	processor := &PRReviewProcessorImpl{
		logger: zap.NewNop(),
	}

	aiOutput := `Some text without any valid response markers.
This should not match the pattern.
Even if it has COMMENT or REVIEW in it.`

	responses := processor.parseCommentResponses(aiOutput, []string{})

	if len(responses) != 0 {
		t.Errorf("Expected 0 responses for output without valid markers, got %d", len(responses))
	}
}

func TestPRReviewProcessor_CollectFeedbackWithHandlingStatus(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
		logger: zap.NewNop(),
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
			User:      models.GitHubUser{Login: "commenter3"},
			Body:      "General conversation comment",
			Path:      "",
			Line:      0,
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

	feedbackData := processor.collectFeedback(reviews, comments, baseTime)

	// Check that Summary contains old items (truncated)
	if !strings.Contains(feedbackData.Summary, "Previously addressed") {
		t.Error("Summary should contain 'Previously addressed' header")
	}
	if !strings.Contains(feedbackData.Summary, "Old review") {
		t.Error("Summary should contain old review content (truncated)")
	}
	if !strings.Contains(feedbackData.Summary, "Old comment") {
		t.Error("Summary should contain old comment content (truncated)")
	}

	// Check that NewFeedback contains new items with IDs
	if !strings.Contains(feedbackData.NewFeedback, "NEW Review Feedback") {
		t.Error("NewFeedback should contain 'NEW Review Feedback' header")
	}
	if !strings.Contains(feedbackData.NewFeedback, "New review") {
		t.Error("NewFeedback should contain new review content")
	}
	if !strings.Contains(feedbackData.NewFeedback, "New comment") {
		t.Error("NewFeedback should contain new comment content")
	}
	if !strings.Contains(feedbackData.NewFeedback, "General conversation comment") {
		t.Error("NewFeedback should contain general conversation comment")
	}

	// Check that bot comments/reviews are excluded from both
	if strings.Contains(feedbackData.Summary, "Bot review") {
		t.Error("Summary should not contain bot review")
	}
	if strings.Contains(feedbackData.Summary, "Bot comment") {
		t.Error("Summary should not contain bot comment")
	}
	if strings.Contains(feedbackData.NewFeedback, "Bot review") {
		t.Error("NewFeedback should not contain bot review")
	}
	if strings.Contains(feedbackData.NewFeedback, "Bot comment") {
		t.Error("NewFeedback should not contain bot comment")
	}

	// Check that comment/review IDs are present for new items
	if !strings.Contains(feedbackData.NewFeedback, "REVIEW_") {
		t.Error("NewFeedback should contain REVIEW_ ID for new review")
	}
	if !strings.Contains(feedbackData.NewFeedback, "COMMENT_") {
		t.Error("NewFeedback should contain COMMENT_ IDs for new comments")
	}

	// Check that maps contain only new items
	if len(feedbackData.CommentMap) != 2 {
		t.Errorf("Expected 2 comments in CommentMap (new items only), got %d", len(feedbackData.CommentMap))
	}
	if len(feedbackData.ReviewCommentMap) != 1 {
		t.Errorf("Expected 1 review in ReviewCommentMap (new items only), got %d", len(feedbackData.ReviewCommentMap))
	}
}

func TestPRReviewProcessor_CollectFeedback_ThreadedReplies(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
		logger: zap.NewNop(),
	}

	baseTime := time.Date(2024, 7, 10, 12, 0, 0, 0, time.UTC)
	oldTime := baseTime.Add(-2 * time.Hour)
	newTime := baseTime.Add(1 * time.Hour)

	comments := []models.GitHubPRComment{
		{
			ID:        100,
			User:      models.GitHubUser{Login: "reviewer1"},
			Body:      "Please refactor this function",
			Path:      "src/main.go",
			Line:      42,
			CreatedAt: oldTime,
		},
		{
			ID:          101,
			InReplyToID: 100, // Reply to comment 100
			User:        models.GitHubUser{Login: "ai-bot"},
			Body:        "Done. Refactored the function as requested.",
			Path:        "src/main.go",
			Line:        42,
			CreatedAt:   oldTime.Add(10 * time.Minute),
		},
		{
			ID:          102,
			InReplyToID: 101, // Follow-up reply to bot's comment
			User:        models.GitHubUser{Login: "reviewer1"},
			Body:        "Actually, please also add error handling",
			Path:        "src/main.go",
			Line:        42,
			CreatedAt:   newTime, // NEW comment after baseTime
		},
	}

	feedbackData := processor.collectFeedback([]models.GitHubReview{}, comments, baseTime)

	// Verify the follow-up comment is detected
	if !strings.Contains(feedbackData.NewFeedback, "Actually, please also add error handling") {
		t.Error("NewFeedback should contain the follow-up comment")
	}

	// Verify threading context is included
	if !strings.Contains(feedbackData.NewFeedback, "Follow-up to previous discussion") {
		t.Error("NewFeedback should indicate this is a follow-up")
	}

	// Verify parent comment context is included
	if !strings.Contains(feedbackData.NewFeedback, "Previous comment by ai-bot") {
		t.Error("NewFeedback should include parent comment author")
	}
	if !strings.Contains(feedbackData.NewFeedback, "Done. Refactored the function") {
		t.Error("NewFeedback should include preview of parent comment")
	}

	// Verify old comments are in summary
	if !strings.Contains(feedbackData.Summary, "Please refactor this function") {
		t.Error("Summary should contain the original comment (truncated)")
	}

	// Verify only the new follow-up comment is in the map
	if len(feedbackData.CommentMap) != 1 {
		t.Errorf("Expected 1 comment in CommentMap (only the new follow-up), got %d", len(feedbackData.CommentMap))
	}
}
