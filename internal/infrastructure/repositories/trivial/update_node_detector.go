package trivial

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var updateNodeAllowed = map[string]bool{
	"package.json":      true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	changelogFile:       true,
}

// UpdateNodeDetector detects Node.js dependency update PRs.
type UpdateNodeDetector struct{}

// Name returns the adapter identifier.
func (d *UpdateNodeDetector) Name() string {
	return "update-node"
}

// Detect checks whether all changed files are Node.js dependency files.
func (d *UpdateNodeDetector) Detect(
	_ context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	for _, f := range dctx.Files {
		if !updateNodeAllowed[filepath.Base(f)] {
			return repositories.DetectionResult{}
		}
	}
	// A CHANGELOG-only change is a version bump, not a dependency update —
	// it must carry an actual manifest/lockfile change to count here. See
	// isChangelogOnly.
	if isChangelogOnly(dctx.Files) {
		return repositories.DetectionResult{}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  verdictApprove,
		Summary: fmt.Sprintf(
			"Node.js dependency update detected (%d files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
