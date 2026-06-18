package trivial

import (
	"context"
	"fmt"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// DocsOnlyDetector detects PRs that only change Markdown documentation files.
type DocsOnlyDetector struct{}

// Name returns the adapter identifier.
func (d *DocsOnlyDetector) Name() string {
	return "docs-only"
}

// Detect checks whether all changed files are Markdown files.
//
// A change that touches ONLY the CHANGELOG is a version-bump / release
// artifact, not documentation, so it is declined here and left to the
// bump-* detectors (see isChangelogOnly). The CHANGELOG may still
// accompany a genuine docs PR (e.g. README.md + CHANGELOG.md).
func (d *DocsOnlyDetector) Detect(_ context.Context, dctx repositories.DetectionContext) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	for _, f := range dctx.Files {
		if !strings.HasSuffix(strings.ToLower(f), ".md") {
			return repositories.DetectionResult{}
		}
	}
	if isChangelogOnly(dctx.Files) {
		return repositories.DetectionResult{}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  verdictApprove,
		Summary: fmt.Sprintf(
			"Documentation-only change detected (%d markdown files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
