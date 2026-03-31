package executor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/taskfile"
)

// --- Happy path ---

func TestExecuteFeedback_HappyPath(t *testing.T) {
	d := newFeedbackDeps(t)

	var commitBranch string
	d.git.CommitChangesFunc = func(_, _, branch, _, _ string, _ *models.Author, _ []string) (string, error) {
		commitBranch = branch
		return "abc1234567890", nil
	}

	var repliedIDs []int64
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, commentID int64, body string) error {
		repliedIDs = append(repliedIDs, commentID)
		if !strings.Contains(body, "abc1234") {
			t.Errorf("reply body = %q, should contain short SHA", body)
		}
		return nil
	}

	var syncCalls int
	d.git.SyncWithRemoteFunc = func(dir, branch string, _ []string) error {
		syncCalls++
		return nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PRURL != "https://github.com/org/repo/pull/42" {
		t.Errorf("PRURL = %q, want github URL", result.PRURL)
	}
	if result.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", result.PRNumber)
	}
	if !result.ValidationPassed {
		t.Error("expected ValidationPassed = true")
	}
	if commitBranch != "ai-bot/PROJ-1" {
		t.Errorf("commit branch = %q, want ai-bot/PROJ-1", commitBranch)
	}
	if len(repliedIDs) != 1 || repliedIDs[0] != 1 {
		t.Errorf("repliedIDs = %v, want [1]", repliedIDs)
	}
	// Sync called twice: once before AI, once after commit.
	if syncCalls != 2 {
		t.Errorf("sync calls = %d, want 2", syncCalls)
	}
}

// --- AI-generated comment responses ---

func TestExecuteFeedback_AIGeneratedReplies(t *testing.T) {
	d := newFeedbackDeps(t)

	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "abc1234567890", nil
	}

	// Write comment-responses.json before the reply step runs.
	// In the real flow the AI writes this during its session; here we
	// simulate it by writing the file to the workspace directory that
	// the pipeline will read from.
	writeCommentResponses(t, d.wsDir, `[
		{"comment_id": 1, "response": "Switched to Optional pattern as suggested."}
	]`)

	var replyBodies []string
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, _ int64, body string) error {
		replyBodies = append(replyBodies, body)
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(replyBodies) != 1 {
		t.Fatalf("reply count = %d, want 1", len(replyBodies))
	}
	if !strings.Contains(replyBodies[0], "Switched to Optional pattern") {
		t.Errorf("reply should contain AI summary, got %q", replyBodies[0])
	}
	if !strings.Contains(replyBodies[0], "abc1234") {
		t.Errorf("reply should still contain commit SHA, got %q", replyBodies[0])
	}
}

func TestExecuteFeedback_FallbackWhenNoResponsesFile(t *testing.T) {
	d := newFeedbackDeps(t)

	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "def5678901234", nil
	}

	var replyBodies []string
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, _ int64, body string) error {
		replyBodies = append(replyBodies, body)
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(replyBodies) != 1 {
		t.Fatalf("reply count = %d, want 1", len(replyBodies))
	}
	// Without AI responses, should fall back to generic message.
	if !strings.Contains(replyBodies[0], "Addressed in def5678") {
		t.Errorf("reply should be generic fallback, got %q", replyBodies[0])
	}
	if strings.Contains(replyBodies[0], "\n") {
		t.Errorf("generic reply should be single line, got %q", replyBodies[0])
	}
}

// --- Workspace recreated (self-healing) ---

func TestExecuteFeedback_WorkspaceRecreated(t *testing.T) {
	d := newFeedbackDeps(t)
	d.workspaces.FindOrCreateFunc = func(ticketKey, repoURL string) (string, bool, error) {
		return d.wsDir, false, nil // newly created
	}

	var branchSwitched bool
	d.git.SwitchBranchFunc = func(dir, name string) error {
		branchSwitched = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !branchSwitched {
		t.Error("expected SwitchBranch to be called for recreated workspace")
	}
}

// --- Sync picks up changes ---

func TestExecuteFeedback_SyncCalledBeforeAI(t *testing.T) {
	d := newFeedbackDeps(t)

	var syncBeforeExec bool
	execCalled := false

	d.git.SyncWithRemoteFunc = func(dir, branch string, _ []string) error {
		if !execCalled {
			syncBeforeExec = true
		}
		return nil
	}
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		execCalled = true
		return "", 0, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !syncBeforeExec {
		t.Error("expected SyncWithRemote to be called before AI execution")
	}
}

// --- No changes from AI ---

func TestExecuteFeedback_NoChanges(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Fatalf("expected no-changes error, got %v", err)
	}
}

