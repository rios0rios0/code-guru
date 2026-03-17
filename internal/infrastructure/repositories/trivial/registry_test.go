//go:build unit

package trivial_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
)

func TestDetectorRegistry(t *testing.T) {
	t.Parallel()

	t.Run("should detect bump-go PR when only go.mod and go.sum changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go", "docs-only"})
		files := []string{"go.mod", "go.sum"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
	})

	t.Run("should detect bump-go PR when go.mod, go.sum, and CHANGELOG.md changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		files := []string{"go.mod", "go.sum", "CHANGELOG.md"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
	})

	t.Run("should not detect trivial PR when code files are included", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go", "docs-only"})
		files := []string{"go.mod", "go.sum", "main.go"}

		// when
		_, found := registry.Detect(files)

		// then
		assert.False(t, found)
	})

	t.Run("should detect docs-only PR when only markdown files changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go", "docs-only"})
		files := []string{"README.md", "docs/guide.md"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "docs-only", detector.Name())
	})

	t.Run("should return first matching detector", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go", "docs-only"})
		files := []string{"CHANGELOG.md"} // matches both bump-go and docs-only

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name()) // bump-go registered first
	})

	t.Run("should not detect anything with empty file list", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		var files []string

		// when
		_, found := registry.Detect(files)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect anything with nil enabled list", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry(nil)
		files := []string{"go.mod", "go.sum"}

		// when
		_, found := registry.Detect(files)

		// then
		assert.False(t, found)
	})

	t.Run("should detect bump-node PR with package.json and lock file", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		files := []string{"package.json", "package-lock.json", "CHANGELOG.md"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
	})

	t.Run("should detect bump-python PR with pyproject.toml", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		files := []string{"pyproject.toml", "requirements.txt", "CHANGELOG.md"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-python", detector.Name())
	})

	t.Run("should return non-empty summary from matched detector", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		files := []string{"go.mod", "go.sum"}

		// when
		detector, found := registry.Detect(files)

		// then
		assert.True(t, found)
		assert.NotEmpty(t, detector.Summary(files))
	})
}
