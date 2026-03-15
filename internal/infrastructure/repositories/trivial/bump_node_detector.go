package trivial

import (
	"fmt"
	"path/filepath"
)

var bumpNodeAllowed = map[string]bool{
	"package.json":      true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"CHANGELOG.md":      true,
}

// BumpNodeDetector detects Node.js dependency bump PRs.
type BumpNodeDetector struct{}

// Name returns the adapter identifier.
func (d *BumpNodeDetector) Name() string {
	return "bump-node"
}

// IsTrivial returns true if all changed files are Node.js dependency files.
func (d *BumpNodeDetector) IsTrivial(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !bumpNodeAllowed[filepath.Base(f)] {
			return false
		}
	}
	return true
}

// Summary returns a description for the auto-approval comment.
func (d *BumpNodeDetector) Summary(files []string) string {
	return fmt.Sprintf("Node.js dependency bump detected (%d files). Auto-approved by trivial PR policy.", len(files))
}
