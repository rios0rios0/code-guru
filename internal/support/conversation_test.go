package support_test

import (
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestBuildReviewConversation(t *testing.T) {
	t.Parallel()

	isBot := func(author string) bool { return author == "code-guru[bot]" }

	t.Run("should return nil when comments slice is empty", func(t *testing.T) {
		t.Parallel()

		// given / when
		got := support.BuildReviewConversation(nil, isBot, nil)

		// then
		assert.Nil(t, got)
	})

	t.Run("should return nil when isBot predicate is nil", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, Body: "[high] x", Author: "code-guru[bot]"},
		}

		// when
		got := support.BuildReviewConversation(comments, nil, nil)

		// then
		assert.Nil(t, got, "a nil predicate must short-circuit so the assembler stays defensive")
	})

	t.Run("should root threads on bot top-level inline comments only", func(t *testing.T) {
		t.Parallel()

		// given: a mix of bot threads, user-only threads, and PR-wide
		// comments. Only the bot-rooted inline thread should survive.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "a.go", Body: "[high] nil-check", Author: "code-guru[bot]"},
			{ID: 2, Line: 20, FilePath: "b.go", Body: "what about timeouts?", Author: "alice"},
			{ID: 3, Line: 0, Body: "PR-wide marker", Author: "code-guru[bot]"},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1)
		assert.Equal(t, "a.go", threads[0].FilePath)
		assert.Equal(t, 10, threads[0].Line)
		require.Len(t, threads[0].Comments, 1)
		assert.Equal(t, "code-guru[bot]", threads[0].Comments[0].Author)
	})

	t.Run("should attach replies to their bot thread root in chronological order", func(t *testing.T) {
		t.Parallel()

		// given: bot root with two replies — user follow-up then bot
		// self-reply. IDs are monotonic by creation, so the chronological
		// expectation is root → reply 2 → reply 3.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "a.go", Body: "[high] nil-check", Author: "code-guru[bot]"},
			{ID: 3, Line: 10, FilePath: "a.go", Body: "good point", Author: "code-guru[bot]", InReplyToID: 1},
			{ID: 2, Line: 10, FilePath: "a.go", Body: "we already handle nil above", Author: "alice", InReplyToID: 1},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1)
		require.Len(t, threads[0].Comments, 3)
		assert.Equal(t, "code-guru[bot]", threads[0].Comments[0].Author, "root must be first")
		assert.Equal(t, "alice", threads[0].Comments[1].Author, "user reply (ID 2) must precede bot self-reply (ID 3)")
		assert.Equal(t, "code-guru[bot]", threads[0].Comments[2].Author)
	})

	t.Run("should drop user-only threads (no bot root)", func(t *testing.T) {
		t.Parallel()

		// given: user starts an inline thread; another user replies.
		// No bot involvement → drop entirely.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "a.go", Body: "I think this is wrong", Author: "alice"},
			{ID: 2, Line: 10, FilePath: "a.go", Body: "agreed", Author: "bob", InReplyToID: 1},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		assert.Empty(t, threads, "user-only threads must not pollute the LLM prompt")
	})

	t.Run("should sort threads by file then line for stable rendering", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 50, FilePath: "z.go", Body: "x", Author: "code-guru[bot]"},
			{ID: 2, Line: 10, FilePath: "a.go", Body: "y", Author: "code-guru[bot]"},
			{ID: 3, Line: 20, FilePath: "a.go", Body: "z", Author: "code-guru[bot]"},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 3)
		assert.Equal(t, "a.go", threads[0].FilePath)
		assert.Equal(t, 10, threads[0].Line)
		assert.Equal(t, "a.go", threads[1].FilePath)
		assert.Equal(t, 20, threads[1].Line)
		assert.Equal(t, "z.go", threads[2].FilePath)
	})

	t.Run("should drop threads anchored to files outside liveFiles", func(t *testing.T) {
		t.Parallel()

		// given: two bot-rooted threads on different files. Only one
		// file is in the live set. The other thread must be dropped so
		// the LLM does not see a stale anchor.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "a.go", Body: "[high] live", Author: "code-guru[bot]"},
			{ID: 2, Line: 20, FilePath: "old.go", Body: "[high] stale anchor", Author: "code-guru[bot]"},
		}
		live := map[string]struct{}{"a.go": {}}

		// when
		threads := support.BuildReviewConversation(comments, isBot, live)

		// then
		require.Len(t, threads, 1)
		assert.Equal(t, "a.go", threads[0].FilePath, "thread on file outside live diff must be dropped")
	})

	t.Run("should normalise leading slash when matching liveFiles", func(t *testing.T) {
		t.Parallel()

		// given: ADO returns paths with a leading slash; the diff side
		// strips it. The filter must compare normalised forms so the
		// thread is kept.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "/a.go", Body: "[high] live", Author: "code-guru[bot]"},
		}
		live := map[string]struct{}{"a.go": {}}

		// when
		threads := support.BuildReviewConversation(comments, isBot, live)

		// then
		require.Len(t, threads, 1)
	})

	t.Run("should resolve a multi-hop reply chain back to the bot root", func(t *testing.T) {
		t.Parallel()

		// given: reply-to-reply-to-bot. The walker must climb the
		// InReplyToID chain past intermediate replies to find the root.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 10, FilePath: "a.go", Body: "root", Author: "code-guru[bot]"},
			{ID: 2, Line: 10, FilePath: "a.go", Body: "reply-1", Author: "alice", InReplyToID: 1},
			{ID: 3, Line: 10, FilePath: "a.go", Body: "reply-2", Author: "bob", InReplyToID: 2},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1)
		require.Len(t, threads[0].Comments, 3, "deep replies must still attach to the bot root")
	})

	t.Run("should populate ThreadID and RootCommentID on each thread for the auto-close path", func(t *testing.T) {
		t.Parallel()

		// given: the resolution-aware re-review path needs the
		// gitforge ThreadID on every thread so the post-pipeline can
		// call UpdatePullRequestThreadStatus when the LLM marks a
		// thread as resolved. Without this propagation the auto-close
		// would silently fail with ThreadID=0.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, ThreadID: 111, Line: 10, FilePath: "a.go", Body: "[high] x", Author: "code-guru[bot]"},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1)
		assert.Equal(
			t,
			int64(111),
			threads[0].ThreadID,
			"the gitforge ThreadID must reach the post-pipeline so UpdatePullRequestThreadStatus has a concrete handle to act on",
		)
		assert.Equal(
			t,
			int64(1),
			threads[0].RootCommentID,
			"the root bot comment ID must reach the post-pipeline for future thread-reply / edit-on-second-push features",
		)
	})
}

