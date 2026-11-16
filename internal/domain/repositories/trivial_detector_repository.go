package repositories

import "context"

// DetectionContext holds the input data needed by trivial detectors.
type DetectionContext struct {
	// Files lists every path the pull request changes, without the leading
	// slash some providers prefix. A deleted file is listed under the path it
	// had before the pull request.
	Files []string
	// DeletedFiles holds the entries of Files that the pull request deletes.
	// Nil means none, or that the caller does not know.
	DeletedFiles       map[string]bool
	RepoName           string
	FileContentFetcher FileContentFetcher
	// FileDiffFetcher serves the unified diff of one changed file, so a
	// detector can check what a change does and not only which files it
	// touches. Nil when no diff is available; a detector that needs one then
	// declines the pull request rather than guess.
	FileDiffFetcher FileDiffFetcher
}

// FileContentFetcher provides read access to files in a remote repository.
type FileContentFetcher interface {
	GetFileContent(ctx context.Context, path string) (string, error)
	HasFile(ctx context.Context, path string) bool
}

// FileDiffFetcher provides the diff of a file the pull request under
// detection changes.
type FileDiffFetcher interface {
	// GetFileDiff returns the unified diff of one changed file (its hunks,
	// with or without the file header lines), or an error when the provider
	// cannot produce it.
	GetFileDiff(ctx context.Context, path string) (string, error)
}

// DetectionResult holds the outcome of a trivial detection check.
type DetectionResult struct {
	Detected bool
	Verdict  string
	Summary  string
}

// TrivialDetector determines whether a PR is trivial based on its changed files.
// Trivial PRs can be auto-approved or rejected without invoking the LLM, saving tokens.
type TrivialDetector interface {
	// Name returns the adapter identifier (e.g., "update-go", "bump-node", "docs-only").
	Name() string

	// Detect checks whether the PR files match this adapter's trivial pattern
	// and returns the detection outcome.
	Detect(ctx context.Context, dctx DetectionContext) DetectionResult
}

// TrivialDetectorRegistry checks a list of files against enabled trivial detectors.
type TrivialDetectorRegistry interface {
	// Detect returns the first matching detector, its result, and true;
	// or nil, empty result, and false if none match.
	Detect(ctx context.Context, dctx DetectionContext) (TrivialDetector, DetectionResult, bool)
}
