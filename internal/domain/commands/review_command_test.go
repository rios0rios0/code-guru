//go:build unit

package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestFilterStaleComments pins the staleness-filter contract that
// keeps the bot from posting comments on files no longer present in
// the latest PR iteration. Captured live on
// `internal-app/internal-integrator#NNNN` where every bot comment
// rendered with ADO's "this file no longer exists in the latest pull
// request changes" warning because the PR had been rewritten between
// the webhook firing (when the diff was fetched) and the review
// completing (when the comments were posted).
func TestFilterStaleComments(t *testing.T) {
	t.Parallel()

	t.Run("should keep all comments when every path is still in the live set", func(t *testing.T) {
		// given
		comments := []entities.ReviewComment{
			{FilePath: "main.go", Line: 10, Body: "fix this"},
			{FilePath: "internal/foo.go", Line: 1, Body: "and this"},
		}
		live := map[string]struct{}{"main.go": {}, "internal/foo.go": {}}

		// when
		kept, dropped := commands.FilterStaleComments(comments, live)

		// then
		assert.Equal(t, comments, kept, "every comment must survive when its file is still in the diff")
		assert.Empty(t, dropped, "no comment should be flagged as stale")
	})

	t.Run("should drop only the comments whose path is no longer in the live set", func(t *testing.T) {
		// given: AI returned 3 findings; only `main.go` survived the
		// follow-up push. The other two reference files the latest
		// iteration no longer touches and would render with the
		// ADO "file no longer exists" banner.
		comments := []entities.ReviewComment{
			{FilePath: "main.go", Line: 10, Body: "still relevant"},
			{FilePath: "removed_in_squash.go", Line: 5, Body: "ignored"},
			{FilePath: "renamed_old.go", Line: 1, Body: "ignored too"},
		}
		live := map[string]struct{}{"main.go": {}}

		// when
		kept, dropped := commands.FilterStaleComments(comments, live)

		// then
		require.Len(t, kept, 1)
		assert.Equal(t, "main.go", kept[0].FilePath)
		require.Len(t, dropped, 2)
		assert.Equal(t, "removed_in_squash.go", dropped[0].FilePath)
		assert.Equal(t, "renamed_old.go", dropped[1].FilePath)
	})

	t.Run("should drop every comment when the live set is empty", func(t *testing.T) {
		// given: a defensive case — empty live set drops everything.
		// In production this happens when `GetPullRequestFiles`
		// returns an empty list (e.g. a force-pushed PR where every
		// file was reverted). The bot must not post a single inline
		// comment in that case.
		comments := []entities.ReviewComment{
			{FilePath: "a.go", Line: 1, Body: "..."},
			{FilePath: "b.go", Line: 2, Body: "..."},
		}

		// when
		kept, dropped := commands.FilterStaleComments(comments, map[string]struct{}{})

		// then
		assert.Empty(t, kept)
		assert.Len(t, dropped, 2)
	})

	t.Run("should normalise leading slash so AI paths match ADO-style paths", func(t *testing.T) {
		// given: ADO's `GetPullRequestFiles` returns paths like
		// `/internal/foo.go` while the AI (driven from the diff) emits
		// `internal/foo.go`. Both halves of the pipeline must compare
		// after stripping the leading `/`, mirroring the rule
		// `support.LookupChunkByPath` already applies on the diff
		// side. Without this row a future "let me clean up the path
		// normalisation" refactor would silently drop every comment
		// on every ADO PR.
		comments := []entities.ReviewComment{
			{FilePath: "internal/foo.go", Line: 1, Body: "..."},
		}
		live := map[string]struct{}{"internal/foo.go": {}} // value already normalised by the caller

		// when
		kept, dropped := commands.FilterStaleComments(comments, live)

		// then
		assert.Len(t, kept, 1, "AI path with no leading slash must match the normalised live entry")
		assert.Empty(t, dropped)
	})

	t.Run("should normalise an AI-supplied leading slash too (defence in depth)", func(t *testing.T) {
		// given: belt-and-suspenders — if the AI ever emits a path
		// with a leading slash (e.g. because the prompt or a future
		// model behaviour change includes one), the filter must not
		// flag it as stale just because the live set's keys are
		// stored without the prefix.
		comments := []entities.ReviewComment{
			{FilePath: "/internal/foo.go", Line: 1, Body: "..."},
		}
		live := map[string]struct{}{"internal/foo.go": {}}

		// when
		kept, dropped := commands.FilterStaleComments(comments, live)

		// then
		assert.Len(t, kept, 1)
		assert.Empty(t, dropped)
	})

	t.Run("should return nil/nil when the input list is empty", func(t *testing.T) {
		// given
		live := map[string]struct{}{"main.go": {}}

		// when
		kept, dropped := commands.FilterStaleComments(nil, live)

		// then
		assert.Empty(t, kept)
		assert.Empty(t, dropped)
	})

	t.Run("should keep PR-wide comments even when their FilePath looks stale", func(t *testing.T) {
		// given: PR-wide comments (`Line <= 0`) are posted via
		// `PostPullRequestComment`, which renders them as repository-
		// wide annotations with no file:line anchor — so they cannot
		// produce ADO's "file no longer exists" warning. Dropping
		// them on staleness grounds would silently delete legitimate
		// summary feedback. Pinned per Copilot review on PR #99
		// thread `PRRT_kwDOJKAEo85-5obu`.
		comments := []entities.ReviewComment{
			{FilePath: "removed.go", Line: 0, Body: "summary annotation, no anchor"},
			{FilePath: "", Line: 0, Body: "no path at all"},
			{FilePath: "removed.go", Line: 5, Body: "inline — must be dropped"},
		}
		live := map[string]struct{}{"main.go": {}}

		// when
		kept, dropped := commands.FilterStaleComments(comments, live)

		// then
		require.Len(t, kept, 2, "PR-wide annotations stay regardless of FilePath")
		assert.Equal(t, "summary annotation, no anchor", kept[0].Body)
		assert.Equal(t, "no path at all", kept[1].Body)
		require.Len(t, dropped, 1, "only the inline reference to a removed file is dropped")
		assert.Equal(t, "inline — must be dropped", dropped[0].Body)
	})
}

