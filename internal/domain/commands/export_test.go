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
var (
	ShouldPostSummary        = shouldPostSummary
	FilterStaleComments      = filterStaleComments
	SummarizeStaleFilePaths  = summarizeStaleFilePaths
	NormalizeFilePathForTest = normalizeFilePath
)
