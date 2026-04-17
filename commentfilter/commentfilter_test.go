package commentfilter_test

import (
	"testing"

	"jira-ai-issue-solver/commentfilter"
	"jira-ai-issue-solver/models"
)

// --- Filter ---

func TestFilter_RemovesIgnoredUsers(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "packit-as-a-service[bot]"}, Body: "/packit build"},
		{ID: 3, Author: models.Author{Username: "reviewer2"}, Body: "Also fix that"},
	}

	cfg := commentfilter.Config{
		BotUsername:      "ai-bot",
		IgnoredUsernames: []string{"packit-as-a-service"},
	}

	result := commentfilter.Filter(comments, cfg)

	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}
	if result[0].ID != 1 || result[1].ID != 3 {
		t.Errorf("result IDs = [%d, %d], want [1, 3]", result[0].ID, result[1].ID)
	}
}

func TestFilter_RemovesKnownBotReplyingToOurBot(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Also check X", InReplyTo: 2},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		KnownBotUsernames: []string{"coderabbitai"},
	}

	result := commentfilter.Filter(comments, cfg)

	// Comment 3 should be removed (known bot replying to our bot).
	// Comment 2 (our bot) should be preserved.
	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}
	if result[0].ID != 1 || result[1].ID != 2 {
		t.Errorf("result IDs = [%d, %d], want [1, 2]", result[0].ID, result[1].ID)
	}
}

func TestFilter_KeepsKnownBotTopLevelComment(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Initial review"},
		{ID: 2, Author: models.Author{Username: "reviewer"}, Body: "Agreed"},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		KnownBotUsernames: []string{"coderabbitai"},
	}

	result := commentfilter.Filter(comments, cfg)

	// Top-level known bot comment should be kept.
	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}
}

func TestFilter_KnownBotReplyToMissingParent_Skipped(t *testing.T) {
	comments := []models.PRComment{
		{ID: 10, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Follow-up", InReplyTo: 999},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		KnownBotUsernames: []string{"coderabbitai"},
	}

	result := commentfilter.Filter(comments, cfg)

	// Defensive skip: parent not in the set.
	if len(result) != 0 {
		t.Fatalf("got %d comments, want 0 (defensive skip)", len(result))
	}
}

func TestFilter_KnownBotReplyToNonBotParent_Kept(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Question"},
		{ID: 2, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Answer", InReplyTo: 1},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		KnownBotUsernames: []string{"coderabbitai"},
	}

	result := commentfilter.Filter(comments, cfg)

	// Known bot replying to a reviewer (not our bot) should be kept.
	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}
}

func TestFilter_ThreadDepthExceeded(t *testing.T) {
	// Thread: reviewer -> bot -> reviewer -> bot -> reviewer
	// Thread depth at comment 5 is 2 (two bot appearances in chain).
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Not quite", InReplyTo: 2},
		{ID: 4, Author: models.Author{Username: "ai-bot"}, Body: "Fixed again", InReplyTo: 3},
		{ID: 5, Author: models.Author{Username: "reviewer"}, Body: "Still wrong", InReplyTo: 4},
	}

	cfg := commentfilter.Config{
		BotUsername:    "ai-bot",
		MaxThreadDepth: 2,
	}

	result := commentfilter.Filter(comments, cfg)

	// Comment 5 has depth 2 (bot at IDs 2 and 4), which equals max.
	// Comments 1 and 3 have depth < 2.
	// Bot comments (2, 4) are preserved.
	ids := extractIDs(result)
	assertIDs(t, ids, []int64{1, 2, 3, 4})
}

func TestFilter_ThreadDepthNotExceeded(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Thanks", InReplyTo: 2},
	}

	cfg := commentfilter.Config{
		BotUsername:    "ai-bot",
		MaxThreadDepth: 5,
	}

	result := commentfilter.Filter(comments, cfg)

	// Depth at comment 3 is 1 (bot at ID 2), which is < 5.
	if len(result) != 3 {
		t.Fatalf("got %d comments, want 3", len(result))
	}
}

