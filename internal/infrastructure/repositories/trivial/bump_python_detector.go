package trivial

import (
	"context"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var bumpPythonExact = map[string]bool{
	"CHANGELOG.md": true,
}

// BumpPythonDetector detects Python version bump (release ceremony) PRs.
// Default expected files: any */__init__.py + CHANGELOG.md.
// When .autobump.yaml exists, the expected set is expanded with its version_files
// under the "python" language key.
type BumpPythonDetector struct{}

// Name returns the adapter identifier.
func (d *BumpPythonDetector) Name() string {
	return "bump-python"
}

// Detect checks whether the PR matches a Python version bump pattern.
func (d *BumpPythonDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	return detectBump(ctx, dctx, "bump-python", "python", bumpPythonExact, isPythonInitFile)
}

func isPythonInitFile(path string) bool {
	return strings.HasSuffix(path, "/__init__.py")
}