// --- AI timeout ---

func TestExecuteFeedback_SessionTimeout(t *testing.T) {
	d := newFeedbackDeps(t)
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		<-ctx.Done() // wait for session timeout
		return "", 0, ctx.Err()
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		SessionTimeout:  100 * time.Millisecond,
		AIAPIKeys:       map[string]string{"claude": "test-key"},
	})

	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if !strings.Contains(err.Error(), "session timeout exceeded") {
		t.Errorf("expected session timeout error, got %v", err)
	}
}

func TestExecuteFeedback_ParentContextCancelled(t *testing.T) {
	d := newFeedbackDeps(t)

	ctx, cancel := context.WithCancel(context.Background())
	d.containers.ExecFunc = func(execCtx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		cancel() // simulate shutdown
		return "", 0, context.Canceled
	}

	p := d.pipeline(t)
	_, err := p.Execute(ctx, newFeedbackJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "job cancelled") {
		t.Fatalf("expected job cancelled error, got %v", err)
	}
}

// --- PR not found ---

func TestExecuteFeedback_PRNotFound(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return nil, errors.New("no open PR for branch")
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "find PR") {
		t.Fatalf("expected PR not found error, got %v", err)
	}
}

// --- No new comments ---

func TestExecuteFeedback_NoNewComments(t *testing.T) {
	d := newFeedbackDeps(t)
	// All comments are from the bot (no actionable feedback).
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "ai-bot"}, Body: "Addressed"},
		}, nil
	}

	containerStarted := false
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		containerStarted = true
		return &container.Container{ID: "c1", Name: "test"}, nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("expected success when no new comments, got %v", err)
	}
	if containerStarted {
		t.Error("container should not start when there are no new comments")
	}
	if result.PRURL != "" {
		t.Errorf("expected empty PRURL, got %q", result.PRURL)
	}
}

// --- Commit failure ---

func TestExecuteFeedback_CommitFails(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "", errors.New("API rate limit")
	}

	var errorComment string
	d.tracker.AddCommentFunc = func(key, body string) error {
		errorComment = body
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err == nil || !strings.Contains(err.Error(), "commit changes") {
		t.Fatalf("expected commit error, got %v", err)
	}
	if !strings.Contains(errorComment, "feedback processing failed") {
		t.Errorf("error comment = %q, should mention feedback failure", errorComment)
	}
}

// --- Status NOT reverted on failure ---

func TestExecuteFeedback_StatusNotRevertedOnFailure(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil // trigger failure
	}

	var transitions []string
	d.tracker.TransitionStatusFunc = func(key, status string) error {
		transitions = append(transitions, status)
		return nil
	}

	p := d.pipeline(t)
	_, _ = p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if len(transitions) > 0 {
		t.Errorf("expected no status transitions for feedback failure, got %v", transitions)
	}
}

// --- Error comments disabled ---

func TestExecuteFeedback_ErrorCommentsDisabled(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil // trigger failure
	}
	d.projects.ResolveProjectFunc = func(workItem models.WorkItem) (*models.ProjectSettings, error) {
		return &models.ProjectSettings{
			Owner:                "org",
			Repo:                 "repo",
			CloneURL:             "https://github.com/org/repo.git",
			BaseBranch:           "main",
			InProgressStatus:     "In Progress",
			InReviewStatus:       "In Review",
			TodoStatus:           "To Do",
			DisableErrorComments: true,
		}, nil
	}

	commentPosted := false
	d.tracker.AddCommentFunc = func(key, body string) error {
		commentPosted = true
		return nil
	}

	p := d.pipeline(t)
	_, _ = p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if commentPosted {
		t.Error("expected no error comment when disabled")
	}
}

// --- Container stopped on all paths ---

