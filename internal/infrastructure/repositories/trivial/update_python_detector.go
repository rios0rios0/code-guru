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
	changelogFile:    true,
}

// UpdatePythonDetector detects Python dependency update PRs.
type UpdatePythonDetector struct{}

// Name returns the adapter identifier.
func (d *UpdatePythonDetector) Name() string {
	return "update-python"
}

// Detect checks whether all changed files are Python dependency files.
func (d *UpdatePythonDetector) Detect(
	ctx context.Context,
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
			"Python dependency update detected (%d files). Auto-approved by trivial PR policy.",
			len(dctx.Files),
		),
	}
}
