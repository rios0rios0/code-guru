//go:build unit

package support_test

import (
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
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
		got := support.BuildReviewConversation(nil, isBot)

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
		got := support.BuildReviewConversation(comments, nil)

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
		threads := support.BuildReviewConversation(comments, isBot)

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
		threads := support.BuildReviewConversation(comments, isBot)

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
		threads := support.BuildReviewConversation(comments, isBot)

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
		threads := support.BuildReviewConversation(comments, isBot)

		// then
		require.Len(t, threads, 3)
		assert.Equal(t, "a.go", threads[0].FilePath)
		assert.Equal(t, 10, threads[0].Line)
		assert.Equal(t, "a.go", threads[1].FilePath)
		assert.Equal(t, 20, threads[1].Line)
		assert.Equal(t, "z.go", threads[2].FilePath)
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
		threads := support.BuildReviewConversation(comments, isBot)

		// then
		require.Len(t, threads, 1)
		require.Len(t, threads[0].Comments, 3, "deep replies must still attach to the bot root")
	})
}

func TestIsBotAuthor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		author string
		want   bool
	}{
		{name: "should match GitHub-shaped bot login", author: "code-guru[bot]", want: true},
		{name: "should match Azure DevOps-shaped bot identity", author: "code-guru@example.com", want: true},
		{name: "should be case-insensitive", author: "Code-Guru[bot]", want: true},
		{name: "should reject a regular user", author: "alice", want: false},
		{name: "should reject a similarly-named user", author: "code-guru-fan", want: true},
	}

	matcher := support.IsBotAuthor()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given / when / then
			assert.Equal(t, tt.want, matcher(tt.author))
		})
	}
}
