//go:build unit

package commands_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
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
		// contract is deterministic. RFC 3339 matches the shape on
		// the corresponding `Info` log line emitted by
		// `postReviewingMarker` (`started_at=<ts>`), so a reader can
		// correlate the timestamp shown in the PR thread with the
		// log entry produced for the same review.
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

	t.Run("should normalise a non-UTC input to UTC so the printed timestamp ends in Z", func(t *testing.T) {
		// given: a caller passing in a localised `time.Time` (e.g. an
		// `America/Sao_Paulo` clock that mistakenly skipped the
		// `.UTC()` step). The helper must enforce its own contract
		// rather than echoing the caller's location into the rendered
		// body — pinned per Copilot review on PR #102 thread
		// `PRRT_kwDOJKAEo85-56Sq`. Without the defensive `.UTC()`
		// inside `buildReviewingMarkerBody`, the body would render
		// `Started at 2026-04-30T22:52:21-03:00.` and the documented
		// "RFC 3339 in UTC" contract would silently break. Use
		// `time.FixedZone` rather than `time.LoadLocation`: the
		// latter reads `tzdata` at runtime and silently fails with
		// `nil` on hermetic systems (Alpine, distroless, scratch),
		// which would then panic in `time.Date`.
		spLoc := time.FixedZone("America/Sao_Paulo", -3*60*60)
		ts := time.Date(2026, 4, 30, 22, 52, 21, 0, spLoc) // == 2026-05-01T01:52:21Z

		// when
		body := commands.BuildReviewingMarkerBody(ts)

		// then
		assert.Contains(t, body, "Started at 2026-05-01T01:52:21Z.",
			"the helper must format in UTC regardless of the input Location")
		assert.NotContains(t, body, "-03:00", "no timezone offset should leak into the body")
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

func TestBuildReviewFailedBody(t *testing.T) {
	t.Parallel()

	t.Run("should render the failure notice with the timestamp and the underlying error", func(t *testing.T) {
		// given: a fixed timestamp + a representative claude failure
		// (the canonical signature observed across PRs #NNNN /
		// #NNNN / #NNNN / #NNNN / #NNNN on `2026-05-01`). The
		// body must surface BOTH the timestamp (so the operator can
		// correlate with the pod log line) AND the error text (so
		// the PR author knows whether to retry or look at logs).
		ts := time.Date(2026, 5, 1, 2, 51, 21, 0, time.UTC)
		err := errors.New("AI review failed: claude CLI failed: exit status 1 (stderr: ; stdout: )")

		// when
		body := commands.BuildReviewFailedBody(ts, err)

		// then
		assert.Contains(t, body, "Code Guru review failed.",
			"the headline must be unambiguous so the author does not confuse it with the marker")
		assert.Contains(t, body, "Failed at 2026-05-01T02:51:21Z.",
			"the timestamp must match the operator-log RFC 3339 UTC shape")
		assert.Contains(t, body, "claude CLI failed",
			"the error text must surface so the author knows whether this is transient or systemic")
		assert.Contains(t, body, "review this PR manually",
			"the body must tell the author what to do next")
	})

	t.Run("should normalise a non-UTC input to UTC so the printed timestamp ends in Z", func(t *testing.T) {
		// given: defensive — same contract as `buildReviewingMarkerBody`
		// (Copilot review on PR #102 thread `PRRT_kwDOJKAEo85-56Sq`).
		// A future caller passing a non-UTC time must still produce
		// a UTC-formatted body. Use `time.FixedZone` rather than
		// `time.LoadLocation`: the latter reads `tzdata` at runtime
		// and silently fails with `nil` on hermetic systems
		// (Alpine, distroless, scratch), which would then panic in
		// `time.Date` — pinned per Copilot review on PR #103 thread
		// `PRRT_kwDOJKAEo85-6Cu4`.
		spLoc := time.FixedZone("America/Sao_Paulo", -3*60*60)
		ts := time.Date(2026, 4, 30, 23, 51, 21, 0, spLoc) // == 2026-05-01T02:51:21Z
		err := errors.New("transient")

		// when
		body := commands.BuildReviewFailedBody(ts, err)

		// then
		assert.Contains(t, body, "Failed at 2026-05-01T02:51:21Z.",
			"the helper must format in UTC regardless of the input Location")
		assert.NotContains(t, body, "-03:00", "no timezone offset should leak into the body")
	})

	t.Run("should fall back to a placeholder when the error is nil (defensive)", func(t *testing.T) {
		// given: callers should always pass a non-nil error, but
		// belt-and-suspenders — a future caller mistakenly passing
		// nil must not produce a body containing the literal `<nil>`.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

		// when
		body := commands.BuildReviewFailedBody(ts, nil)

		// then
		assert.Contains(t, body, "(no error details)",
			"a nil error must surface a readable placeholder, never `<nil>`")
		assert.NotContains(t, body, "<nil>")
	})

	t.Run("should truncate an oversized error so the PR thread stays bounded", func(t *testing.T) {
		// given: a runaway claude that emits a 10 KB error envelope
		// (e.g. multi-megabyte stdout truncated by PR #98's
		// `support.TruncateBytesForLog` to its own cap, then echoed
		// here a second time). The 2 KB cap on this side keeps the
		// PR thread from turning into a wall of text.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		oversized := errors.New(strings.Repeat("X", 10*1024))

		// when
		body := commands.BuildReviewFailedBody(ts, oversized)

		// then
		// 2 KB error + ~500 bytes envelope/markdown + sentinel
		// fits comfortably below 4 KB.
		assert.Less(t, len(body), 4*1024,
			"the rendered body must respect the 2 KB error cap so a runaway claude cannot flood the PR thread")
		assert.Contains(t, body, "...[truncated]",
			"the truncation sentinel from support.TruncateForLog must be present so a reader knows the error was clipped")
	})
}

func TestBuildReviewCompleteBody(t *testing.T) {
	t.Parallel()

	t.Run("should render the completion notice with verdict, comment count and timestamp", func(t *testing.T) {
		// given: a typical AI review result — `request_changes`
		// verdict with 3 inline comments. The body must surface
		// each so the author can see the conclusion at a glance
		// without scrolling.
		ts := time.Date(2026, 5, 1, 2, 51, 21, 0, time.UTC)
		result := &entities.ReviewResult{
			Verdict: "request_changes",
			Comments: []entities.ReviewComment{
				{FilePath: "a.go", Line: 1, Body: "..."},
				{FilePath: "b.go", Line: 2, Body: "..."},
				{FilePath: "c.go", Line: 3, Body: "..."},
			},
		}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "Code Guru review complete.")
		assert.Contains(t, body, "request_changes",
			"the verdict must surface so the author knows the bot's conclusion")
		assert.Contains(t, body, "3 inline comments",
			"the comment count must surface so the author can locate the threads")
		assert.Contains(t, body, "Completed at 2026-05-01T02:51:21Z.",
			"the timestamp must be RFC 3339 UTC matching the marker's `Started at <ts>` shape so a reader can pair them")
	})

	t.Run("should pluralise the comment label correctly for exactly 1 inline comment", func(t *testing.T) {
		// given: pluralisation is the kind of thing that's quiet
		// until a reader notices "1 inline comments" looks broken.
		// Pin "1 inline comment" / "0 inline comments" /
		// "2 inline comments" so a future formatting refactor
		// preserves grammar.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{
			Verdict:  "comment",
			Comments: []entities.ReviewComment{{FilePath: "x.go", Line: 1, Body: "."}},
		}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "1 inline comment.",
			"singular comment count must use the singular noun")
		assert.NotContains(t, body, "1 inline comments")
	})

	t.Run("should render `0 inline comments` when the AI returned no findings", func(t *testing.T) {
		// given: a clean review — `verdict=approve` and zero
		// inline findings. The completion notice must still post
		// (it's the bot's "done" signal) and must read naturally.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{Verdict: "approve", Comments: nil}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "approve")
		assert.Contains(t, body, "0 inline comments.",
			"plural form covers zero too — `0 inline comment` would read as broken grammar")
	})

	t.Run("should fall back to `comment` verdict when the result's verdict is empty", func(t *testing.T) {
		// given: the AI parser sometimes yields an empty Verdict
		// (e.g. malformed JSON repaired but missing the field).
		// The completion notice must still render something
		// meaningful rather than `Verdict: \`\`` which looks broken.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{Verdict: "", Comments: nil}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "comment",
			"empty verdict must fall back to the `comment` literal")
		assert.NotContains(t, body, "``", "no empty backtick block must leak into the body")
	})

	t.Run("should normalise a non-UTC input to UTC so the printed timestamp ends in Z", func(t *testing.T) {
		// given: defensive — same contract as the other body
		// builders (per Copilot review on PR #102 thread
		// `PRRT_kwDOJKAEo85-56Sq`). Use `time.FixedZone` rather
		// than `time.LoadLocation` for hermeticity (matching the
		// pattern fixed across the rest of this file in PR #103
		// thread `PRRT_kwDOJKAEo85-6Cu4`).
		spLoc := time.FixedZone("America/Sao_Paulo", -3*60*60)
		ts := time.Date(2026, 4, 30, 23, 51, 21, 0, spLoc) // == 2026-05-01T02:51:21Z
		result := &entities.ReviewResult{Verdict: "comment", Comments: nil}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "Completed at 2026-05-01T02:51:21Z.",
			"the helper must format in UTC regardless of the input Location")
		assert.NotContains(t, body, "-03:00", "no timezone offset should leak into the body")
	})

	t.Run("should not panic and produce a usable body when the result is nil (defensive)", func(t *testing.T) {
		// given: the production caller never passes nil, but a
		// future refactor that, e.g., skips the AI call for trivial
		// detection and still wires the completion notice could
		// pass `nil`. The helper must degrade gracefully rather
		// than panic on a nil dereference.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

		// when
		body := commands.BuildReviewCompleteBody(ts, nil)

		// then
		assert.NotEmpty(t, body)
		assert.Contains(t, body, "Code Guru review complete.")
	})

	t.Run("should count only inline (Line > 0) comments — PR-wide annotations don't inflate the count", func(t *testing.T) {
		// given: a review with 2 inline (`Line > 0`) findings and
		// 3 PR-wide annotations (`Line <= 0`). The body says
		// "X inline comments", so only the inline ones count
		// against that label — pinned per Copilot review on PR #104
		// thread `PRRT_kwDOJKAEo85-6ErC`.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{
			Verdict: "request_changes",
			Comments: []entities.ReviewComment{
				{FilePath: "a.go", Line: 1, Body: "inline 1"},
				{FilePath: "b.go", Line: 5, Body: "inline 2"},
				{FilePath: "c.go", Line: 0, Body: "PR-wide"},
				{FilePath: "", Line: 0, Body: "PR-wide annotation"},
				{FilePath: "d.go", Line: -1, Body: "negative-line PR-wide"},
			},
		}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "2 inline comments.",
			"only the two `Line > 0` comments must show up against the `inline` label")
		assert.NotContains(t, body, "5 inline comments",
			"the three PR-wide annotations must NOT inflate the count")
	})

	t.Run("should include result.Summary as a separate paragraph when non-empty (trivial fast path)", func(t *testing.T) {
		// given: a trivial-detector-shaped result. Every detector emits
		// a Summary like the one below; when the trivial path posts its
		// completion annotation, that rationale is the ONLY place the
		// PR author sees why the bot reached the verdict.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{
			Verdict: "approve",
			Summary: "Documentation-only change detected (2 markdown files). Auto-approved by trivial PR policy.",
		}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then
		assert.Contains(t, body, "**Code Guru review",
			"the F2 review-once-gate marker must remain in the annotation body")
		assert.Contains(t, body, result.Summary,
			"the trivial detector's Summary must surface in the annotation body — without it the PR author only sees the verdict label and loses the rationale (especially important for `reject` verdicts like the bump-detector's missing-files message)")
	})

	t.Run("should preserve the legacy two-paragraph layout when result.Summary is empty (LLM path)", func(t *testing.T) {
		// given: the LLM path typically leaves Summary empty because
		// the rationale lands as inline comments. The completion
		// annotation has shipped with two paragraphs ("review complete"
		// + "Verdict: ... " followed by the timestamp) and we must not
		// regress that layout while wiring the trivial-summary section.
		ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		result := &entities.ReviewResult{Verdict: "comment"}

		// when
		body := commands.BuildReviewCompleteBody(ts, result)

		// then: between "comments." and "_Completed" there must be
		// exactly one blank line — same as before this PR landed.
		expected := "comments.\n\n_Completed at 2026-05-01T00:00:00Z._"
		assert.Contains(t, body, expected,
			"empty Summary must keep the original layout: a single blank line between the verdict line and the timestamp")
	})
}