func TestExecuteFeedback_ContainerStoppedOnSuccess(t *testing.T) {
	d := newFeedbackDeps(t)

	stopped := false
	d.containers.StopFunc = func(ctx context.Context, ctr *container.Container) error {
		stopped = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopped {
		t.Error("expected container to be stopped")
	}
}

func TestExecuteFeedback_ContainerStoppedOnFailure(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.HasChangesFunc = func(dir string) (bool, error) {
		return false, nil // trigger failure
	}

	stopped := false
	d.containers.StopFunc = func(ctx context.Context, ctr *container.Container) error {
		stopped = true
		return nil
	}

	p := d.pipeline(t)
	_, _ = p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if !stopped {
		t.Error("expected container to be stopped on failure")
	}
}

// --- Session cost ---

func TestExecuteFeedback_SessionCostCaptured(t *testing.T) {
	d := newFeedbackDeps(t)
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		writeSessionOutput(t, d.wsDir, executor.SessionOutput{
			ExitCode: 0,
			CostUSD:  2.50,
		})
		return "", 0, nil
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CostUSD != 2.50 {
		t.Errorf("CostUSD = %f, want 2.50", result.CostUSD)
	}
}

// --- Co-author attribution ---

func TestExecuteFeedback_CoAuthorAttribution(t *testing.T) {
	d := newFeedbackDeps(t)
	d.tracker.GetWorkItemFunc = func(key string) (*models.WorkItem, error) {
		return &models.WorkItem{
			Key:     key,
			Summary: "Fix bug",
			Type:    "Bug",
			Assignee: &models.Author{
				Name:  "Jane Doe",
				Email: "jane@example.com",
			},
			Components: []string{},
			Labels:     []string{},
		}, nil
	}

	var receivedCoAuthor *models.Author
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, coAuthor *models.Author, _ []string) (string, error) {
		receivedCoAuthor = coAuthor
		return "abc123", nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedCoAuthor == nil || receivedCoAuthor.Name != "Jane Doe" {
		t.Errorf("expected co-author Jane Doe, got %+v", receivedCoAuthor)
	}
}

// --- Comment reply failure is non-fatal ---

func TestExecuteFeedback_ReplyFailureNonFatal(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, _ int64, _ string) error {
		return errors.New("rate limited")
	}

	p := d.pipeline(t)
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("expected success despite reply failure, got %v", err)
	}
	if result.PRURL == "" {
		t.Error("expected PR URL in result")
	}
}

// --- Multiple comments categorized correctly ---

func TestExecuteFeedback_MultipleComments(t *testing.T) {
	d := newFeedbackDeps(t)

	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer1"}, Body: "Fix this", IsReviewComment: true},
			{ID: 2, Author: models.Author{Username: "reviewer2"}, Body: "Also fix that", IsReviewComment: true},
			{ID: 3, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 1, IsReviewComment: true},
		}, nil
	}

	var taskNewComments []models.PRComment
	d.taskWriter.WriteFeedbackTaskFunc = func(pr models.PRDetails, newC, addrC []models.PRComment, dir, _, _ string) error {
		taskNewComments = newC
		return nil
	}

	var repliedIDs []int64
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, commentID int64, _ string) error {
		repliedIDs = append(repliedIDs, commentID)
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only comment 2 is new (comment 1 was addressed by bot reply 3).
	if len(taskNewComments) != 1 || taskNewComments[0].ID != 2 {
		t.Errorf("taskNewComments = %v, want [comment 2]", taskNewComments)
	}
	// Only replied to comment 2 (the new one).
	if len(repliedIDs) != 1 || repliedIDs[0] != 2 {
		t.Errorf("repliedIDs = %v, want [2]", repliedIDs)
	}
}

// --- Conversation comment reply routing ---

func TestExecuteFeedback_ConversationCommentUsesPostIssueComment(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 100, Author: models.Author{Username: "reviewer"}, Body: "Update docs", IsReviewComment: false},
		}, nil
	}
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "abc1234567890", nil
	}

	reviewReplyCalled := false
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, _ int64, _ string) error {
		reviewReplyCalled = true
		return nil
	}

	var issueCommentBody string
	d.git.PostIssueCommentFunc = func(_, _ string, _ int, body string) error {
		issueCommentBody = body
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reviewReplyCalled {
		t.Error("ReplyToComment should NOT be called for conversation comments")
	}
	if issueCommentBody == "" {
		t.Fatal("PostIssueComment should be called for conversation comments")
	}
	if !strings.Contains(issueCommentBody, "abc1234") {
		t.Errorf("issue comment should contain short SHA, got %q", issueCommentBody)
	}
	if !strings.Contains(issueCommentBody, "<!-- addressed: 100 -->") {
		t.Errorf("issue comment should contain addressed marker, got %q", issueCommentBody)
	}
}

