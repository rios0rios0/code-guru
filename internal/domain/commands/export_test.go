//go:build unit

package commands

// Re-exports of the unexported helpers in the `commands` package so the
// external `commands_test` package can pin their contracts directly,
// without standing up stubs for every repository/provider/registry that
// the full `Execute` flow would otherwise require. The variable
// indirection keeps each helper unexported in production builds (the
// file is gated on the `unit` build tag).
//
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
	FilterStaleComments      = filterStaleComments
	SummarizeStaleFilePaths  = summarizeStaleFilePaths
	NormalizeFilePathForTest = normalizeFilePath
	BuildReviewingMarkerBody = buildReviewingMarkerBody
	BuildReviewFailedBody    = buildReviewFailedBody
	BuildReviewCompleteBody  = buildReviewCompleteBody
	IsPullRequestClosed      = isPullRequestClosed

	// PostReviewingMarker / PostReviewCompleteAnnotation /
	// PostReviewFailedAnnotation are method-value re-exports of the
	// `*ReviewCommand` post helpers so external tests can pin the
	// option-forwarding contract without going through the full
	// `Execute` flow (which would require a stub for every
	// repository, registry, and provider method). A regression in
	// the `WithThreadStatus("closed")` wiring would surface as the
	// recorded option list in the test stub no longer matching the
	// expected value — the contract that tells ADO to render every
	// informational annotation as a closed thread instead of one
	// the PR author has to dismiss by hand.
	PostReviewingMarker          = (*ReviewCommand).postReviewingMarker
	PostReviewCompleteAnnotation = (*ReviewCommand).postReviewCompleteAnnotation
	PostReviewFailedAnnotation   = (*ReviewCommand).postReviewFailedAnnotation

	// SubmitNativeReview is a method-value re-export of the
	// `*ReviewCommand.submitNativeReview` helper so external tests can
	// pin the verdict-mapping + flag-gating + soft-fail contract
	// without going through the full Execute flow.
	SubmitNativeReview = (*ReviewCommand).submitNativeReview

	// DropDuplicateComments is the method-value re-export of the
	// comment-dedup pass so external tests can drive the filter
	// (file+line+body-prefix fingerprint) without standing up the
	// full postComments pipeline.
	DropDuplicateComments = (*ReviewCommand).dropDuplicateComments

	// CommentDedupKey is the pure fingerprint helper backing the
	// filter — exposed so tests can pin the leading-slash
	// normalisation and the `commentDedupBodyPrefix` cap.
	CommentDedupKey = commentDedupKey

	// BuildConversation re-exports the per-command conversation walk
	// helper so tests can pin (1) the UserMentioned gate, (2) the
	// soft-fail-on-list-error contract, without standing up the full
	// Execute flow.
	BuildConversation = (*ReviewCommand).buildConversation

	// ApplyThreadResolutions re-exports the resolution-aware
	// re-review helper so tests can pin the per-resolution behaviour
	// (reply on every thread, auto-close `resolved` / `outdated`,
	// leave `outstanding` active, skip unmatched anchors) without
	// running the full Execute pipeline. Returns the set of handled
	// (file, line) anchors so the caller can assert the dedup gate
	// for the surrounding `postComments` pass.
	ApplyThreadResolutions = (*ReviewCommand).applyThreadResolutions

	// BuildResolutionReplyBody is the pure renderer for the inline
	// reply body the bot posts on each prior thread. Exposed so tests
	// can pin the headline-per-status contract without standing up a
	// stub provider.
	BuildResolutionReplyBody = buildResolutionReplyBody

	// MapResolutionStatusToThreadState turns the LLM vocabulary
	// (`resolved` / `outstanding` / `outdated`) into the platform
	// thread-status string forwarded to gitforge's
	// `UpdatePullRequestThreadStatus`. Exposed so tests can pin the
	// mapping without driving the full resolution pipeline.
	MapResolutionStatusToThreadState = mapResolutionStatusToThreadState

	// ShouldCloseResolution reports whether a given LLM status maps
	// to a thread state that should auto-close the thread.
	ShouldCloseResolution = shouldCloseResolution
)

// AnnotationThreadStatus re-exports the unexported package constant
// so tests can pin the value the post helpers forward to gitforge —
// `"closed"` is what ADO treats as "discussion ended", which is the
// shape the PR author should see for purely informational threads.
const AnnotationThreadStatus = annotationThreadStatus

// PullRequestStatusGetter is the test-only alias for the unexported
// `pullRequestStatusGetter` interface so external tests can build a
// 1-method stub without depending on the full `forgeEntities.ReviewProvider`.
type PullRequestStatusGetter = pullRequestStatusGetter