// stubPRStatusGetter is a 1-method test double satisfying
// `commands.PullRequestStatusGetter`. Records the call and returns a
// canned status / error.
type stubPRStatusGetter struct {
	status string
	err    error
	calls  int
}

func (s *stubPRStatusGetter) GetPullRequestStatus(_ context.Context, _ forgeEntities.Repository, _ int) (string, error) {
	s.calls++
	return s.status, s.err
}

func TestIsPullRequestClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status string
		err    error
		want   bool
	}{
		{name: "should return true for ADO `completed`", status: "completed", want: true},
		{name: "should return true for ADO `abandoned`", status: "abandoned", want: true},
		{name: "should return true for GitHub `closed`", status: "closed", want: true},
		{name: "should return true for GitHub `merged`", status: "merged", want: true},
		{name: "should normalise mixed case (`Completed`)", status: "Completed", want: true},
		{name: "should normalise upper case (`ABANDONED`)", status: "ABANDONED", want: true},
		{name: "should normalise leading/trailing whitespace (` merged `)", status: " merged ", want: true},
		{name: "should return false for ADO `active`", status: "active", want: false},
		{name: "should return false for GitHub `open`", status: "open", want: false},
		{name: "should return false for an empty status (defensive — webhook payload sometimes ships empty)", status: "", want: false},
		{name: "should return false for an unknown future enum value (`merging`) — defer to the worker", status: "merging", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			getter := &stubPRStatusGetter{status: tc.status, err: tc.err}
			repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}

			// when
			got := commands.IsPullRequestClosed(context.Background(), getter, repo, 12)

			// then
			assert.Equal(t, tc.want, got)
			assert.Equal(t, 1, getter.calls, "the getter must be called exactly once per check")
		})
	}

	t.Run("should return false (proceed with post) when GetPullRequestStatus errors — best-effort contract", func(t *testing.T) {
		// given: a transient ADO outage. The bot must NOT silently
		// drop the review comments because the status check failed —
		// posting on a closed PR is harmless (verified live on PR
		// #NNNN), but skipping a legitimate post would be a
		// regression. Pinned per task #43.
		t.Parallel()
		getter := &stubPRStatusGetter{err: errors.New("ADO 503 Service Unavailable")}
		repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}

		// when
		got := commands.IsPullRequestClosed(context.Background(), getter, repo, 12)

		// then
		assert.False(t, got, "a fetch failure must default to `not closed` so the caller proceeds with posting")
		assert.Equal(t, 1, getter.calls)
	})
}

