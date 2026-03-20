//go:build unit

package trivial_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
)

func TestDetectorRegistry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("should detect update-go PR when only go.mod and go.sum changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go", "docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should detect update-go PR when go.mod, go.sum, and CHANGELOG.md changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should not detect trivial PR when code files are included", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go", "docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum", "main.go"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should detect docs-only PR when only markdown files changed", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go", "docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"README.md", "docs/guide.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "docs-only", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should return first matching detector", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go", "docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}} // matches both update-go and docs-only

		// when
		detector, _, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-go", detector.Name()) // update-go registered first
	})

	t.Run("should not detect anything with empty file list", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect anything with nil enabled list", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry(nil)
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should detect update-node PR with package.json and lock file", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-node"})
		dctx := repositories.DetectionContext{Files: []string{"package.json", "package-lock.json", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-node", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should detect update-python PR with pyproject.toml", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-python"})
		dctx := repositories.DetectionContext{Files: []string{"pyproject.toml", "requirements.txt", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-python", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should return non-empty summary from matched detector", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum"}}

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.NotEmpty(t, result.Summary)
	})

	t.Run("should detect bump-go PR with only CHANGELOG.md", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should detect bump-node PR with package.json and CHANGELOG.md", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{Files: []string{"package.json", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should detect bump-python PR with __init__.py and CHANGELOG.md", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{Files: []string{"mypackage/__init__.py", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-python", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should not detect bump-python when __init__.py path has no parent dir", func(t *testing.T) {
		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{Files: []string{"__init__.py", "CHANGELOG.md"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should approve bump-go with autobump when all version files present", func(t *testing.T) {
		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  go:\n    version_files: []\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := repositories.DetectionContext{
			Files:              []string{"CHANGELOG.md"},
			RepoName:           "myrepo",
			FileContentFetcher: fetcher,
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should reject bump-python with autobump when version file is missing", func(t *testing.T) {
		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  python:\n    version_files:\n      - path: \"{project_name}/__init__.py\"\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{
			Files:              []string{"CHANGELOG.md"},
			RepoName:           "mypackage",
			FileContentFetcher: fetcher,
		}

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "reject", result.Verdict)
		assert.Contains(t, result.Summary, "mypackage/__init__.py")
	})

	t.Run("should approve bump-python with autobump when all files present", func(t *testing.T) {
		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  python:\n    version_files:\n      - path: \"{project_name}/__init__.py\"\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{
			Files:              []string{"mypackage/__init__.py", "CHANGELOG.md"},
			RepoName:           "mypackage",
			FileContentFetcher: fetcher,
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-python", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should fall back to default patterns when autobump fetch fails", func(t *testing.T) {
		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{}, // no autobump file
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := repositories.DetectionContext{
			Files:              []string{"CHANGELOG.md"},
			RepoName:           "myrepo",
			FileContentFetcher: fetcher,
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should list all 7 available detectors", func(t *testing.T) {
		// given / when
		names := trivial.AvailableDetectors()

		// then
		assert.Len(t, names, 7)
	})
}

// stubFileContentFetcher is a test double for the FileContentFetcher interface.
type stubFileContentFetcher struct {
	files map[string]string
}

func (s *stubFileContentFetcher) GetFileContent(_ context.Context, path string) (string, error) {
	content, ok := s.files[path]
	if !ok {
		return "", fmt.Errorf("file not found: %s", path)
	}
	return content, nil
}

func (s *stubFileContentFetcher) HasFile(_ context.Context, path string) bool {
	_, ok := s.files[path]
	return ok
}