func TestFilter_ThreadDepthDisabledWhenZero(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Again", InReplyTo: 2},
		{ID: 4, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 3},
		{ID: 5, Author: models.Author{Username: "reviewer"}, Body: "Again", InReplyTo: 4},
	}

	cfg := commentfilter.Config{
		BotUsername:    "ai-bot",
		MaxThreadDepth: 0, // disabled
	}

	result := commentfilter.Filter(comments, cfg)

	if len(result) != 5 {
		t.Fatalf("got %d comments, want 5 (depth disabled)", len(result))
	}
}

func TestFilter_PreservesBotOwnComments(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "ai-bot"}, Body: "I started"},
		{ID: 2, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 3, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 2},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	result := commentfilter.Filter(comments, cfg)

	if len(result) != 3 {
		t.Fatalf("got %d comments, want 3 (bot comments preserved)", len(result))
	}
}

func TestFilter_EmptyInput(t *testing.T) {
	result := commentfilter.Filter(nil, commentfilter.Config{BotUsername: "ai-bot"})

	if result == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(result) != 0 {
		t.Fatalf("got %d comments, want 0", len(result))
	}
}

func TestFilter_UsernameNormalization_CaseInsensitive(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "PACKIT"}, Body: "build"},
	}

	cfg := commentfilter.Config{
		BotUsername:      "ai-bot",
		IgnoredUsernames: []string{"packit"},
	}

	result := commentfilter.Filter(comments, cfg)

	if len(result) != 0 {
		t.Fatalf("got %d comments, want 0 (case-insensitive ignore)", len(result))
	}
}

func TestFilter_UsernameNormalization_BotSuffix(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "dependabot[bot]"}, Body: "bump"},
	}

	cfg := commentfilter.Config{
		BotUsername:      "ai-bot",
		IgnoredUsernames: []string{"dependabot"},
	}

	result := commentfilter.Filter(comments, cfg)

	if len(result) != 0 {
		t.Fatalf("got %d comments, want 0 ([bot] suffix normalized)", len(result))
	}
}

func TestFilter_CycleInCommentChain(t *testing.T) {
	// Malformed data: comment 1 and 2 reference each other.
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "ai-bot"}, Body: "A", InReplyTo: 2},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "B", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "Fix", InReplyTo: 1},
	}

	cfg := commentfilter.Config{
		BotUsername:    "ai-bot",
		MaxThreadDepth: 10,
	}

	// Should not hang or panic.
	result := commentfilter.Filter(comments, cfg)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestFilter_CombinedRules(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Addressed", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Loop", InReplyTo: 2},
		{ID: 4, Author: models.Author{Username: "packit[bot]"}, Body: "/build"},
		{ID: 5, Author: models.Author{Username: "reviewer2"}, Body: "Update docs"},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		IgnoredUsernames:  []string{"packit"},
		KnownBotUsernames: []string{"coderabbitai"},
		MaxThreadDepth:    5,
	}

	result := commentfilter.Filter(comments, cfg)

	// Kept: 1 (reviewer), 2 (our bot), 5 (reviewer2)
	// Removed: 3 (known bot replying to our bot), 4 (ignored)
	ids := extractIDs(result)
	assertIDs(t, ids, []int64{1, 2, 5})
}

// --- HasNewActionable ---

func TestHasNewActionable_TrueWhenNewComments(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if !commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected true: unaddressed reviewer comment")
	}
}

func TestHasNewActionable_FalseWhenAllAddressed(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected false: all comments addressed")
	}
}

func TestHasNewActionable_FalseWhenEmpty(t *testing.T) {
	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if commentfilter.HasNewActionable(nil, cfg) {
		t.Error("expected false: no comments")
	}
}

func TestHasNewActionable_FalseWhenOnlyBotComments(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "ai-bot"}, Body: "Status update"},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected false: only bot comments")
	}
}

func TestHasNewActionable_FalseWhenOnlyIgnoredUsers(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "packit[bot]"}, Body: "/build"},
	}

	cfg := commentfilter.Config{
		BotUsername:      "ai-bot",
		IgnoredUsernames: []string{"packit"},
	}

	if commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected false: only ignored users")
	}
}