// recordingReviewProvider satisfies `forgeEntities.ReviewProvider` by
// embedding the interface (zero value is nil) and overriding only
// `PostPullRequestComment`. Any other method call would panic, which
// is fine because the marker post helpers never touch the rest of
// the surface — and a future refactor that DOES introduce another
// call would surface immediately as a panic during this test rather
// than as a silent stub.
type recordingReviewProvider struct {
	forgeEntities.ReviewProvider
	calls                  []recordedPRComment
	submissions            []forgeEntities.ReviewSubmission
	submitErr              error
	getPullRequestFilesErr error
	// files seeds the response of `GetPullRequestFiles`. When nil and
	// no error is set, the method returns an empty slice — same as
	// before this field was added.
	files []forgeEntities.PullRequestFile
	// existingComments seeds the response of `ListPullRequestComments`
	// so tests can simulate an already-reviewed PR (review-once gate)
	// or pre-existing inline comments (dedup pass) without standing up
	// a full provider. Nil means "no comments on the PR yet".
	existingComments []forgeEntities.PullRequestComment
	// listCommentsErr, when set, makes `ListPullRequestComments` return
	// the error instead of `existingComments`. Used by the conversation
	// walk's soft-fail-on-list-error test path.
	listCommentsErr error
	// merges records every call to `MergePullRequest` so tests can
	// assert auto-merge behaviour on the trivial fast path.
	merges    []recordedMerge
	mergeErr  error
}

