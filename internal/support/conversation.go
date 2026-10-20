package support

import (
	"sort"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

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
// `liveFiles` is the set of file paths in the current PR diff. Threads
// anchored to a file outside that set are dropped — without the filter,
// a re-review would see prior bot comments on lines that are no longer
// in the diff and try to "respond" inline on a stale anchor (the
// post-pipeline's `dropStaleComments` would then drop them, but only
// after the LLM has already wasted tokens deliberating). Passing nil
// keeps every thread regardless of file (used by tests that have no
// diff to compare against).
//
// Algorithm:
//
//  1. Collect every comment whose author satisfies isBot AND that opens
//     an inline conversation (Line > 0, and the first comment of its
//     provider thread — or, when the provider reports no thread, a
//     comment with no InReplyToID) AND that is anchored to a file
//     present in liveFiles. Each becomes a thread root.
//  2. Attach every other inline comment to the root of its provider
//     thread; a comment with no thread walks its InReplyToID chain
//     (directly or transitively) to a root instead.
//  3. Sort each thread's reply chain by comment ID (stable proxy for
//     creation order — every provider returns IDs that monotonically
//     increase by creation time within a thread).
//  4. Sort the resulting threads by FilePath + Line so the prompt's
//     conversation block reads top-to-bottom alongside the diff.
//
// Comments are told apart by thread AND ID, never by ID alone, and a
// comment carrying a thread joins that thread whatever its InReplyToID
// says. Both are Azure DevOps facts: it numbers comments within each
// thread, so every thread's first comment is ID 1, and it stores a reply
// posted into a thread with no parent at all — which is how the bot's own
// re-review verdicts land. Keyed on the bare ID, threads overwrote one
// another, and each of those verdicts opened a "thread" of its own that
// the next re-review answered again, so a settled thread collected more
// identical replies on every run.
//
// PR-wide comments (Line == 0) are skipped: they are the bot's
// markers / completion annotations / failure notices, not review
// findings the LLM should re-engage with.
func BuildReviewConversation(
	comments []forgeEntities.PullRequestComment,
	isBot func(author string) bool,
	liveFiles map[string]struct{},
) []entities.ReviewThread {
	if len(comments) == 0 || isBot == nil {
		return nil
	}

	// Index every comment by thread + ID so the reply walk can resolve
	// parent chains in O(1) per comment.
	byKey := make(map[commentKey]forgeEntities.PullRequestComment, len(comments))
	for _, c := range comments {
		byKey[keyOf(c)] = c
	}

	roots := discoverThreadRoots(comments, isBot, liveFiles)
	if len(roots) == 0 {
		return nil
	}
	rootOfThread := make(map[int64]commentKey, len(roots))
	for key, root := range roots {
		if root.ThreadID != 0 {
			rootOfThread[root.ThreadID] = key
		}
	}

	// Append every reply that belongs to one of the bot's thread roots:
	// by provider thread when the comment carries one, else by walking
	// its InReplyToID chain (a reply can answer a reply) up to the root.
	type orderedReply struct {
		root commentKey
		id   int64
		msg  entities.ReviewMessage
	}
	var replies []orderedReply
	for _, c := range comments {
		if c.Line <= 0 {
			continue
		}
		if _, isRoot := roots[keyOf(c)]; isRoot {
			continue
		}
		root, ok := rootFor(c, byKey, roots, rootOfThread)
		if !ok {
			continue
		}
		replies = append(replies, orderedReply{
			root: root,
			id:   c.ID,
			msg:  entities.ReviewMessage{Author: c.Author, Body: c.Body, FromBot: isBot(c.Author)},
		})
	}

	// Sort replies by ID so the prompt reads chronologically (every
	// provider issues IDs in creation order within a thread — gitforge's
	// int64 normalisation preserves that invariant).
	sort.Slice(replies, func(i, j int) bool {
		return replies[i].id < replies[j].id
	})
	for _, reply := range replies {
		root := roots[reply.root]
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

// commentKey identifies a comment within a pull request. The ID alone is
// not enough: Azure DevOps numbers comments per thread, so two threads
// both have a comment 1. GitHub's IDs are unique on their own, and a
// comment with no thread keys on its ID exactly as before.
type commentKey struct {
	thread int64
	id     int64
}

func keyOf(c forgeEntities.PullRequestComment) commentKey {
	return commentKey{thread: c.ThreadID, id: c.ID}
}

// discoverThreadRoots collects every inline comment authored by the bot
// that opens a conversation — these become the thread roots. Comments
// anchored to a file outside `liveFiles` are dropped (when liveFiles is
// non-nil) so a re-review's prompt does not include stale anchors. The
// returned map is keyed by the root comment's key so the reply walk can
// match `InReplyToID` chains in O(1).
func discoverThreadRoots(
	comments []forgeEntities.PullRequestComment,
	isBot func(author string) bool,
	liveFiles map[string]struct{},
) map[commentKey]*entities.ReviewThread {
	first := firstCommentOfEachThread(comments)
	roots := map[commentKey]*entities.ReviewThread{}
	for _, c := range comments {
		if c.Line <= 0 || !opensConversation(c, first) || !isBot(c.Author) {
			continue
		}
		if liveFiles != nil {
			if _, live := liveFiles[normalizeConversationFilePath(c.FilePath)]; !live {
				continue
			}
		}
		roots[keyOf(c)] = &entities.ReviewThread{
			FilePath:      c.FilePath,
			Line:          c.Line,
			ThreadID:      c.ThreadID,
			RootCommentID: c.ID,
			Comments: []entities.ReviewMessage{
				{Author: c.Author, Body: c.Body, FromBot: true},
			},
		}
	}
	return roots
}

// firstCommentOfEachThread maps every provider thread to the ID of the
// comment that opened it, its earliest. Only the order of IDs within one
// thread is relied on, which every provider guarantees.
func firstCommentOfEachThread(comments []forgeEntities.PullRequestComment) map[int64]int64 {
	first := map[int64]int64{}
	for _, c := range comments {
		if c.ThreadID == 0 {
			continue
		}
		if id, seen := first[c.ThreadID]; !seen || c.ID < id {
			first[c.ThreadID] = c.ID
		}
	}
	return first
}

// opensConversation reports whether c is the comment a conversation
// starts with: the first comment of its provider thread or, when the
// provider reports no thread, a comment that replies to nothing.
func opensConversation(c forgeEntities.PullRequestComment, first map[int64]int64) bool {
	if c.ThreadID != 0 {
		return first[c.ThreadID] == c.ID
	}
	return c.InReplyToID == 0
}

// rootFor resolves the thread root a reply belongs to. A comment that
// carries a provider thread joins that thread's root outright, whatever
// its InReplyToID says; one without a thread walks its InReplyToID chain.
func rootFor(
	c forgeEntities.PullRequestComment,
	byKey map[commentKey]forgeEntities.PullRequestComment,
	roots map[commentKey]*entities.ReviewThread,
	rootOfThread map[int64]commentKey,
) (commentKey, bool) {
	if c.ThreadID != 0 {
		root, ok := rootOfThread[c.ThreadID]
		return root, ok
	}
	return walkToRoot(c, byKey, roots)
}

// normalizeConversationFilePath strips a leading `/` so ADO-shape
// paths (`/internal/foo.go`) compare equal to the AI-emitted
// `internal/foo.go` form. Mirrors the same normalisation the dedup
// pipeline uses on the post side.
func normalizeConversationFilePath(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

// walkToRoot resolves a reply's chain of InReplyToID pointers up to a
// known thread root. A parent is looked up in the reply's own thread,
// where its InReplyToID points. Returns the matching root's key when the
// chain terminates on one of `roots`; returns false when the chain leads
// to a non-bot top-level comment (a user-only thread the assembler
// intentionally drops). Bounded by `maxReplyChainDepth` so a malformed
// payload with a self-referential reply cannot loop forever.
func walkToRoot(
	c forgeEntities.PullRequestComment,
	byKey map[commentKey]forgeEntities.PullRequestComment,
	roots map[commentKey]*entities.ReviewThread,
) (commentKey, bool) {
	current := c
	for range maxReplyChainDepth {
		if current.InReplyToID == 0 {
			return commentKey{}, false
		}
		parentKey := commentKey{thread: current.ThreadID, id: current.InReplyToID}
		if _, ok := roots[parentKey]; ok {
			return parentKey, true
		}
		parent, ok := byKey[parentKey]
		if !ok {
			return commentKey{}, false
		}
		current = parent
	}
	return commentKey{}, false
}

// maxReplyChainDepth bounds the InReplyToID walk so a malformed or
// hostile payload (a comment whose InReplyToID points at itself, or a
// cycle) cannot loop forever. 32 is comfortably above any realistic
// reply chain on a code review (the longest the team has seen in
// production is ~12).
const maxReplyChainDepth = 32

// IsBotAuthor returns a predicate the conversation walker uses to
// identify the bot's own comments. An author is the bot when EITHER:
//
//  1. it exactly (case-insensitively) equals one of the `identities`
//     the caller supplies — the account the deployment posts under
//     (a service-account login / email), wired from
//     `Settings.BotIdentities` and/or self-detected from the bot's own
//     PR-wide status annotations (see `DetectBotAuthors`); OR
//  2. it matches the built-in `code-guru` name shape (the default,
//     always recognised so GitHub App deployments need no config).
//
// The built-in shape match is **strict** so user names that merely
// contain `code-guru` as a substring (e.g. `code-guru-fan`,
// `alice+code-guru@example.com`) are NOT pulled into the conversation
// context as if they were prior bot findings. It matches when, after a
// case-insensitive comparison, the string starts with the literal
// `code-guru` AND the next character is one of:
//
//   - end of string (the bot identity is exactly `code-guru`)
//   - `[` — the GitHub App login shape (`code-guru[bot]`)
//   - `@` — the Azure DevOps PAT-identity shape (`code-guru@<tenant>`)
//
// Configured identities are matched by EXACT (case-insensitive) full
// equality rather than the prefix rule: they are complete account
// identities, so a substring/prefix match would risk pulling an
// unrelated user with a coincidentally overlapping name into the bot's
// conversation context.
//
// Returned as a closure (rather than a free function) so the caller
// can combine configured + self-detected identities per re-review
// without touching the assembler. Passing no identities preserves the
// pre-configuration behaviour byte-for-byte.
func IsBotAuthor(identities ...string) func(string) bool {
	const botMarker = "code-guru"
	// Copy the non-empty configured identities once so the returned
	// matcher does no allocation per call. `equalFold` already short-
	// circuits on a length mismatch, so the per-author scan is cheap
	// for the realistic 1-3 identity case.
	configured := make([]string, 0, len(identities))
	for _, id := range identities {
		if id != "" {
			configured = append(configured, id)
		}
	}
	return func(author string) bool {
		if author == "" {
			return false
		}
		for _, id := range configured {
			if equalFold(author, id) {
				return true
			}
		}
		if len(author) < len(botMarker) {
			return false
		}
		head := author[:len(botMarker)]
		if !equalFold(head, botMarker) {
			return false
		}
		if len(author) == len(botMarker) {
			return true
		}
		switch author[len(botMarker)] {
		case '[', '@':
			return true
		default:
			return false
		}
	}
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