func TestSummarizeStaleFilePaths(t *testing.T) {
	t.Parallel()

	t.Run("should join unique paths with a comma when the count is small", func(t *testing.T) {
		// given
		dropped := []entities.ReviewComment{
			{FilePath: "a.go"}, {FilePath: "b.go"}, {FilePath: "a.go"},
		}

		// when
		got := commands.SummarizeStaleFilePaths(dropped)

		// then
		assert.Equal(t, "a.go, b.go", got, "duplicates must be deduplicated to keep the log line tight")
	})

	t.Run("should append `(+N more)` when the unique count exceeds the cap", func(t *testing.T) {
		// given: nine unique paths, cap is eight
		var dropped []entities.ReviewComment
		for _, p := range []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go", "i.go"} {
			dropped = append(dropped, entities.ReviewComment{FilePath: p})
		}

		// when
		got := commands.SummarizeStaleFilePaths(dropped)

		// then
		assert.Contains(t, got, "(+1 more)",
			"the trailing sentinel must reflect how many paths were elided so the operator knows the count")
		assert.Less(t, strings.Count(got, ".go"), 9,
			"the visible portion must not exceed the cap")
	})

	t.Run("should return an empty string for an empty input", func(t *testing.T) {
		// given: a nil/empty `dropped` slice — every other subtest in
		// this file (and across the repo) keeps the BDD `given/when/
		// then` triplet even when the setup is trivial. CLAUDE.md
		// requires all three markers, so leaving `// given` empty is
		// preferable to omitting it entirely.

		// when
		got := commands.SummarizeStaleFilePaths(nil)

		// then
		assert.Empty(t, got)
	})

	t.Run("should deduplicate by normalised path so leading-slash variants are not double-counted", func(t *testing.T) {
		// given: the AI sometimes emits `internal/foo.go` and ADO's
		// underlying paths look like `/internal/foo.go` — both
		// references resolve to the same file, so the operator log
		// must list each file once. Pinned per Copilot review on
		// PR #99 thread `PRRT_kwDOJKAEo85-5obx`.
		dropped := []entities.ReviewComment{
			{FilePath: "internal/foo.go"},
			{FilePath: "/internal/foo.go"},
			{FilePath: "main.go"},
		}

		// when
		got := commands.SummarizeStaleFilePaths(dropped)

		// then
		assert.Equal(t, "internal/foo.go, main.go", got,
			"the normalised pair counts once and the first form encountered is the one printed")
	})
}

func TestNormalizeFilePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "should strip a single leading slash", in: "/internal/foo.go", want: "internal/foo.go"},
		{name: "should leave an unprefixed path alone", in: "internal/foo.go", want: "internal/foo.go"},
		{name: "should not strip a non-leading slash", in: "internal/foo.go/", want: "internal/foo.go/"},
		{name: "should preserve an empty input", in: "", want: ""},
		{
			name: "should only strip ONE leading slash (so a network-style `//host/path` does not collapse)",
			// The bot will never see this in real ADO traffic, but
			// pinning the contract prevents a future "be more
			// aggressive" refactor from silently turning `//`
			// into `/`.
			in:   "//double-leading.go",
			want: "/double-leading.go",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// when
			got := commands.NormalizeFilePathForTest(tc.in)

			// then
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildReviewingMarkerBody(t *testing.T) {
	t.Parallel()

	t.Run("should render the marker with the start timestamp in RFC 3339 UTC", func(t *testing.T) {
		// given: a fixed timestamp pinned in UTC so the formatting
		// contract is deterministic. RFC 3339 matches the shape used
		// in the operator log ("Started at <ts>"), so a reader can
		// correlate the PR thread with the pod log line.
		ts := time.Date(2026, 5, 1, 1, 52, 21, 0, time.UTC)

		// when
		body := commands.BuildReviewingMarkerBody(ts)

		// then
		assert.Contains(t, body, "Code Guru is reviewing this PR.",
			"the marker headline must explicitly tell the author the bot has the PR")
		assert.Contains(t, body, "Started at 2026-05-01T01:52:21Z.",
			"the timestamp must be RFC 3339 UTC so it matches the operator log shape")
		assert.Contains(t, body, "Comments will be posted as inline threads",
			"the body must set expectations on what the eventual review looks like")
	})

	t.Run("should always emit a non-empty body even at the zero time (defensive)", func(t *testing.T) {
		// given: the zero `time.Time` shouldn't crash or produce an
		// empty body — pin the contract so a future refactor that
		// (e.g.) reads the year off the timestamp doesn't panic on
		// an uninitialised value.

		// when
		body := commands.BuildReviewingMarkerBody(time.Time{})

		// then
		assert.NotEmpty(t, body)
		assert.Contains(t, body, "Code Guru is reviewing this PR.")
	})

	t.Run("should not embed `\\n` literally (must use real newlines for Markdown rendering)", func(t *testing.T) {
		// given: ADO and GitHub render the marker as Markdown — the
		// blank line between the headline and the explanatory
		// paragraph requires an actual `\n\n` so the renderer treats
		// them as separate paragraphs. A literal "\n" string would
		// render as the four characters `\`, `n`, `\`, `n` and the
		// PR thread would look like a single squashed line.
		ts := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)

		// when
		body := commands.BuildReviewingMarkerBody(ts)

		// then
		assert.NotContains(t, body, `\n`, "the body must contain real newlines, not the escape sequence")
		assert.Contains(t, body, "\n\n", "the body must have at least one blank line for Markdown paragraph breaks")
	})
}