type recordedMerge struct {
	prID     int
	strategy string
}

type recordedPRComment struct {
	body string
	opts []forgeEntities.CommentOption
}

func (r *recordingReviewProvider) PostPullRequestComment(
	_ context.Context,
	_ forgeEntities.Repository,
	_ int,
	body string,
	opts ...forgeEntities.CommentOption,
) error {
	r.calls = append(r.calls, recordedPRComment{body: body, opts: opts})
	return nil
}

func (r *recordingReviewProvider) SubmitPullRequestReview(
	_ context.Context,
	_ forgeEntities.Repository,
	_ int,
	sub forgeEntities.ReviewSubmission,
) error {
	r.submissions = append(r.submissions, sub)
	return r.submitErr
}

func (r *recordingReviewProvider) GetPullRequestFiles(
	_ context.Context,
	_ forgeEntities.Repository,
	_ int,
) ([]forgeEntities.PullRequestFile, error) {
	if r.getPullRequestFilesErr != nil {
		return nil, r.getPullRequestFilesErr
	}
	return r.files, nil
}

func (r *recordingReviewProvider) ListPullRequestComments(
	_ context.Context,
	_ forgeEntities.Repository,
	_ int,
) ([]forgeEntities.PullRequestComment, error) {
	if r.listCommentsErr != nil {
		return nil, r.listCommentsErr
	}
	return r.existingComments, nil
}

func (r *recordingReviewProvider) MergePullRequest(
	_ context.Context,
	_ forgeEntities.Repository,
	prID int,
	strategy string,
) error {
	r.merges = append(r.merges, recordedMerge{prID: prID, strategy: strategy})
	return r.mergeErr
}

func TestAnnotationThreadStatusContract(t *testing.T) {
	t.Parallel()

	// The constant pins the ADO thread-status string the bot sends.
	// `"closed"` is what ADO renders as "discussion ended" — the
	// right shape for purely informational threads (start marker,
	// review-complete notice, review-failed notice). Without this
	// pin a future "let me try `fixed` instead" refactor would land
	// without a test failure even though it would change how the PR
	// author perceives every annotation we post.
	t.Run("should be 'closed' so ADO renders annotations as ended discussions", func(t *testing.T) {
		t.Parallel()

		// given / when / then
		assert.Equal(t, "closed", commands.AnnotationThreadStatus,
			"the constant must remain 'closed' — see the doc on annotationThreadStatus for why")
	})
}

