//go:build unit

package commands_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

func TestShouldPostSummary(t *testing.T) {
	t.Parallel()

	t.Run("should suppress summary when inline comments are present", func(t *testing.T) {
		// given: the user's complaint on `internal/warden-service#NNNN` was that
		// every push produced a duplicate PR-wide summary thread on top of
		// the per-file inline threads. Skipping the summary in this case is
		// the entire point of the gate.
		result := &entities.ReviewResult{
			Summary: "Found a few issues",
			Comments: []entities.ReviewComment{
				{FilePath: "main.go", Line: 10, Severity: "warning", Body: "..."},
			},
		}

		// when
		ok := commands.ShouldPostSummary(result)

		// then
		assert.False(t, ok, "summary must not be re-posted when inline comments already cover the issues")
	})

	t.Run("should post summary when there are no inline comments", func(t *testing.T) {
		// given: clean reviews (`verdict=approve`, "no issues found") still
		// need a visible signal that the bot ran — otherwise the operator
		// has no easy way to tell whether the webhook fired at all.
		result := &entities.ReviewResult{
			Verdict:  "approve",
			Summary:  "No issues found.",
			Comments: nil,
		}

		// when
		ok := commands.ShouldPostSummary(result)

		// then
		assert.True(t, ok, "summary must still be posted when the review has no inline comments")
	})

	t.Run("should suppress summary when both summary and comments are empty", func(t *testing.T) {
		// given: a degenerate empty result — neither summary nor comments —
		// must produce no PR thread at all. Posting an empty summary would
		// leave a blank thread on the PR.
		result := &entities.ReviewResult{Summary: "", Comments: nil}

		// when
		ok := commands.ShouldPostSummary(result)

		// then
		assert.False(t, ok, "summary must not be posted when the summary string is empty")
	})

	t.Run("should treat a whitespace-only summary as non-empty (current contract)", func(t *testing.T) {
		// given: the predicate's emptiness check is `Summary != ""`, so a
		// whitespace-only summary is considered non-empty and posted when
		// `Comments` is empty. Pin this behaviour so a future change to
		// trim or treat whitespace as empty is deliberate and arrives with
		// an explicit test update.
		result := &entities.ReviewResult{Summary: "   ", Comments: nil}

		// when
		ok := commands.ShouldPostSummary(result)

		// then
		assert.True(t, ok, "whitespace-only Summary is treated as non-empty and posted when Comments is empty")
	})
}