// TestBuildReviewConversationProviderThreads pins the grouping by
// provider thread. Azure DevOps numbers comments within each thread and
// stores a reply posted into a thread with no parent, which is how the
// bot's own re-review verdicts land; grouped by the bare comment ID and
// the parent pointer, those verdicts each opened a "thread" of their own
// and every re-review answered all of them again.
func TestBuildReviewConversationProviderThreads(t *testing.T) {
	t.Parallel()

	isBot := func(author string) bool { return author == "code-guru@example.com" }

	t.Run("should keep an Azure DevOps thread whole when the bot's replies carry no parent", func(t *testing.T) {
		t.Parallel()

		// given: the bot's finding, the author's answer (parented on the
		// finding, as the Azure DevOps UI posts it) and two verdicts the
		// bot posted on earlier re-reviews, which carry no parent.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, ThreadID: 100, Line: 10, FilePath: "/a.go", Body: "nil-check", Author: "code-guru@example.com"},
			{ID: 2, ThreadID: 100, Line: 10, FilePath: "/a.go", Body: "fixed", Author: "alice", InReplyToID: 1},
			{ID: 3, ThreadID: 100, Line: 10, FilePath: "/a.go", Body: "resolved", Author: "code-guru@example.com"},
			{
				ID:       4,
				ThreadID: 100,
				Line:     10,
				FilePath: "/a.go",
				Body:     "resolved again",
				Author:   "code-guru@example.com",
			},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1, "a parentless bot reply must not open a conversation of its own")
		require.Len(t, threads[0].Comments, 4)
		assert.Equal(t, int64(100), threads[0].ThreadID)
		bodies := []string{}
		fromBot := []bool{}
		for _, message := range threads[0].Comments {
			bodies = append(bodies, message.Body)
			fromBot = append(fromBot, message.FromBot)
		}
		assert.Equal(t, []string{"nil-check", "fixed", "resolved", "resolved again"}, bodies)
		assert.Equal(t, []bool{true, false, true, true}, fromBot, "every message must say whether the bot wrote it")
	})

	t.Run("should keep two Azure DevOps threads apart although each numbers its comments from 1", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, ThreadID: 100, Line: 10, FilePath: "a.go", Body: "first finding", Author: "code-guru@example.com"},
			{ID: 1, ThreadID: 200, Line: 20, FilePath: "b.go", Body: "second finding", Author: "code-guru@example.com"},
			{ID: 2, ThreadID: 200, Line: 20, FilePath: "b.go", Body: "intentional", Author: "alice", InReplyToID: 1},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 2, "two threads that share comment IDs must not overwrite each other")
		assert.Equal(t, "a.go", threads[0].FilePath)
		require.Len(t, threads[0].Comments, 1)
		assert.Equal(t, "b.go", threads[1].FilePath)
		require.Len(t, threads[1].Comments, 2)
		assert.Equal(t, "intentional", threads[1].Comments[1].Body)
	})

	t.Run("should not open a conversation on a bot reply inside a thread a person started", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, ThreadID: 300, Line: 5, FilePath: "c.go", Body: "why is this here?", Author: "alice"},
			{ID: 2, ThreadID: 300, Line: 5, FilePath: "c.go", Body: "an answer", Author: "code-guru@example.com"},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		assert.Empty(t, threads, "a thread a person opened is not one of the bot's conversations")
	})

	t.Run("should keep a GitHub review thread whole, where every reply points at the root", func(t *testing.T) {
		t.Parallel()

		// given: GitHub's shape, where the thread is the root comment's ID.
		comments := []forgeEntities.PullRequestComment{
			{ID: 10, ThreadID: 10, Line: 7, FilePath: "d.go", Body: "finding", Author: "code-guru@example.com"},
			{ID: 11, ThreadID: 10, Line: 7, FilePath: "d.go", Body: "reply", Author: "alice", InReplyToID: 10},
			{
				ID:          12,
				ThreadID:    10,
				Line:        7,
				FilePath:    "d.go",
				Body:        "verdict",
				Author:      "code-guru@example.com",
				InReplyToID: 10,
			},
		}

		// when
		threads := support.BuildReviewConversation(comments, isBot, nil)

		// then
		require.Len(t, threads, 1)
		require.Len(t, threads[0].Comments, 3)
		assert.Equal(t, "verdict", threads[0].Comments[2].Body)
	})
}