func TestMarkerHelpersForwardThreadStatusOption(t *testing.T) {
	t.Parallel()

	// Pin the wiring contract: each of the three PR-wide annotation
	// helpers MUST forward `forgeEntities.WithThreadStatus(commands.AnnotationThreadStatus)`
	// on every call to `PostPullRequestComment`. Without this, ADO
	// renders every marker / completion / failure notice as an
	// active thread the PR author has to dismiss by hand — the
	// failure mode operationally observed before gitforge PR #87
	// landed.
	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	prID := 4242

	tests := []struct {
		name   string
		invoke func(rc *commands.ReviewCommand, p *recordingReviewProvider)
	}{
		{
			name: "should forward WithThreadStatus(closed) from postReviewingMarker",
			invoke: func(rc *commands.ReviewCommand, p *recordingReviewProvider) {
				commands.PostReviewingMarker(rc, context.Background(), p, repo, prID)
			},
		},
		{
			name: "should forward WithThreadStatus(closed) from postReviewCompleteAnnotation",
			invoke: func(rc *commands.ReviewCommand, p *recordingReviewProvider) {
				result := &entities.ReviewResult{Verdict: "comment", Summary: "ok"}
				commands.PostReviewCompleteAnnotation(rc, context.Background(), p, repo, prID, result)
			},
		},
		{
			name: "should forward WithThreadStatus(closed) from postReviewFailedAnnotation",
			invoke: func(rc *commands.ReviewCommand, p *recordingReviewProvider) {
				commands.PostReviewFailedAnnotation(rc, context.Background(), p, repo, prID, errors.New("claude crashed"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given: a ReviewCommand built with nil dependencies —
			// the helpers never touch them, and a constructor change
			// that introduces a new dependency would surface as a
			// nil-pointer panic here, not as a silent regression.
			rc := commands.NewReviewCommand(nil, nil, nil)
			provider := &recordingReviewProvider{}

			// when
			tc.invoke(rc, provider)

			// then
			require.Len(t, provider.calls, 1, "each helper must call PostPullRequestComment exactly once")
			require.NotEmpty(t, provider.calls[0].opts, "each helper must forward at least one CommentOption")

			// Resolve the forwarded options through gitforge's helper
			// to confirm the encoded status value is `closed` — this
			// keeps the test focused on the contract that matters even
			// if additional CommentOptions are forwarded in the future.
			resolved := forgeEntities.ResolveCommentOptions(provider.calls[0].opts...)
			assert.Equal(t, commands.AnnotationThreadStatus, resolved,
				"the forwarded options must resolve to the AnnotationThreadStatus constant ('closed')")
		})
	}
}

func TestSubmitNativeReviewFlagGate(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	prID := 4242

	t.Run("should not call provider when SubmitNativeReview is false", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}

		// when
		commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
			"approve", "all good", commands.ReviewOptions{SubmitNativeReview: false})

		// then
		assert.Empty(t, provider.submissions)
	})

	t.Run("should map approve verdict to ReviewVerdictApprove when flag is on", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}

		// when
		commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
			"approve", "looks good", commands.ReviewOptions{SubmitNativeReview: true})

		// then
		require.Len(t, provider.submissions, 1)
		assert.Equal(t, forgeEntities.ReviewVerdictApprove, provider.submissions[0].Verdict)
		assert.Equal(t, "looks good", provider.submissions[0].Body)
	})

	t.Run("should map reject verdict to ReviewVerdictRequestChanges when flag is on", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}

		// when
		commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
			"reject", "blocking", commands.ReviewOptions{SubmitNativeReview: true})

		// then
		require.Len(t, provider.submissions, 1)
		assert.Equal(t, forgeEntities.ReviewVerdictRequestChanges, provider.submissions[0].Verdict)
	})

	t.Run("should map comment verdict to WaitingForAuthor so ADO surfaces vote=-5 and GitHub posts a COMMENT review", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}

		// when
		commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
			"comment", "FYI", commands.ReviewOptions{SubmitNativeReview: true})

		// then
		require.Len(t, provider.submissions, 1)
		assert.Equal(t, forgeEntities.ReviewVerdictWaitingForAuthor, provider.submissions[0].Verdict)
		assert.Equal(t, "FYI", provider.submissions[0].Body)
	})

	t.Run("should map LLM-vocabulary request_changes verdict to RequestChanges (parser emits this, not 'reject')", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}

		// when
		commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
			"request_changes", "needs work", commands.ReviewOptions{SubmitNativeReview: true})

		// then: this is the verdict shape that was silently skipped before the
		// mapper learned the LLM vocabulary — see the dev pod logs from
		// 2026-05-01T21:13Z where verdict=request_changes never produced a
		// "native review submission failed" warning AND never produced a vote.
		require.Len(t, provider.submissions, 1)
		assert.Equal(t, forgeEntities.ReviewVerdictRequestChanges, provider.submissions[0].Verdict)
		assert.Equal(t, "needs work", provider.submissions[0].Body)
	})

	t.Run("should swallow provider errors so the worker keeps going", func(t *testing.T) {
		t.Parallel()

		// given: the gitforge error path is documented as best-effort UX,
		// so a transient permission failure must not bubble up to Execute.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{submitErr: errors.New("permission denied")}

		// when / then: no panic, no return — the helper is fire-and-forget
		require.NotPanics(t, func() {
			commands.SubmitNativeReview(rc, context.Background(), provider, repo, prID,
				"approve", "lgtm", commands.ReviewOptions{SubmitNativeReview: true})
		})
		require.Len(t, provider.submissions, 1, "the helper still attempted the call before logging the error")
	})
}

func TestExecuteSkipsDraftsByDefault(t *testing.T) {
	t.Parallel()

	t.Run("should short-circuit with a 'skipped: draft' result when ReviewDrafts is false", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{}
		repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
		pr := forgeEntities.PullRequestDetail{
			PullRequest: forgeEntities.PullRequest{ID: 4242, Title: "wip", URL: "https://example/pr/4242"},
			IsDraft:     true,
		}

		// when
		result, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "comment", result.Verdict)
		assert.Contains(t, result.Summary, "draft")
		assert.Empty(t, provider.calls, "no marker / annotation should fire on a skipped draft")
		assert.Empty(t, provider.submissions, "no native review should fire on a skipped draft")
	})

	t.Run("should NOT skip when ReviewDrafts opt-in is set", func(t *testing.T) {
		t.Parallel()

		// given: a deterministic provider that surfaces a known error from
		// GetPullRequestFiles — Execute must reach that call (proving the
		// draft branch was bypassed) and return the wrapped error so the
		// test asserts the bypass without relying on panic behaviour.
		expectedErr := errors.New("get pull request files failed")
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			getPullRequestFilesErr: expectedErr,
		}
		repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
		pr := forgeEntities.PullRequestDetail{
			PullRequest: forgeEntities.PullRequest{ID: 4242, Title: "wip", URL: "https://example/pr/4242"},
			IsDraft:     true,
		}

		// when
		result, err := rc.Execute(
			context.Background(), provider, repo, pr,
			commands.ReviewOptions{ReviewDrafts: true},
		)

		// then
		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr,
			"with ReviewDrafts=true the draft branch must be bypassed; Execute must surface the deterministic provider error from GetPullRequestFiles")
		assert.Nil(t, result)
		assert.Empty(t, provider.submissions, "the command should stop on the deterministic provider error instead of skipping the draft")
	})
}

