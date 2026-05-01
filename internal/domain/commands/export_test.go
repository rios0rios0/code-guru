//go:build unit

package commands

// Re-exports of the unexported helpers in the `commands` package so the
// external `commands_test` package can pin their contracts directly,
// without standing up stubs for every repository/provider/registry that
// the full `Execute` flow would otherwise require. The variable
// indirection keeps each helper unexported in production builds (the
// file is gated on the `unit` build tag).
//
//   - `ShouldPostSummary`        — gating decision for the PR-wide
//     summary thread (suppressed when per-issue comments are present).
//   - `FilterStaleComments`      — partitions AI findings into kept vs
//     dropped, where dropped means "FilePath is no longer in the
//     latest PR iteration" (only applies to inline `Line > 0`
//     comments; PR-wide comments are always kept).
//   - `SummarizeStaleFilePaths`  — bounded log summariser for the
//     dropped paths, deduped on the normalised form.
//   - `NormalizeFilePathForTest` — leading-slash normalisation
//     mirroring `support.LookupChunkByPath` on the diff side, so AI
//     paths and ADO-shape paths compare consistently.
//   - `BuildReviewingMarkerBody` — pure renderer for the "🤖
//     reviewing" marker body (PR `#102`).
//   - `BuildReviewFailedBody`    — pure renderer for the "⚠️ review
//     failed" annotation body (PR `#103`).
//   - `BuildReviewCompleteBody`  — pure renderer for the "✅ review
//     complete" annotation body (PR `#104`).
//   - `IsPullRequestClosed`      — re-checks PR status via
//     `gitforge.GetPullRequestStatus` so the bot skips posting on
//     PRs merged / abandoned / closed mid-flight (task `#43`).
//   - `PullRequestStatusGetter`  — test-only alias for the narrow
//     `pullRequestStatusGetter` interface that `IsPullRequestClosed`
//     consumes; tests build a 1-method stub instead of a full
//     `forgeEntities.ReviewProvider`.
var (
	ShouldPostSummary        = shouldPostSummary
	FilterStaleComments      = filterStaleComments
	SummarizeStaleFilePaths  = summarizeStaleFilePaths
	NormalizeFilePathForTest = normalizeFilePath
	BuildReviewingMarkerBody = buildReviewingMarkerBody
	BuildReviewFailedBody    = buildReviewFailedBody
	BuildReviewCompleteBody  = buildReviewCompleteBody
	IsPullRequestClosed      = isPullRequestClosed
)

// PullRequestStatusGetter is the test-only alias for the unexported
// `pullRequestStatusGetter` interface so external tests can build a
// 1-method stub without depending on the full `forgeEntities.ReviewProvider`.
type PullRequestStatusGetter = pullRequestStatusGetter
