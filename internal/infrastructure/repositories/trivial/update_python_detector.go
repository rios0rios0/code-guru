package trivial

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// updatePythonDependencyFiles are the Python manifest and lock files a
// dependency update edits, besides the `requirements*.txt` files.
//
//nolint:gochecknoglobals // constant lookup map
var updatePythonDependencyFiles = map[string]bool{
	"pyproject.toml": true,
	"pdm.lock":       true,
	"poetry.lock":    true,
	"uv.lock":        true,
}

// UpdatePythonDetector detects Python dependency update PRs.
type UpdatePythonDetector struct{}

// Name returns the adapter identifier.
func (d *UpdatePythonDetector) Name() string {
	return "update-python"
}

// Detect checks whether the PR only updates Python dependencies. See
// detectDependencyUpdate for the files it may change besides them.
func (d *UpdatePythonDetector) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	return detectDependencyUpdate(ctx, dctx, isPythonDependencyFile, "Python dependency update detected")
}

func isPythonDependencyFile(filePath string) bool {
	base := filepath.Base(filePath)
	return updatePythonDependencyFiles[base] ||
		(strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"))
}
