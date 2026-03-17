package repositories

// TrivialDetector determines whether a PR is trivial based on its changed files.
// Trivial PRs can be auto-merged without invoking the LLM, saving tokens.
type TrivialDetector interface {
	// Name returns the adapter identifier (e.g., "bump-go", "docs-only").
	Name() string

	// IsTrivial returns true if all changed files match this adapter's trivial pattern.
	IsTrivial(files []string) bool

	// Summary returns a human-readable summary for the approval comment.
	Summary(files []string) string
}

// TrivialDetectorRegistry checks a list of files against enabled trivial detectors.
type TrivialDetectorRegistry interface {
	// Detect returns the first matching detector and true, or nil and false if none match.
	Detect(files []string) (TrivialDetector, bool)
}
