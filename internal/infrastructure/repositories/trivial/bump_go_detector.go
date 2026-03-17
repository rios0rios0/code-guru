package trivial

import (
	"fmt"
	"path/filepath"
)

//nolint:gochecknoglobals // constant lookup map
var bumpGoAllowed = map[string]bool{
	"go.mod":       true,
	"go.sum":       true,
	"CHANGELOG.md": true,
}

// BumpGoDetector detects Go dependency bump PRs.
type BumpGoDetector struct{}

// Name returns the adapter identifier.
func (d *BumpGoDetector) Name() string {
	return "bump-go"
}

// IsTrivial returns true if all changed files are Go dependency files.
func (d *BumpGoDetector) IsTrivial(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !bumpGoAllowed[filepath.Base(f)] {
			return false
		}
	}
	return true
}

// Summary returns a description for the auto-approval comment.
func (d *BumpGoDetector) Summary(files []string) string {
	return fmt.Sprintf("Go dependency bump detected (%d files). Auto-approved by trivial PR policy.", len(files))
}
