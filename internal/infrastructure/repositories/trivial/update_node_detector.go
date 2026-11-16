package trivial

import (
	"context"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// updateNodeDependencyFiles are the npm, Yarn and pnpm manifest and lock
// files a dependency update edits.
//
//nolint:gochecknoglobals // constant lookup map
var updateNodeDependencyFiles = map[string]bool{
	"package.json":      true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
}

// UpdateNodeDetector detects Node.js dependency update PRs.
type UpdateNodeDetector struct{}

// Name returns the adapter identifier.
func (d *UpdateNodeDetector) Name() string {
	return "update-node"
}

// Detect checks whether the PR only updates Node.js dependencies. See
// detectDependencyUpdate for the files it may change besides them.
func (d *UpdateNodeDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	return detectDependencyUpdate(ctx, dctx, isNodeDependencyFile, "Node.js dependency update detected")
}

func isNodeDependencyFile(filePath string) bool {
	return updateNodeDependencyFiles[filepath.Base(filePath)]
}
