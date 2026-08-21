//go:build unit

package main_test

import (
	"go/version"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dockerfilePath is the delivery stage's build recipe, relative to the module root.
const dockerfilePath = ".ci/stages/40-delivery/app.Dockerfile"

// goDirectivePattern captures the version in the `go` directive of `go.mod`.
const goDirectivePattern = `(?m)^go\s+([0-9]+(?:\.[0-9]+)*)\b`

// builderTagPattern captures the version in the `FROM golang:<tag> AS builder` instruction.
const builderTagPattern = `(?mi)^FROM\s+golang:([0-9]+(?:\.[0-9]+)*)\S*\s+AS\s+builder`

// moduleRoot walks up from the test's working directory until it finds `go.mod`,
// so the test keeps working regardless of how deeply the package is nested.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not locate go.mod above the working directory")
		dir = parent
	}
}

// captureVersion reads the file at path and returns the single capture group of pattern,
// prefixed with "go" so the result is comparable with the go/version package.
func captureVersion(t *testing.T, path, pattern, what string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)

	match := regexp.MustCompile(pattern).FindSubmatch(content)
	require.Len(t, match, 2, "could not find the %s in %q", what, path)

	return "go" + string(match[1])
}

func TestDockerBuilderTracksGoDirective(t *testing.T) {
	t.Parallel()

	t.Run("should pin a builder image no older than the `go` directive", func(t *testing.T) {
		t.Parallel()

		// given
		root := moduleRoot(t)
		required := captureVersion(t, filepath.Join(root, "go.mod"), goDirectivePattern, "`go` directive")
		builder := captureVersion(t, filepath.Join(root, dockerfilePath), builderTagPattern, "builder tag")
		require.True(t, version.IsValid(required), "unparseable `go` directive %q", required)
		require.True(t, version.IsValid(builder), "unparseable builder tag %q", builder)

		// when
		comparison := version.Compare(builder, required)

		// then
		assert.GreaterOrEqual(t, comparison, 0,
			"the builder image (%s) is older than the `go` directive in go.mod (%s). "+
				"The official golang images set GOTOOLCHAIN=local, so the delivery stage aborts at "+
				"`go mod download` and no image is published. Bump the `FROM golang:` tag in %s "+
				"in the same commit that bumps go.mod.",
			builder, required, dockerfilePath)
	})
}