func TestExecuteFeedback_ReviewCommentUsesReplyToComment(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this line", IsReviewComment: true},
		}, nil
	}
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "def5678901234", nil
	}

	var repliedID int64
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, commentID int64, _ string) error {
		repliedID = commentID
		return nil
	}

	issueCommentCalled := false
	d.git.PostIssueCommentFunc = func(_, _ string, _ int, _ string) error {
		issueCommentCalled = true
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repliedID != 1 {
		t.Errorf("ReplyToComment called with ID %d, want 1", repliedID)
	}
	if issueCommentCalled {
		t.Error("PostIssueComment should NOT be called for review comments")
	}
}

func TestExecuteFeedback_MixedCommentTypes(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix line", IsReviewComment: true},
			{ID: 2, Author: models.Author{Username: "reviewer"}, Body: "Update readme", IsReviewComment: false},
		}, nil
	}
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "abc1234567890", nil
	}

	var reviewRepliedIDs []int64
	d.git.ReplyToCommentFunc = func(_, _ string, _ int, commentID int64, _ string) error {
		reviewRepliedIDs = append(reviewRepliedIDs, commentID)
		return nil
	}

	var issueCommentBodies []string
	d.git.PostIssueCommentFunc = func(_, _ string, _ int, body string) error {
		issueCommentBodies = append(issueCommentBodies, body)
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviewRepliedIDs) != 1 || reviewRepliedIDs[0] != 1 {
		t.Errorf("review replies = %v, want [1]", reviewRepliedIDs)
	}
	if len(issueCommentBodies) != 1 {
		t.Fatalf("issue comments count = %d, want 1", len(issueCommentBodies))
	}
	if !strings.Contains(issueCommentBodies[0], "<!-- addressed: 2 -->") {
		t.Errorf("issue comment should contain addressed marker for ID 2, got %q", issueCommentBodies[0])
	}
}

// --- CategorizeComments unit tests ---

func TestCategorizeComments_SeparatesNewAndAddressed(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Old feedback"},
		{ID: 2, Author: models.Author{Username: "bot"}, Body: "Addressed", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "New feedback"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(newC) != 1 || newC[0].ID != 3 {
		t.Errorf("new = %v, want [comment 3]", newC)
	}
	if len(addrC) != 1 || addrC[0].ID != 1 {
		t.Errorf("addressed = %v, want [comment 1]", addrC)
	}
}

func TestCategorizeComments_ExcludesBotComments(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "bot"}, Body: "I did something"},
		{ID: 2, Author: models.Author{Username: "reviewer"}, Body: "Looks good"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(newC) != 1 || newC[0].ID != 2 {
		t.Errorf("new = %v, want [comment 2]", newC)
	}
	if len(addrC) != 0 {
		t.Errorf("addressed = %v, want empty", addrC)
	}
}

func TestCategorizeComments_AllNew(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer1"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "reviewer2"}, Body: "Fix that"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(newC) != 2 {
		t.Errorf("new count = %d, want 2", len(newC))
	}
	if len(addrC) != 0 {
		t.Errorf("addressed count = %d, want 0", len(addrC))
	}
}

func TestCategorizeComments_AllAddressed(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "bot"}, Body: "Done", InReplyTo: 1},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(newC) != 0 {
		t.Errorf("new count = %d, want 0", len(newC))
	}
	if len(addrC) != 1 {
		t.Errorf("addressed count = %d, want 1", len(addrC))
	}
}

func TestCategorizeComments_EmptyInput(t *testing.T) {
	newC, addrC := executor.CategorizeComments(nil, "bot")

	if newC == nil {
		t.Error("new should be non-nil")
	}
	if addrC == nil {
		t.Error("addressed should be non-nil")
	}
	if len(newC) != 0 || len(addrC) != 0 {
		t.Error("both slices should be empty")
	}
}

func TestCategorizeComments_NilSliceNormalization(t *testing.T) {
	// All comments from bot -- both outputs would be nil without
	// normalization.
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "bot"}, Body: "Hello"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if newC == nil {
		t.Error("new should be non-nil (empty slice, not nil)")
	}
	if addrC == nil {
		t.Error("addressed should be non-nil (empty slice, not nil)")
	}
}

func TestCategorizeComments_BotReplyToOwnComment(t *testing.T) {
	// Bot replying to its own comment should not mark anything as
	// addressed.
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "bot"}, Body: "Initial"},
		{ID: 2, Author: models.Author{Username: "bot"}, Body: "Follow-up", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Please fix"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(newC) != 1 || newC[0].ID != 3 {
		t.Errorf("new = %v, want [comment 3]", newC)
	}
	if len(addrC) != 0 {
		t.Errorf("addressed = %v, want empty", addrC)
	}
}

