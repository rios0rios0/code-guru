package trivial

import (
	"fmt"
	"path/filepath"
	"strings"
)

var bumpPythonExact = map[string]bool{
	"pyproject.toml": true,
	"CHANGELOG.md":   true,
}

// BumpPythonDetector detects Python dependency bump PRs.
type BumpPythonDetector struct{}

// Name returns the adapter identifier.
func (d *BumpPythonDetector) Name() string {
	return "bump-python"
}

// IsTrivial returns true if all changed files are Python dependency files.
func (d *BumpPythonDetector) IsTrivial(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		base := filepath.Base(f)
		if bumpPythonExact[base] || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
			continue
		}
		return false
	}
	return true
}

// Summary returns a description for the auto-approval comment.
func (d *BumpPythonDetector) Summary(files []string) string {
	return fmt.Sprintf("Python dependency bump detected (%d files). Auto-approved by trivial PR policy.", len(files))
}
