//go:build unit

package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestHasCompletedReviewMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bodies []string
		want   bool
	}{
		{
			name: "should match the review-complete marker body",
			bodies: []string{
				"✅ **Code Guru review complete.**\n\nVerdict: `approve` · 3 inline comments.\n",
			},
			want: true,
		},
		{
			name: "should match the review-failed marker body",
			bodies: []string{
				"⚠️ **Code Guru review failed.**\n\nThe AI step crashed.\n",
			},
			want: true,
		},
		{
			name: "should NOT match the in-flight reviewing marker (different prefix)",
			bodies: []string{
				"\U0001f916 **Code Guru is reviewing this PR.**\n\nPlease wait...",
			},
			// `**Code Guru is reviewing` does not contain the
			// `**Code Guru review` literal we look for (the space after
			// `Code Guru` is followed by `is`, not `review`). This is
			// intentional — the in-flight marker should NOT count as
			// "this PR has been reviewed".
			want: false,
		},
		{
			name:   "should return false on empty body list",
			bodies: nil,
			want:   false,
		},
		{
			name: "should return false when no body contains the marker",
			bodies: []string{
				"PR-wide comment from a user",
				"Looks good to me",
				"@code-guru please re-review",
			},
			want: false,
		},
		{
			name: "should match when the marker is one of several bodies",
			bodies: []string{
				"a user comment",
				"✅ **Code Guru review complete.** ...",
				"another user comment",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given / when
			got := support.HasCompletedReviewMarker(tt.bodies)

			// then
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHasMention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "should match a plain @code-guru mention",
			body: "@code-guru please re-review the auth changes",
			want: true,
		},
		{
			name: "should match a case-insensitive mention",
			body: "Hey @Code-Guru, what about the timeouts?",
			want: true,
		},
		{
			name: "should match when followed by punctuation",
			body: "thanks @code-guru!",
			want: true,
		},
		{
			name: "should match when at end of string",
			body: "ping @code-guru",
			want: true,
		},
		{
			name: "should NOT match when extended into a longer identifier",
			body: "@code-guru-staging please confirm",
			want: false,
		},
		{
			name: "should NOT match @code-guru99 (digit continuation)",
			body: "@code-guru99 please re-review",
			want: false,
		},
		{
			name: "should keep scanning past a non-match and find a real mention",
			body: "@code-guru-staging deployed; @code-guru please re-review",
			want: true,
		},
		{
			name: "should return false when token absent",
			body: "@someone-else please re-review",
			want: false,
		},
		{
			name: "should return false on empty body",
			body: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given / when
			got := support.HasMention(tt.body)

			// then
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectBotAuthors(t *testing.T) {
	t.Parallel()

	t.Run("should detect the bot from the author of a PR-wide review-complete annotation", func(t *testing.T) {
		t.Parallel()

		// given: a self-hosted deployment posts under a service account
		// whose name does not start with `code-guru`. Its PR-wide status
		// annotation carries the bot marker.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 0, Author: "automation@example.com", Body: "✅ **Code Guru review complete.**\n\nVerdict: `approve`."},
			{ID: 2, Line: 10, FilePath: "internal/foo.go", Author: "automation@example.com", Body: "[high] nil-check this"},
			{ID: 3, Line: 10, FilePath: "internal/foo.go", Author: "alice", Body: "already handled", InReplyToID: 2},
		}

		// when
		got := support.DetectBotAuthors(comments)

		// then
		assert.Equal(t, []string{"automation@example.com"}, got)
	})

	t.Run("should detect the bot from the reviewing / failed annotations too", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 0, Author: "svc-bot", Body: "\U0001f916 **Code Guru is reviewing this PR.**"},
			{ID: 2, Line: 0, Author: "svc-bot", Body: "⚠️ **Code Guru review failed.**"},
		}

		// when
		got := support.DetectBotAuthors(comments)

		// then: both annotations are by the same account; collapsed once.
		assert.Equal(t, []string{"svc-bot"}, got)
	})

	t.Run("should ignore inline comments that merely quote the marker", func(t *testing.T) {
		t.Parallel()

		// given: a human pastes the bot's annotation text into an inline
		// comment. Only PR-wide (Line <= 0) annotations identify the bot.
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 42, FilePath: "main.go", Author: "mallory", Body: "look: **Code Guru review complete.**"},
		}

		// when
		got := support.DetectBotAuthors(comments)

		// then
		assert.Empty(t, got)
	})

	t.Run("should ignore ordinary human PR-wide comments without the marker", func(t *testing.T) {
		t.Parallel()

		// given
		comments := []forgeEntities.PullRequestComment{
			{ID: 1, Line: 0, Author: "alice", Body: "@code-guru review it again"},
		}

		// when
		got := support.DetectBotAuthors(comments)

		// then
		assert.Empty(t, got)
	})

	t.Run("should return nil for no comments", func(t *testing.T) {
		t.Parallel()

		// given / when
		got := support.DetectBotAuthors(nil)

		// then
		assert.Nil(t, got)
	})
}