func TestCategorizeComments_ConversationCommentAddressedViaMarker(t *testing.T) {
	comments := []models.PRComment{
		{ID: 100, Author: models.Author{Username: "reviewer"}, Body: "Update docs"},
		{ID: 200, Author: models.Author{Username: "bot"}, Body: "Done.\n<!-- addressed: 100 -->"},
	}

	newC, addrC := executor.CategorizeComments(comments, "bot")

	if len(addrC) != 1 || addrC[0].ID != 100 {
		t.Errorf("addressed = %v, want [comment 100]", addrC)
	}
	if len(newC) != 0 {
		t.Errorf("new = %v, want empty", newC)
	}
}

func TestCategorizeComments_MarkerFromNonBotIgnored(t *testing.T) {
	// A reviewer quoting the marker text should not count.
	comments := []models.PRComment{
		{ID: 100, Author: models.Author{Username: "reviewer"}, Body: "Update docs"},
		{ID: 200, Author: models.Author{Username: "reviewer2"}, Body: "<!-- addressed: 100 -->"},
	}

	newC, _ := executor.CategorizeComments(comments, "bot")

	if len(newC) != 2 {
		t.Errorf("new count = %d, want 2 (marker from non-bot ignored)", len(newC))
	}
}

func TestCategorizeComments_NormalizesUsername(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		// Bot username has different casing and [bot] suffix.
		{ID: 2, Author: models.Author{Username: "MyBot[bot]"}, Body: "Done", InReplyTo: 1},
	}

	newC, addrC := executor.CategorizeComments(comments, "mybot")

	if len(addrC) != 1 || addrC[0].ID != 1 {
		t.Errorf("addressed = %v, want [comment 1]", addrC)
	}
	if len(newC) != 0 {
		t.Errorf("new = %v, want empty", newC)
	}
}

// --- Comment filter integration ---

func TestExecuteFeedback_IgnoredUsersFiltered(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			// Only comment is from an ignored user.
			{ID: 1, Author: models.Author{Username: "packit-as-a-service[bot]"}, Body: "/build"},
		}, nil
	}

	containerStarted := false
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		containerStarted = true
		return &container.Container{ID: "c1", Name: "test"}, nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:      "ai-bot",
		DefaultProvider:  "claude",
		AIAPIKeys:        map[string]string{"claude": "test-key"},
		IgnoredUsernames: []string{"packit-as-a-service"},
	})
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("expected success when only ignored users, got %v", err)
	}
	if containerStarted {
		t.Error("container should not start when all comments are from ignored users")
	}
	if result.PRURL != "" {
		t.Errorf("expected empty PRURL, got %q", result.PRURL)
	}
}

func TestExecuteFeedback_KnownBotLoopFiltered(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
			{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
			// Known bot replying to our bot — should be filtered.
			{ID: 3, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Also...", InReplyTo: 2},
		}, nil
	}

	containerStarted := false
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		containerStarted = true
		return &container.Container{ID: "c1", Name: "test"}, nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:       "ai-bot",
		DefaultProvider:   "claude",
		AIAPIKeys:         map[string]string{"claude": "test-key"},
		KnownBotUsernames: []string{"coderabbitai"},
	})
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("expected success when bot loop filtered, got %v", err)
	}
	if containerStarted {
		t.Error("container should not start when only actionable comment is a bot loop")
	}
	if result.PRURL != "" {
		t.Errorf("expected empty PRURL, got %q", result.PRURL)
	}
}

func TestExecuteFeedback_ThreadDepthExceeded(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
			{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
			// Depth at this comment: bot appeared once (ID 2), equals max.
			{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Still wrong", InReplyTo: 2},
		}, nil
	}

	containerStarted := false
	d.containers.StartFunc = func(ctx context.Context, cfg *container.Config, wsDir, ticketKey string, env map[string]string) (*container.Container, error) {
		containerStarted = true
		return &container.Container{ID: "c1", Name: "test"}, nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:     "ai-bot",
		DefaultProvider: "claude",
		AIAPIKeys:       map[string]string{"claude": "test-key"},
		MaxThreadDepth:  1,
	})
	result, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("expected success when thread depth exceeded, got %v", err)
	}
	if containerStarted {
		t.Error("container should not start when all comments exceed thread depth")
	}
	if result.PRURL != "" {
		t.Errorf("expected empty PRURL, got %q", result.PRURL)
	}
}