func TestDropDuplicateComments(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	const prID = 4242

	t.Run("should drop a comment that matches an existing bot comment by file+line+body-prefix", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{FilePath: "internal/foo.go", Line: 42, Body: "[high] this could be nil-checked"},
			},
		}
		newComments := []entities.ReviewComment{
			{FilePath: "internal/foo.go", Line: 42, Body: "[high] this could be nil-checked", Severity: "high"},
			{FilePath: "internal/foo.go", Line: 99, Body: "[medium] consider extracting", Severity: "medium"},
		}

		// when
		kept := commands.DropDuplicateComments(rc, context.Background(), provider, repo, prID, newComments)

		// then
		require.Len(t, kept, 1)
		assert.Equal(t, 99, kept[0].Line, "the new comment on a different line must be kept")
	})

	t.Run("should keep PR-wide comments (Line <= 0) regardless of duplicates", func(t *testing.T) {
		t.Parallel()

		// given: PR-wide annotations are not subject to the dedup pass —
		// the F2 review-once gate above already suppresses entire
		// follow-up reviews, so the only path that lands a duplicate
		// PR-wide comment is the explicit @code-guru re-review which the
		// user asked for.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{Body: "summary"},
			},
		}
		newComments := []entities.ReviewComment{
			{Line: 0, Body: "summary", Severity: "comment"},
		}

		// when
		kept := commands.DropDuplicateComments(rc, context.Background(), provider, repo, prID, newComments)

		// then
		assert.Len(t, kept, 1, "PR-wide comments must always pass through the inline-only dedup")
	})

	t.Run("should normalize the leading slash on file paths", func(t *testing.T) {
		t.Parallel()

		// given: ADO's threads carry `filePath: "/internal/foo.go"`
		// while the AI emits `internal/foo.go` — both must dedup.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{FilePath: "/internal/foo.go", Line: 42, Body: "[high] nil-check"},
			},
		}
		newComments := []entities.ReviewComment{
			{FilePath: "internal/foo.go", Line: 42, Body: "[high] nil-check", Severity: "high"},
		}

		// when
		kept := commands.DropDuplicateComments(rc, context.Background(), provider, repo, prID, newComments)

		// then
		assert.Empty(t, kept, "leading-slash normalisation must let the dedup match")
	})
}

func TestExecuteReviewOnceGate(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{
		PullRequest: forgeEntities.PullRequest{ID: 4242, Title: "feat", URL: "https://example/pr/4242"},
	}

	t.Run("should skip when an existing review-complete marker is present and the user has not mentioned the bot", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{Body: "✅ **Code Guru review complete.** Verdict: `approve`"},
			},
		}

		// when
		result, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "comment", result.Verdict)
		assert.Contains(t, result.Summary, "already been reviewed")
		assert.Empty(t, provider.calls, "no marker / annotation should fire when the gate skips the review")
		assert.Empty(t, provider.submissions, "no native review should fire when the gate skips the review")
	})

	t.Run("should NOT skip when UserMentioned is true even with an existing review-complete marker", func(t *testing.T) {
		t.Parallel()

		// given: same precondition as the previous row plus a user
		// mention. The deterministic GetPullRequestFiles error proves
		// Execute reached past the gate (the user-requested re-review
		// went through).
		expectedErr := errors.New("get pull request files failed")
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{Body: "✅ **Code Guru review complete.** Verdict: `approve`"},
			},
			getPullRequestFilesErr: expectedErr,
		}

		// when
		result, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			UserMentioned: true,
		})

		// then
		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr,
			"UserMentioned=true must bypass the review-once gate so the re-review reaches GetPullRequestFiles")
		assert.Nil(t, result)
	})

	t.Run("should proceed when no marker is present", func(t *testing.T) {
		t.Parallel()

		// given: empty existingComments + the deterministic error
		// proves Execute walked past the gate down the normal path.
		expectedErr := errors.New("get pull request files failed")
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			getPullRequestFilesErr: expectedErr,
		}

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{})

		// then
		require.Error(t, err)
		assert.ErrorIs(t, err, expectedErr)
	})
}

