package repositories

import "context"

// DetectionContext holds the input data needed by trivial detectors.
type DetectionContext struct {
	Files              []string
	RepoName           string
	FileContentFetcher FileContentFetcher
}

// FileContentFetcher provides read access to files in a remote repository.
type FileContentFetcher interface {
	GetFileContent(ctx context.Context, path string) (string, error)
	HasFile(ctx context.Context, path string) bool
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