func TestExecuteFeedback_FilterKeepsActionableComments(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			// Ignored user — should be filtered.
			{ID: 1, Author: models.Author{Username: "packit[bot]"}, Body: "/build"},
			// Real reviewer comment — should pass through.
			{ID: 2, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		}, nil
	}

	var taskNewComments []models.PRComment
	d.taskWriter.WriteFeedbackTaskFunc = func(pr models.PRDetails, newC, addrC []models.PRComment, dir, _, _ string) error {
		taskNewComments = newC
		return nil
	}

	p := d.pipelineWithConfig(t, executor.Config{
		BotUsername:      "ai-bot",
		DefaultProvider:  "claude",
		AIAPIKeys:        map[string]string{"claude": "test-key"},
		IgnoredUsernames: []string{"packit"},
	})
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only comment 2 (reviewer) should be in the task — comment 1 (ignored) filtered out.
	if len(taskNewComments) != 1 || taskNewComments[0].ID != 2 {
		t.Errorf("taskNewComments = %v, want [comment 2 only]", taskNewComments)
	}
}

// --- Auth strip/restore ---

func TestExecuteFeedback_AuthStrippedBeforeAI(t *testing.T) {
	d := newFeedbackDeps(t)

	var sequence []string
	d.git.StripRemoteAuthFunc = func(dir string) error {
		sequence = append(sequence, "strip")
		return nil
	}
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		sequence = append(sequence, "exec")
		return "", 0, nil
	}
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		sequence = append(sequence, "restore")
		return nil
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// strip → exec → restore (explicit restore, then defer is a no-op because authStripped=false)
	if len(sequence) < 3 {
		t.Fatalf("sequence = %v, want at least [strip exec restore]", sequence)
	}
	if sequence[0] != "strip" {
		t.Errorf("sequence[0] = %q, want strip", sequence[0])
	}
	if sequence[1] != "exec" {
		t.Errorf("sequence[1] = %q, want exec", sequence[1])
	}
	if sequence[2] != "restore" {
		t.Errorf("sequence[2] = %q, want restore", sequence[2])
	}
}

func TestExecuteFeedback_AuthRestoredOnExecFailure(t *testing.T) {
	d := newFeedbackDeps(t)

	var restored bool
	d.git.RestoreRemoteAuthFunc = func(dir, owner, repo string) error {
		restored = true
		return nil
	}
	d.containers.ExecFunc = func(ctx context.Context, ctr *container.Container, cmd []string) (string, int, error) {
		return "", 0, errors.New("AI crashed")
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err == nil {
		t.Fatal("expected error from exec failure")
	}
	if !restored {
		t.Error("expected RestoreRemoteAuth to be called on exec failure")
	}
}

// --- ErrNoChanges in feedback ---

func TestExecuteFeedback_ErrNoChanges_ReturnsError(t *testing.T) {
	d := newFeedbackDeps(t)
	d.git.CommitChangesFunc = func(_, _, _, _, _ string, _ *models.Author, _ []string) (string, error) {
		return "", services.ErrNoChanges
	}

	p := d.pipeline(t)
	_, err := p.Execute(context.Background(), newFeedbackJob("PROJ-1"))

	if err == nil {
		t.Fatal("expected error for ErrNoChanges")
	}
	if !strings.Contains(err.Error(), "no committable changes") {
		t.Errorf("error = %q, want to mention no committable changes", err.Error())
	}
}

// --- helpers ---

func newFeedbackDeps(t *testing.T) *testDeps {
	t.Helper()
	d := newTestDeps(t)

	// Override defaults for feedback-specific methods.
	d.workspaces.FindOrCreateFunc = func(ticketKey, repoURL string) (string, bool, error) {
		return d.wsDir, true, nil // reused workspace
	}
	d.git.GetPRForBranchFunc = func(owner, repo, head string) (*models.PRDetails, error) {
		return &models.PRDetails{
			Number: 42,
			Title:  "Fix a bug",
			Branch: head,
			URL:    "https://github.com/org/repo/pull/42",
		}, nil
	}
	d.git.GetPRCommentsFunc = func(_, _ string, _ int, _ time.Time) ([]models.PRComment, error) {
		return []models.PRComment{
			{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Please fix this", IsReviewComment: true},
		}, nil
	}

	return d
}

func writeCommentResponses(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, taskfile.CommentResponsesPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFeedbackJob(ticketKey string) *jobmanager.Job {
	return &jobmanager.Job{
		ID:         "job-1",
		TicketKey:  ticketKey,
		Type:       jobmanager.JobTypeFeedback,
		AttemptNum: 1,
	}
}
