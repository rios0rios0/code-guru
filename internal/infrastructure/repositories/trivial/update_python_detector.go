package trivial

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

//nolint:gochecknoglobals // constant lookup map
var updatePythonExact = map[string]bool{
	"pyproject.toml": true,
	"CHANGELOG.md":   true,
}

// UpdatePythonDetector detects Python dependency update PRs.
type UpdatePythonDetector struct{}

// Name returns the adapter identifier.
func (d *UpdatePythonDetector) Name() string {
	return "update-python"
}

// Detect checks whether all changed files are Python dependency files.
func (d *UpdatePythonDetector) Detect(
	_ context.Context,
	dctx repositories.DetectionContext,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	for _, f := range dctx.Files {
		base := filepath.Base(f)
		if updatePythonExact[base] || (strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt")) {
			continue
		}
		return repositories.DetectionResult{}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"Python dependency update detected (%d files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