func TestIsBotAuthor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		author     string
		identities []string
		want       bool
	}{
		// Built-in `code-guru` shape — recognised with no configuration
		// so GitHub App deployments keep working unchanged.
		{name: "should match GitHub-shaped bot login", author: "code-guru[bot]", want: true},
		{name: "should match Azure DevOps-shaped bot identity", author: "code-guru@example.com", want: true},
		{name: "should be case-insensitive", author: "Code-Guru[bot]", want: true},
		{name: "should match the bare identity (defensive)", author: "code-guru", want: true},
		{name: "should reject a regular user", author: "alice", want: false},
		{name: "should reject a hyphenated user with the prefix", author: "code-guru-fan", want: false},
		{name: "should reject a digit-suffixed user with the prefix", author: "code-guru99", want: false},
		{
			name:   "should reject when the prefix is in the middle of the name",
			author: "alice+code-guru@example.com",
			want:   false,
		},
		{name: "should reject when the prefix is followed by a dot", author: "code-guru.dev", want: false},
		{name: "should reject the empty author", author: "", want: false},
		// Configured identities — a deployment that posts review comments
		// under a service account whose name does not start with
		// `code-guru` (the failure mode that left re-reviews context-free).
		{
			name:       "should match a configured service-account identity exactly",
			author:     "automation@example.com",
			identities: []string{"automation@example.com"},
			want:       true,
		},
		{
			name:       "should match a configured identity case-insensitively",
			author:     "Automation@Example.com",
			identities: []string{"automation@example.com"},
			want:       true,
		},
		{
			name:       "should still match the built-in code-guru shape when identities are configured",
			author:     "code-guru[bot]",
			identities: []string{"automation@example.com"},
			want:       true,
		},
		{
			name:       "should reject an author matching neither the built-in shape nor a configured identity",
			author:     "mallory@example.com",
			identities: []string{"automation@example.com"},
			want:       false,
		},
		{
			name:       "should reject a configured identity matched only as a prefix (exact-match, not prefix)",
			author:     "automation@example.com.evil.test",
			identities: []string{"automation@example.com"},
			want:       false,
		},
		{
			name:       "should ignore empty configured identities",
			author:     "code-guru",
			identities: []string{""},
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given
			matcher := support.IsBotAuthor(tt.identities...)

			// when / then
			assert.Equal(t, tt.want, matcher(tt.author))
		})
	}
}