func TestBuildConversation(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	const prID = 4242
	diffs := []entities.FileDiff{{Path: "internal/foo.go", Diff: "@@ -1,1 +1,1 @@", Language: "go"}}

	t.Run("should return nil when UserMentioned is false", func(t *testing.T) {
		t.Parallel()

		// given: a PR with several existing bot threads, but the run
		// was push-triggered (no mention). The conversation walk must
		// stay nil so first-pass review prompts are byte-for-byte
		// identical to the pre-conversation shape.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{ID: 1, Line: 10, FilePath: "internal/foo.go", Body: "[high] x", Author: "code-guru[bot]"},
			},
		}

		// when
		got := commands.BuildConversation(rc, context.Background(), provider, repo, prID, diffs,
			commands.ReviewOptions{UserMentioned: false})

		// then
		assert.Nil(t, got)
	})

	t.Run("should populate the conversation when UserMentioned is true", func(t *testing.T) {
		t.Parallel()

		// given
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{ID: 1, Line: 10, FilePath: "internal/foo.go", Body: "[high] nil-check", Author: "code-guru[bot]"},
				{ID: 2, Line: 10, FilePath: "internal/foo.go", Body: "we already handle that", Author: "alice", InReplyToID: 1},
			},
		}

		// when
		got := commands.BuildConversation(rc, context.Background(), provider, repo, prID, diffs,
			commands.ReviewOptions{UserMentioned: true})

		// then
		require.Len(t, got, 1)
		assert.Equal(t, "internal/foo.go", got[0].FilePath)
		assert.Equal(t, 10, got[0].Line)
		require.Len(t, got[0].Comments, 2)
		assert.Equal(t, "code-guru[bot]", got[0].Comments[0].Author)
		assert.Equal(t, "alice", got[0].Comments[1].Author)
	})

	t.Run("should soft-fail to nil when ListPullRequestComments errors", func(t *testing.T) {
		t.Parallel()

		// given: contract is best-effort — a list error must not break
		// the re-review; the F3 dedup catches any duplicates the LLM
		// emits without context.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			listCommentsErr: errors.New("transient API blip"),
		}

		// when
		got := commands.BuildConversation(rc, context.Background(), provider, repo, prID, diffs,
			commands.ReviewOptions{UserMentioned: true})

		// then
		assert.Nil(t, got, "list error must degrade to nil so the re-review still runs")
	})

	t.Run("should drop threads anchored to files outside the live diff", func(t *testing.T) {
		t.Parallel()

		// given: bot has prior comments on both a file in the current
		// diff AND a file the PR no longer touches. Only the live one
		// should reach the prompt.
		rc := commands.NewReviewCommand(nil, nil, nil)
		provider := &recordingReviewProvider{
			existingComments: []forgeEntities.PullRequestComment{
				{ID: 1, Line: 10, FilePath: "internal/foo.go", Body: "[high] live", Author: "code-guru[bot]"},
				{ID: 2, Line: 20, FilePath: "internal/old.go", Body: "[high] stale anchor", Author: "code-guru[bot]"},
			},
		}

		// when
		got := commands.BuildConversation(rc, context.Background(), provider, repo, prID, diffs,
			commands.ReviewOptions{UserMentioned: true})

		// then
		require.Len(t, got, 1, "thread anchored to a file outside the diff must be dropped")
		assert.Equal(t, "internal/foo.go", got[0].FilePath)
	})
}

// recordingRegistry is a TrivialDetectorRegistry that always returns
// Detected=true with a fixed verdict, and records the file paths it
// receives so the test can pin the leading-`/` normalisation contract.
type recordingRegistry struct {
	verdict   string
	lastFiles []string
}

func (r *recordingRegistry) Detect(
	_ context.Context,
	dctx repositories.DetectionContext,
) (repositories.TrivialDetector, repositories.DetectionResult, bool) {
	r.lastFiles = append([]string(nil), dctx.Files...)
	d := &stubNamedDetector{name: "stub"}
	return d, repositories.DetectionResult{
		Detected: true,
		Verdict:  r.verdict,
		Summary:  "trivial",
	}, true
}

type stubNamedDetector struct{ name string }

func (s *stubNamedDetector) Name() string { return s.name }
func (s *stubNamedDetector) Detect(_ context.Context, _ repositories.DetectionContext) repositories.DetectionResult {
	return repositories.DetectionResult{}
}

// failingAIReviewer panics if its ReviewDiff is invoked. The trivial
// path must short-circuit before reaching it; if Execute somehow falls
// through to the AI call, the panic surfaces the regression cleanly.
type failingAIReviewer struct{}

func (failingAIReviewer) Name() string { return "fail" }
func (failingAIReviewer) ReviewDiff(_ context.Context, _ entities.ReviewRequest) (*entities.ReviewResult, error) {
	panic("AI review must not run when a trivial detector matches")
}

