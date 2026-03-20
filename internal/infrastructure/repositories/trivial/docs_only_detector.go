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
func (d *DocsOnlyDetector) Detect(_ context.Context, dctx repositories.DetectionContext) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	for _, f := range dctx.Files {
		if !strings.HasSuffix(strings.ToLower(f), ".md") {
			return repositories.DetectionResult{}
		}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"Documentation-only change detected (%d markdown files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
