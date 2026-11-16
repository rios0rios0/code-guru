package trivial

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var updateGoAllowed = map[string]bool{
	"go.mod":      true,
	"go.sum":      true,
	changelogFile: true,
}

// UpdateGoDetector detects Go dependency update PRs.
type UpdateGoDetector struct{}

// Name returns the adapter identifier.
func (d *UpdateGoDetector) Name() string {
	return "update-go"
}

// Detect checks whether all changed files are Go dependency files.
func (d *UpdateGoDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	for _, f := range dctx.Files {
		if !updateGoAllowed[filepath.Base(f)] {
			return repositories.DetectionResult{}
		}
	}
	// A release is not a dependency update, even when its files would
	// pass for one (a Node bump edits `package.json` + `CHANGELOG.md`): it
	// must carry no release heading and be more than a CHANGELOG edit to
	// count here. See declinesAsRelease.
	if declinesAsRelease(ctx, dctx) {
		return repositories.DetectionResult{}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  verdictApprove,
		Summary: fmt.Sprintf(
			"Go dependency update detected (%d files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
