package trivial

import (
	"context"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// updateGoDependencyFiles are the Go module and workspace files a dependency
// update edits.
//
//nolint:gochecknoglobals // constant lookup map
var updateGoDependencyFiles = map[string]bool{
	"go.mod":      true,
	"go.sum":      true,
	"go.work":     true,
	"go.work.sum": true,
}

// UpdateGoDetector detects Go dependency update PRs.
type UpdateGoDetector struct{}

// Name returns the adapter identifier.
func (d *UpdateGoDetector) Name() string {
	return "update-go"
}

// Detect checks whether the PR only updates Go dependencies. See
// detectDependencyUpdate for the files it may change besides them.
func (d *UpdateGoDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	return detectDependencyUpdate(ctx, dctx, isGoDependencyFile, "Go dependency update detected")
}

func isGoDependencyFile(filePath string) bool {
	return updateGoDependencyFiles[filepath.Base(filePath)]
}
