package trivial

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var bumpNodeAllowed = map[string]bool{
	"package.json": true,
	"CHANGELOG.md": true,
}

// BumpNodeDetector detects Node.js version bump (release ceremony) PRs.
// Default expected files: package.json + CHANGELOG.md.
// When .autobump.yaml exists, the expected set is expanded with its version_files
// under the "typescript" language key.
type BumpNodeDetector struct{}

// Name returns the adapter identifier.
func (d *BumpNodeDetector) Name() string {
	return "bump-node"
}

// Detect checks whether the PR matches a Node.js version bump pattern.
func (d *BumpNodeDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	return detectBump(ctx, dctx, "bump-node", "typescript", bumpNodeAllowed, nil)
}
