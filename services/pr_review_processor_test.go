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

	groupedFeedback := processor.collectFeedback(reviews, comments, time.Time{})

	// Check that groupedFeedback structure is correct
	if groupedFeedback == nil {
		t.Fatal("groupedFeedback should not be nil")
	}

	// Should have 2 groups: "" (general) and "src/main.go"
	if len(groupedFeedback.Groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(groupedFeedback.Groups))
	}

	// Check general group (contains review and general comment)
	generalGroup, ok := groupedFeedback.Groups[""]
	if !ok {
		t.Fatal("Should have a general group with key ''")
	}
	if len(generalGroup.ReviewCommentMap) != 1 {
		t.Errorf("General group should have 1 review, got %d", len(generalGroup.ReviewCommentMap))
	}
	if len(generalGroup.CommentMap) != 1 {
		t.Errorf("General group should have 1 comment (general comment), got %d", len(generalGroup.CommentMap))
	}
	if !strings.Contains(generalGroup.NewFeedback, "NEW Review Feedback") {
		t.Error("General group NewFeedback should contain 'NEW Review Feedback'")
	}
	if !strings.Contains(generalGroup.NewFeedback, "reviewer1") {
		t.Error("General group should contain reviewer name")
	}
	if !strings.Contains(generalGroup.NewFeedback, "Please fix the formatting") {
		t.Error("General group should contain review body")
	}
	if !strings.Contains(generalGroup.NewFeedback, "Please add more tests") {
		t.Error("General group should contain general comment body")
	}
	if !strings.Contains(generalGroup.NewFeedback, "REVIEW_1") {
		t.Error("General group should contain REVIEW_1 ID")
	}
	if !strings.Contains(generalGroup.NewFeedback, "COMMENT_2") {
		t.Error("General group should contain COMMENT_2 ID (general comment)")
	}

	// Check src/main.go group (contains file-specific comment)
	fileGroup, ok := groupedFeedback.Groups["src/main.go"]
	if !ok {
		t.Fatal("Should have a group for 'src/main.go'")
	}
	if len(fileGroup.CommentMap) != 1 {
		t.Errorf("File group should have 1 comment, got %d", len(fileGroup.CommentMap))
	}
	if len(fileGroup.ReviewCommentMap) != 0 {
		t.Errorf("File group should have 0 reviews, got %d", len(fileGroup.ReviewCommentMap))
	}
	if !strings.Contains(fileGroup.NewFeedback, "src/main.go") {
		t.Error("File group should contain file name")
	}
	if !strings.Contains(fileGroup.NewFeedback, "This line needs improvement") {
		t.Error("File group should contain comment body")
	}
	if !strings.Contains(fileGroup.NewFeedback, "COMMENT_1") {
		t.Error("File group should contain COMMENT_1 ID")
	}

	// Check that Summary is empty (no old items)
	if groupedFeedback.Summary != "" {
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

	groupedFeedback := processor.collectFeedback([]models.GitHubReview{}, comments, time.Time{})

	// Should have 3 groups: "src/main.go", "src/util.go", and "" (general)
	if len(groupedFeedback.Groups) != 3 {
		t.Errorf("Expected 3 groups, got %d", len(groupedFeedback.Groups))
	}

	// Test single-line comment formatting in src/main.go group
	mainGroup, ok := groupedFeedback.Groups["src/main.go"]
	if !ok {
		t.Fatal("Should have a group for 'src/main.go'")
	}
	expectedSingleLine := "**Comment by reviewer1 on src/main.go:42:**"
	if !strings.Contains(mainGroup.NewFeedback, expectedSingleLine) {
		t.Errorf("Single-line comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedSingleLine, mainGroup.NewFeedback)
	}

	// Test multi-line comment formatting in src/util.go group
	utilGroup, ok := groupedFeedback.Groups["src/util.go"]
	if !ok {
		t.Fatal("Should have a group for 'src/util.go'")
	}
	expectedMultiLine := "**Comment by reviewer2 on src/util.go:95-100:**"
	if !strings.Contains(utilGroup.NewFeedback, expectedMultiLine) {
		t.Errorf("Multi-line comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedMultiLine, utilGroup.NewFeedback)
	}

	// Test general conversation comment formatting (no path/line) in general group
	generalGroup, ok := groupedFeedback.Groups[""]
	if !ok {
		t.Fatal("Should have a general group with key ''")
	}
	expectedGeneral := "**Comment by reviewer3 General comment:**"
	if !strings.Contains(generalGroup.NewFeedback, expectedGeneral) {
		t.Errorf("General comment not formatted correctly.\nExpected to contain: %s\nGot: %s", expectedGeneral, generalGroup.NewFeedback)
	}

	// Verify we don't have ":0" anywhere in general comment
	if strings.Contains(generalGroup.NewFeedback, "reviewer3 on :0") || strings.Contains(generalGroup.NewFeedback, "reviewer3 on 0") {
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
		NewFeedback:      "## NEW Review Feedback (Action Required)\n\n### COMMENT_1\n**Comment by reviewer on src/main.go:42:**\nPlease fix the formatting\n\n",
		CommentMap:       make(map[string]*models.GitHubPRComment),
		ReviewCommentMap: make(map[string]*models.GitHubReview),
	}
	summary := "Previously addressed (for context only - do not re-fix):\n- Old issue was fixed\n"

	prompt := processor.generateFeedbackPrompt(pr, feedbackData, summary)

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

	groupedFeedback := processor.collectFeedback(reviews, comments, baseTime)

	// Check that Summary contains old items (truncated)
	if !strings.Contains(groupedFeedback.Summary, "Previously addressed") {
		t.Error("Summary should contain 'Previously addressed' header")
	}
	if !strings.Contains(groupedFeedback.Summary, "Old review") {
		t.Error("Summary should contain old review content (truncated)")
	}
	if !strings.Contains(groupedFeedback.Summary, "Old comment") {
		t.Error("Summary should contain old comment content (truncated)")
	}

	// Should have 2 groups: "src/main.go" and "" (general)
	if len(groupedFeedback.Groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(groupedFeedback.Groups))
	}

	// Check src/main.go group
	fileGroup, ok := groupedFeedback.Groups["src/main.go"]
	if !ok {
		t.Fatal("Should have a group for 'src/main.go'")
	}
	if len(fileGroup.CommentMap) != 1 {
		t.Errorf("File group should have 1 new comment, got %d", len(fileGroup.CommentMap))
	}
	if !strings.Contains(fileGroup.NewFeedback, "New comment") {
		t.Error("File group should contain new comment content")
	}

	// Check general group
	generalGroup, ok := groupedFeedback.Groups[""]
	if !ok {
		t.Fatal("Should have a general group with key ''")
	}
	if len(generalGroup.ReviewCommentMap) != 1 {
		t.Errorf("General group should have 1 new review, got %d", len(generalGroup.ReviewCommentMap))
	}
	if len(generalGroup.CommentMap) != 1 {
		t.Errorf("General group should have 1 new comment, got %d", len(generalGroup.CommentMap))
	}
	if !strings.Contains(generalGroup.NewFeedback, "New review") {
		t.Error("General group should contain new review content")
	}
	if !strings.Contains(generalGroup.NewFeedback, "General conversation comment") {
		t.Error("General group should contain general conversation comment")
	}

	// Check that bot comments/reviews are excluded from summary
	if strings.Contains(groupedFeedback.Summary, "Bot review") {
		t.Error("Summary should not contain bot review")
	}
	if strings.Contains(groupedFeedback.Summary, "Bot comment") {
		t.Error("Summary should not contain bot comment")
	}

	// Check that bot comments/reviews are excluded from all group NewFeedback
	for _, group := range groupedFeedback.Groups {
		if strings.Contains(group.NewFeedback, "Bot review") {
			t.Error("NewFeedback should not contain bot review")
		}
		if strings.Contains(group.NewFeedback, "Bot comment") {
			t.Error("NewFeedback should not contain bot comment")
		}
	}

	// Check that comment/review IDs are present for new items
	if !strings.Contains(generalGroup.NewFeedback, "REVIEW_") {
		t.Error("General group NewFeedback should contain REVIEW_ ID for new review")
	}
	if !strings.Contains(fileGroup.NewFeedback, "COMMENT_") {
		t.Error("File group NewFeedback should contain COMMENT_ IDs for new comments")
	}
	if !strings.Contains(generalGroup.NewFeedback, "COMMENT_") {
		t.Error("General group NewFeedback should contain COMMENT_ IDs for new comments")
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

	groupedFeedback := processor.collectFeedback([]models.GitHubReview{}, comments, baseTime)

	// Should have 1 group for "src/main.go"
	if len(groupedFeedback.Groups) != 1 {
		t.Errorf("Expected 1 group, got %d", len(groupedFeedback.Groups))
	}

	fileGroup, ok := groupedFeedback.Groups["src/main.go"]
	if !ok {
		t.Fatal("Should have a group for 'src/main.go'")
	}

	// Verify the follow-up comment is detected
	if !strings.Contains(fileGroup.NewFeedback, "Actually, please also add error handling") {
		t.Error("NewFeedback should contain the follow-up comment")
	}

	// Verify threading context is included
	if !strings.Contains(fileGroup.NewFeedback, "Follow-up to previous discussion") {
		t.Error("NewFeedback should indicate this is a follow-up")
	}

	// Verify parent comment context is included
	if !strings.Contains(fileGroup.NewFeedback, "Previous comment by ai-bot") {
		t.Error("NewFeedback should include parent comment author")
	}
	if !strings.Contains(fileGroup.NewFeedback, "Done. Refactored the function") {
		t.Error("NewFeedback should include preview of parent comment")
	}

	// Verify old comments are in summary
	if !strings.Contains(groupedFeedback.Summary, "Please refactor this function") {
		t.Error("Summary should contain the original comment (truncated)")
	}

	// Verify only the new follow-up comment is in the map
	if len(fileGroup.CommentMap) != 1 {
		t.Errorf("Expected 1 comment in CommentMap (only the new follow-up), got %d", len(fileGroup.CommentMap))
	}
}

func TestPRReviewProcessor_IsKnownBot(t *testing.T) {
	config := &models.Config{}
	config.GitHub.KnownBotUsernames = []string{
		"github-actions[bot]",
		"coderabbitai",
		"dependabot[bot]",
	}
	processor := &PRReviewProcessorImpl{
		config: config,
	}

	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{
			name:     "exact match",
			username: "coderabbitai",
			want:     true,
		},
		{
			name:     "case insensitive match",
			username: "CodeRabbitAI",
			want:     true,
		},
		{
			name:     "bot suffix",
			username: "github-actions[bot]",
			want:     true,
		},
		{
			name:     "not a bot",
			username: "human-reviewer",
			want:     false,
		},
		{
			name:     "partial match should not match",
			username: "coderabbit",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := processor.isKnownBot(tt.username)
			if got != tt.want {
				t.Errorf("isKnownBot(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

func TestPRReviewProcessor_CalculateThreadDepth(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	processor := &PRReviewProcessorImpl{
		config: config,
		logger: zap.NewNop(),
	}

	// Build a comment thread: human -> bot -> human -> bot -> human
	comments := []models.GitHubPRComment{
		{
			ID:   100,
			User: models.GitHubUser{Login: "reviewer1"},
		},
		{
			ID:          101,
			InReplyToID: 100,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          102,
			InReplyToID: 101,
			User:        models.GitHubUser{Login: "reviewer1"},
		},
		{
			ID:          103,
			InReplyToID: 102,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          104,
			InReplyToID: 103,
			User:        models.GitHubUser{Login: "reviewer1"},
		},
	}

	commentByID := make(map[int64]*models.GitHubPRComment)
	for i := range comments {
		commentByID[comments[i].ID] = &comments[i]
	}

	tests := []struct {
		name      string
		commentID int64
		want      int
	}{
		{
			name:      "first comment (human) - depth 0",
			commentID: 100,
			want:      0,
		},
		{
			name:      "first bot reply - depth 1",
			commentID: 101,
			want:      1,
		},
		{
			name:      "second human reply - depth 1 (only counts bot)",
			commentID: 102,
			want:      1,
		},
		{
			name:      "second bot reply - depth 2",
			commentID: 103,
			want:      2,
		},
		{
			name:      "third human reply - depth 2 (only counts bot)",
			commentID: 104,
			want:      2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := processor.calculateThreadDepth(tt.commentID, commentByID)
			if got != tt.want {
				t.Errorf("calculateThreadDepth(%d) = %d, want %d", tt.commentID, got, tt.want)
			}
		})
	}
}

func TestPRReviewProcessor_ShouldSkipReply(t *testing.T) {
	config := &models.Config{}
	config.GitHub.BotUsername = "ai-bot"
	config.GitHub.MaxThreadDepth = 3
	config.GitHub.KnownBotUsernames = []string{"coderabbitai", "github-actions[bot]"}

	processor := &PRReviewProcessorImpl{
		config: config,
		logger: zap.NewNop(),
	}

	// Build a comment thread
	comments := []models.GitHubPRComment{
		{
			ID:   100,
			User: models.GitHubUser{Login: "reviewer1"},
		},
		{
			ID:          101,
			InReplyToID: 100,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          102,
			InReplyToID: 101,
			User:        models.GitHubUser{Login: "coderabbitai"}, // AI bot replying to our bot
		},
		{
			ID:          103,
			InReplyToID: 101,
			User:        models.GitHubUser{Login: "reviewer1"}, // Human replying to our bot - OK
		},
		{
			ID:          200,
			InReplyToID: 100,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          201,
			InReplyToID: 200,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          202,
			InReplyToID: 201,
			User:        models.GitHubUser{Login: "ai-bot"},
		},
		{
			ID:          203,
			InReplyToID: 202,
			User:        models.GitHubUser{Login: "reviewer1"}, // Exceeds depth limit
		},
		{
			ID:          300,
			InReplyToID: 0, // Top-level bot comment
			User:        models.GitHubUser{Login: "coderabbitai"},
		},
		{
			ID:          400,
			InReplyToID: 999, // Parent doesn't exist in map
			User:        models.GitHubUser{Login: "coderabbitai"},
		},
	}

	commentByID := make(map[int64]*models.GitHubPRComment)
	for i := range comments {
		commentByID[comments[i].ID] = &comments[i]
	}

	tests := []struct {
		name       string
		comment    *models.GitHubPRComment
		wantSkip   bool
		wantReason string
	}{
		{
			name:       "human comment - should not skip",
			comment:    &comments[0],
			wantSkip:   false,
			wantReason: "",
		},
		{
			name:       "AI bot replying to our bot - should skip",
			comment:    &comments[2], // coderabbitai replying to ai-bot
			wantSkip:   true,
			wantReason: "loop prevention",
		},
		{
			name:       "human replying to our bot - should not skip",
			comment:    &comments[3], // reviewer1 replying to ai-bot
			wantSkip:   false,
			wantReason: "",
		},
		{
			name:       "comment at depth limit - should skip",
			comment:    &comments[7], // depth is 3 (three ai-bot in chain), at max limit, would exceed if we replied
			wantSkip:   true,
			wantReason: "thread depth",
		},
		{
			name:       "top-level bot comment - should not skip",
			comment:    &comments[8], // Bot comment with no parent (InReplyToID = 0)
			wantSkip:   false,
			wantReason: "",
		},
		{
			name:       "bot replying to missing parent - should skip defensively",
			comment:    &comments[9], // Bot replying to parent not in map
			wantSkip:   true,
			wantReason: "defensive skip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSkip, gotReason := processor.shouldSkipReply(tt.comment, commentByID)
			if gotSkip != tt.wantSkip {
				t.Errorf("shouldSkipReply() skip = %v, want %v", gotSkip, tt.wantSkip)
			}
			if tt.wantSkip && !strings.Contains(gotReason, tt.wantReason) {
				t.Errorf("shouldSkipReply() reason = %q, want to contain %q", gotReason, tt.wantReason)
			}
		})
	}
}
