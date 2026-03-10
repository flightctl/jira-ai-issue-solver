// Package commentfilter provides bot-loop prevention filtering for
// PR comments.
//
// The filter removes comments that would create bot-to-bot
// conversation loops or violate thread depth limits. It is used by
// both scanners (to decide whether to emit feedback events) and
// executors (to exclude non-actionable comments before processing).
//
// Filtering rules:
//   - Comments from ignored usernames are removed entirely
//   - Known bot comments replying to our bot are removed (prevents
//     bot-to-bot ping-pong)
//   - Comments in threads where our bot has replied at or beyond
//     [Config.MaxThreadDepth] times are removed
//
// Bot's own comments are preserved in the output. Callers that need
// to split or exclude them (e.g., [executor.CategorizeComments])
// handle that separately.
package commentfilter

import (
	"strings"

	"jira-ai-issue-solver/models"
)

// Config holds bot-loop prevention settings.
type Config struct {
	// BotUsername is the bot's GitHub username, used to identify
	// the bot's own comments and calculate thread depth.
	BotUsername string

	// IgnoredUsernames lists usernames whose comments are removed
	// entirely. Use for CI/build bots whose output is not code
	// review feedback (e.g., packit-as-a-service[bot]).
	IgnoredUsernames []string

	// KnownBotUsernames lists usernames of other bots. Their
	// top-level comments are kept, but replies to our bot's
	// comments are removed to prevent bot-to-bot loops.
	KnownBotUsernames []string

	// MaxThreadDepth limits how many times our bot can appear in
	// a comment thread's ancestry. Comments in threads at or
	// exceeding this depth are removed. Zero or negative disables
	// the limit.
	MaxThreadDepth int
}

// Filter removes comments that violate bot-loop prevention rules.
// Bot's own comments are preserved (callers need them for address
// detection). The returned slice is never nil.
func Filter(comments []models.PRComment, cfg Config) []models.PRComment {
	if len(comments) == 0 {
		return []models.PRComment{}
	}

	byID := buildLookup(comments)
	normBot := normalizeUsername(cfg.BotUsername)

	result := make([]models.PRComment, 0, len(comments))
	for _, c := range comments {
		norm := normalizeUsername(c.Author.Username)

		// Keep bot's own comments (needed for address detection).
		if norm == normBot {
			result = append(result, c)
			continue
		}

		if isIgnored(norm, cfg.IgnoredUsernames) {
			continue
		}

		if isKnownBot(norm, cfg.KnownBotUsernames) && c.InReplyTo != 0 {
			if shouldSkipBotReply(c, byID, normBot) {
				continue
			}
		}

		if cfg.MaxThreadDepth > 0 {
			if threadDepth(c.ID, normBot, byID) >= cfg.MaxThreadDepth {
				continue
			}
		}

		result = append(result, c)
	}

	return result
}

// HasNewActionable reports whether any comments remain actionable
// after bot-loop prevention AND excluding comments the bot has
// already replied to.
func HasNewActionable(comments []models.PRComment, cfg Config) bool {
	filtered := Filter(comments, cfg)
	normBot := normalizeUsername(cfg.BotUsername)

	// Build set of comment IDs the bot has replied to.
	botRepliedTo := make(map[int64]bool)
	for _, c := range filtered {
		if normalizeUsername(c.Author.Username) == normBot && c.InReplyTo != 0 {
			botRepliedTo[c.InReplyTo] = true
		}
	}

	for _, c := range filtered {
		if normalizeUsername(c.Author.Username) == normBot {
			continue
		}
		if !botRepliedTo[c.ID] {
			return true
		}
	}

	return false
}

// normalizeUsername strips the GitHub [bot] suffix and lowercases
// for case-insensitive comparison.
func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSuffix(s, "[bot]"))
}

func buildLookup(comments []models.PRComment) map[int64]models.PRComment {
	m := make(map[int64]models.PRComment, len(comments))
	for _, c := range comments {
		m[c.ID] = c
	}
	return m
}

func isIgnored(normUsername string, ignored []string) bool {
	for _, u := range ignored {
		if normalizeUsername(u) == normUsername {
			return true
		}
	}
	return false
}

func isKnownBot(normUsername string, knownBots []string) bool {
	for _, b := range knownBots {
		if normalizeUsername(b) == normUsername {
			return true
		}
	}
	return false
}

// shouldSkipBotReply returns true if a known bot is replying to our
// bot's comment, or if the parent comment is missing (defensive
// skip — the parent may have been our bot's comment).
func shouldSkipBotReply(c models.PRComment, byID map[int64]models.PRComment, normBot string) bool {
	parent, found := byID[c.InReplyTo]
	if !found {
		return true
	}
	return normalizeUsername(parent.Author.Username) == normBot
}

// threadDepth counts how many times our bot appears in the comment
// chain from the given comment upward through its parents.
func threadDepth(commentID int64, normBot string, byID map[int64]models.PRComment) int {
	depth := 0
	currentID := commentID
	visited := make(map[int64]bool)

	for currentID != 0 {
		if visited[currentID] {
			break
		}
		visited[currentID] = true

		c, found := byID[currentID]
		if !found {
			break
		}

		if normalizeUsername(c.Author.Username) == normBot {
			depth++
		}

		currentID = c.InReplyTo
	}

	return depth
}