func TestExecuteRunsTrivialDetectionRegardlessOfCIPassed(t *testing.T) {
	t.Parallel()

	// Pins the no-CI-gate contract: trivial detection (e.g. docs-only,
	// bump-go) MUST fire even when CIPassed is false. Before this test
	// existed the gate at review_command.go suppressed every detector
	// because no entry point ever set CIPassed=true in production.

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{
		PullRequest: forgeEntities.PullRequest{ID: 4242, Title: "docs", URL: "https://example/pr/4242"},
	}

	t.Run("should return the trivial verdict when CIPassed is false", func(t *testing.T) {
		t.Parallel()

		// given: an ADO-shape leading-`/` path (Azure DevOps's
		// `GetPullRequestFiles` prefixes one onto every path); a
		// registry that always detects + records the paths it sees;
		// an AI that panics if reached; a non-nil rules repo so a
		// future regression that disables the trivial path produces
		// the AI panic — not a `nil rulesRepo` panic that would
		// confuse the failure mode. CIPassed left at its zero value.
		registry := &recordingRegistry{verdict: "approve"}
		rules := &doubles.StubRulesRepository{}
		rc := commands.NewReviewCommand(failingAIReviewer{}, rules, registry)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{{Path: "/CHANGELOG.md"}},
		}

		// when
		result, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			DryRun: true,
		})

		// then
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "approve", result.Verdict,
			"Execute must propagate the trivial detector's verdict even with CIPassed=false")
		assert.Equal(t, "trivial", result.Summary)
		assert.Equal(t, []string{"CHANGELOG.md"}, registry.lastFiles,
			"the leading `/` Azure DevOps prefixes onto every path must be stripped before the detector sees it — otherwise bump detectors miss their required-files match against `CHANGELOG.md`")
	})
}

func TestTrivialFastPathPostsSingleMarkerAndOptionalMerge(t *testing.T) {
	t.Parallel()

	// Pins the trivial-path post / merge contract surfaced by smoke
	// `backend/user-http#NNNNN`: two pods posted four duplicate
	// approvals between them because the trivial path's old
	// `[Auto-Approved]` body did not contain `**Code Guru review`,
	// so the F2 review-once gate did not catch the second ADO
	// `pullrequest.updated` delivery. Native review submission also
	// echoed the body, doubling every review.
	//
	// New contract:
	//   - One PR-wide comment containing the F2 marker.
	//   - Native review submission carries an empty body so it
	//     does not duplicate the annotation.
	//   - `TrivialAutoMerge=true` triggers `MergePullRequest`;
	//     `false` (or a non-approve verdict) does not.

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{
		PullRequest: forgeEntities.PullRequest{ID: 4242, Title: "docs", URL: "https://example/pr/4242"},
	}

	newCmd := func(verdict string) (*commands.ReviewCommand, *recordingReviewProvider) {
		registry := &recordingRegistry{verdict: verdict}
		rules := &doubles.StubRulesRepository{}
		rc := commands.NewReviewCommand(failingAIReviewer{}, rules, registry)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{{Path: "/CHANGELOG.md"}},
		}
		return rc, provider
	}

	t.Run("should post exactly one PR-wide comment carrying the `**Code Guru review` F2 marker on approve", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("approve")

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			SubmitNativeReview: true,
		})

		// then
		require.NoError(t, err)
		require.Len(t, provider.calls, 1, "exactly one PR-wide comment must be posted on a trivial-approve verdict — duplicates flooded the smoke PR before this contract pinned it")
		assert.Contains(t, provider.calls[0].body, "**Code Guru review",
			"the trivial-path comment MUST contain the F2 review-once-gate marker substring; without it the second ADO `pullrequest.updated` delivery re-runs the trivial path and posts again")
		assert.NotContains(t, provider.calls[0].body, "[Auto-Approved]",
			"the legacy `[Auto-Approved]` prefix has been replaced by the unified completion annotation — its presence would mean the dedup contract regressed")
	})

	t.Run("should submit a native review with empty body (vote-only) so it does not duplicate the annotation", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("approve")

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			SubmitNativeReview: true,
		})

		// then
		require.NoError(t, err)
		require.Len(t, provider.submissions, 1, "native review submission still records the reviewer-panel vote")
		assert.Empty(t, provider.submissions[0].Body,
			"the native submission's body MUST be empty so gitforge does not post the trivial summary as a second PR-wide comment alongside the annotation")
	})

	t.Run("should call MergePullRequest with the configured strategy when TrivialAutoMerge=true and verdict=approve", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("approve")

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			TrivialAutoMerge:     true,
			TrivialMergeStrategy: "squash",
		})

		// then
		require.NoError(t, err)
		require.Len(t, provider.merges, 1, "auto-merge must fire on a trivial-approve verdict when the operator opted in")
		assert.Equal(t, 4242, provider.merges[0].prID)
		assert.Equal(t, "squash", provider.merges[0].strategy,
			"the configured merge strategy must reach gitforge unchanged — empty falls back to the platform default, but `squash` is an explicit operator choice")
	})

	t.Run("should NOT call MergePullRequest when TrivialAutoMerge=false (the default)", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("approve")

		// when: TrivialAutoMerge omitted — defaults to false
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		assert.Empty(t, provider.merges,
			"auto-merge is opt-in by design; the default config must NEVER complete a PR cross-system without operator consent")
	})

	t.Run("should NOT call MergePullRequest when verdict=reject even with TrivialAutoMerge=true", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("reject")

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			TrivialAutoMerge: true,
		})

		// then
		require.NoError(t, err)
		assert.Empty(t, provider.merges,
			"auto-merge fires only on the approve verdict — a trivial detector that rejects (e.g. an incomplete bump per `.autobump.yaml`) must never auto-merge")
	})
}
