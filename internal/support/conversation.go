package support

import (
	"sort"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// BuildReviewConversation assembles the bot's inline review threads on
// a PR into a chronologically-ordered list of ReviewThreads, each one
// rooted on a previous bot comment and carrying every reply (user
// follow-up, bot self-reply, anyone else) in dialogue order.
//
// The walk is deliberately rooted on bot-authored top-level comments
// only: those are the threads the LLM should re-read on a re-review,
// because they are the conversations the bot started. User-only
// threads (reviewers asking each other questions, off-topic chat) are
// not relevant input to the AI and would dilute the prompt.
//
// Identifying "the bot's" comments is provider-specific (GitHub uses
// `<app-name>[bot]`, Azure DevOps uses the bot's account uniqueName);
// rather than hard-coding a list, the caller passes a predicate so the
// matcher stays out of this package and the assembler stays pure.
//
// Algorithm:
//
//  1. Collect every comment whose author satisfies isBot AND that is a
//     top-level inline comment (Line > 0, InReplyToID == 0). Each
//     becomes a thread root.
//  2. For every other inline comment whose InReplyToID points at one of
//     those roots (directly or transitively via a chain of replies),
//     append it to that thread's Comments.
//  3. Sort each thread's reply chain by comment ID (stable proxy for
//     creation order — every provider returns IDs that monotonically
//     increase by creation time).
//  4. Sort the resulting threads by FilePath + Line so the prompt's
//     conversation block reads top-to-bottom alongside the diff.
//
// PR-wide comments (Line == 0) are skipped: they are the bot's
// markers / completion annotations / failure notices, not review
// findings the LLM should re-engage with.
func BuildReviewConversation(
	comments []forgeEntities.PullRequestComment,
	isBot func(author string) bool,
) []entities.ReviewThread {
	if len(comments) == 0 || isBot == nil {
		return nil
	}

	// Index every comment by ID so the reply walk can resolve parent
	// chains in O(1) per comment.
	byID := make(map[int64]forgeEntities.PullRequestComment, len(comments))
	for _, c := range comments {
		byID[c.ID] = c
	}

	// Discover thread roots: top-level inline comments authored by the
	// bot. ThreadID is the platform-specific stable identifier for the
	// conversation (gitforge derives it from the root comment ID), so
	// it doubles as the key the reply walk attaches to.
	roots := map[int64]*entities.ReviewThread{}
	for _, c := range comments {
		if c.Line <= 0 || c.InReplyToID != 0 || !isBot(c.Author) {
			continue
		}
		roots[c.ID] = &entities.ReviewThread{
			FilePath: c.FilePath,
			Line:     c.Line,
			Comments: []entities.ReviewMessage{
				{Author: c.Author, Body: c.Body},
			},
		}
	}

	if len(roots) == 0 {
		return nil
	}

	// Append every reply that resolves to one of the bot's thread
	// roots. Replies can chain (reply-to-a-reply), so walk the
	// InReplyToID pointer up to the root.
	type orderedReply struct {
		rootID int64
		id     int64
		msg    entities.ReviewMessage
	}
	var replies []orderedReply
	for _, c := range comments {
		if c.Line <= 0 || c.InReplyToID == 0 {
			continue
		}
		rootID := walkToRoot(c, byID, roots)
		if rootID == 0 {
			continue
		}
		replies = append(replies, orderedReply{
			rootID: rootID,
			id:     c.ID,
			msg:    entities.ReviewMessage{Author: c.Author, Body: c.Body},
		})
	}

	// Sort replies by ID so the prompt reads chronologically (every
	// provider issues IDs in creation order — gitforge's int64
	// normalisation preserves that invariant).
	sort.Slice(replies, func(i, j int) bool {
		return replies[i].id < replies[j].id
	})
	for _, reply := range replies {
		root := roots[reply.rootID]
		root.Comments = append(root.Comments, reply.msg)
	}

	// Materialise + sort threads by file then line for a stable,
	// readable prompt block.
	threads := make([]entities.ReviewThread, 0, len(roots))
	for _, t := range roots {
		threads = append(threads, *t)
	}
	sort.Slice(threads, func(i, j int) bool {
		if threads[i].FilePath != threads[j].FilePath {
			return threads[i].FilePath < threads[j].FilePath
		}
		return threads[i].Line < threads[j].Line
	})
	return threads
}

// walkToRoot resolves a reply's chain of InReplyToID pointers up to a
// known thread root. Returns the matching root ID when the chain
// terminates on one of `roots`; returns 0 when the chain leads to a
// non-bot top-level comment (a user-only thread the assembler
// intentionally drops). Bounded by `maxReplyChainDepth` so a malformed
// payload with a self-referential reply cannot loop forever.
func walkToRoot(
	c forgeEntities.PullRequestComment,
	byID map[int64]forgeEntities.PullRequestComment,
	roots map[int64]*entities.ReviewThread,
) int64 {
	current := c
	for range maxReplyChainDepth {
		if current.InReplyToID == 0 {
			return 0
		}
		if _, ok := roots[current.InReplyToID]; ok {
			return current.InReplyToID
		}
		parent, ok := byID[current.InReplyToID]
		if !ok {
			return 0
		}
		current = parent
	}
	return 0
}

// maxReplyChainDepth bounds the InReplyToID walk so a malformed or
// hostile payload (a comment whose InReplyToID points at itself, or a
// cycle) cannot loop forever. 32 is comfortably above any realistic
// reply chain on a code review (the longest the team has seen in
// production is ~12).
const maxReplyChainDepth = 32

// IsBotAuthor returns a predicate the conversation walker uses to
// identify the bot's own comments. The match is substring-based and
// case-insensitive so it tolerates the per-platform variations:
// GitHub posts as `code-guru[bot]`; Azure DevOps posts as the
// configured PAT identity which often looks like
// `code-guru@<tenant>`. The single substring `code-guru` covers both.
//
// Returned as a closure (rather than a free function) so a future
// configuration can override the matcher per deployment without
// touching the assembler.
func IsBotAuthor() func(string) bool {
	const botMarker = "code-guru"
	return func(author string) bool {
		return containsFold(author, botMarker)
	}
}

// containsFold is a tiny case-insensitive substring helper local to
// this package — `strings.Contains` would force the caller to ToLower
// both sides on every call, and `strings.EqualFold` is exact-match
// only.
func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

// equalFold is `strings.EqualFold`-without-the-import — kept inline so
// the helper file has no transitive imports beyond what
// `BuildReviewConversation` already needs.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if foldByte(a[i]) != foldByte(b[i]) {
			return false
		}
	}
	return true
}

func foldByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
