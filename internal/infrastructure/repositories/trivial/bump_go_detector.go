package trivial

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var bumpGoAllowed = map[string]bool{
	"CHANGELOG.md": true,
}

// BumpGoDetector detects Go version bump (release ceremony) PRs.
// Default expected files: CHANGELOG.md only (Go uses git tags for versioning).
// When .autobump.yaml exists, the expected set is expanded with its version_files.
type BumpGoDetector struct{}

// Name returns the adapter identifier.
func (d *BumpGoDetector) Name() string {
	return "bump-go"
}

// Detect checks whether the PR matches a Go version bump pattern.
func (d *BumpGoDetector) Detect(ctx context.Context, dctx repositories.DetectionContext) repositories.DetectionResult {
	return detectBump(ctx, dctx, "bump-go", "go", bumpGoAllowed, nil)
}