func TestHasNewActionable_TrueWhenMixedAddressedAndNew(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Old"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "reviewer"}, Body: "New"},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if !commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected true: comment 3 is new")
	}
}

func TestHasNewActionable_FalseWhenFilteredByLoopPrevention(t *testing.T) {
	// Only "new" comment is from a known bot replying to our bot.
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
		{ID: 3, Author: models.Author{Username: "coderabbitai[bot]"}, Body: "Also check", InReplyTo: 2},
	}

	cfg := commentfilter.Config{
		BotUsername:       "ai-bot",
		KnownBotUsernames: []string{"coderabbitai"},
	}

	if commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected false: only actionable comment is a bot loop")
	}
}

// --- AddressedMarker ---

func TestAddressedMarker_Format(t *testing.T) {
	got := commentfilter.AddressedMarker(12345)
	want := "<!-- addressed: 12345 -->"
	if got != want {
		t.Errorf("AddressedMarker(12345) = %q, want %q", got, want)
	}
}

// --- BotRepliedTo ---

func TestBotRepliedTo_ReviewCommentViaInReplyTo(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Fix this"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1},
	}

	replied := commentfilter.BotRepliedTo(comments, "ai-bot")

	if !replied[1] {
		t.Error("expected comment 1 to be in replied set (via InReplyTo)")
	}
	if replied[2] {
		t.Error("comment 2 is the bot's own reply, should not be in set")
	}
}

func TestBotRepliedTo_ConversationCommentViaMarker(t *testing.T) {
	marker := commentfilter.AddressedMarker(100)
	comments := []models.PRComment{
		{ID: 100, Author: models.Author{Username: "reviewer"}, Body: "Please update docs"},
		{ID: 200, Author: models.Author{Username: "ai-bot"}, Body: "Updated.\n" + marker},
	}

	replied := commentfilter.BotRepliedTo(comments, "ai-bot")

	if !replied[100] {
		t.Error("expected comment 100 to be in replied set (via marker)")
	}
}

func TestBotRepliedTo_IgnoresNonBotMarkers(t *testing.T) {
	marker := commentfilter.AddressedMarker(100)
	comments := []models.PRComment{
		{ID: 50, Author: models.Author{Username: "reviewer"}, Body: "Tricky: " + marker},
	}

	replied := commentfilter.BotRepliedTo(comments, "ai-bot")

	if replied[100] {
		t.Error("marker in non-bot comment should be ignored")
	}
}

func TestBotRepliedTo_BothMechanisms(t *testing.T) {
	marker := commentfilter.AddressedMarker(10)
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Review comment", IsReviewComment: true},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done", InReplyTo: 1, IsReviewComment: true},
		{ID: 10, Author: models.Author{Username: "reviewer"}, Body: "Conversation comment"},
		{ID: 20, Author: models.Author{Username: "ai-bot"}, Body: "Updated.\n" + marker},
	}

	replied := commentfilter.BotRepliedTo(comments, "ai-bot")

	if !replied[1] {
		t.Error("review comment 1 should be addressed via InReplyTo")
	}
	if !replied[10] {
		t.Error("conversation comment 10 should be addressed via marker")
	}
}

// --- HasNewActionable with conversation comments ---

func TestHasNewActionable_FalseWhenConversationCommentAddressedViaMarker(t *testing.T) {
	marker := commentfilter.AddressedMarker(1)
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Update docs"},
		{ID: 2, Author: models.Author{Username: "ai-bot"}, Body: "Done.\n" + marker},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected false: conversation comment addressed via marker")
	}
}

func TestHasNewActionable_TrueWhenConversationCommentNotAddressed(t *testing.T) {
	comments := []models.PRComment{
		{ID: 1, Author: models.Author{Username: "reviewer"}, Body: "Update docs"},
	}

	cfg := commentfilter.Config{BotUsername: "ai-bot"}

	if !commentfilter.HasNewActionable(comments, cfg) {
		t.Error("expected true: conversation comment not addressed")
	}
}

// --- helpers ---

func extractIDs(comments []models.PRComment) []int64 {
	ids := make([]int64, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
	}
	return ids
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got IDs %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got IDs %v, want %v", got, want)
		}
	}
}
