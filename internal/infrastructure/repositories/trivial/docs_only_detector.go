package trivial

import (
	"fmt"
	"strings"
)

// DocsOnlyDetector detects PRs that only change Markdown documentation files.
type DocsOnlyDetector struct{}

// Name returns the adapter identifier.
func (d *DocsOnlyDetector) Name() string {
	return "docs-only"
}

// IsTrivial returns true if all changed files are Markdown files.
func (d *DocsOnlyDetector) IsTrivial(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f), ".md") {
			return false
		}
	}
	return true
}

// Summary returns a description for the auto-approval comment.
func (d *DocsOnlyDetector) Summary(files []string) string {
	return fmt.Sprintf(
		"Documentation-only change detected (%d markdown files). Auto-approved by trivial PR policy.",
		len(files),
	)
}
