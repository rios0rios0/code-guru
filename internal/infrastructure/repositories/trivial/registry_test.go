package trivial_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

func TestDetectorRegistry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("should detect update-go PR when only go.mod and go.sum changed", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go", "docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum", "main.go"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should detect docs-only PR when only markdown files changed", func(t *testing.T) {
		t.Parallel()

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

	t.Run("should return the first matching detector when several match", func(t *testing.T) {
		t.Parallel()

		// given -- a CHANGELOG-only release matches both bump-node and bump-go;
		// the first registered detector wins.
		registry := trivial.NewDetectorRegistry([]string{"bump-node", "bump-go"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		detector, _, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-node", detector.Name()) // bump-node registered first
	})

	t.Run("should not detect a CHANGELOG-only change via docs-only (it is a version bump)", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a CHANGELOG-only change via update-go (it is a version bump)", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run(
		"should not auto-approve a CHANGELOG-only bump when only docs/update adapters are enabled",
		func(t *testing.T) {
			t.Parallel()

			// given -- the bump-* adapters are intentionally disabled, leaving only
			// docs-only and the update-* family; a CHANGELOG-only version bump must
			// NOT slip through any of them.
			registry := trivial.NewDetectorRegistry(
				[]string{"docs-only", "update-go", "update-node", "update-python"},
			)
			dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

			// when
			_, _, found := registry.Detect(ctx, dctx)

			// then
			assert.False(t, found)
		},
	)

	t.Run("should still claim a CHANGELOG-only change with bump-go when that adapter is enabled", func(t *testing.T) {
		t.Parallel()

		// given -- with a bump adapter enabled, the CHANGELOG-only bump is claimed
		// by it (not by docs-only / update-go, which decline it).
		registry := trivial.NewDetectorRegistry([]string{"docs-only", "update-go", "bump-go"})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should still detect docs-only when a real doc accompanies the CHANGELOG", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"docs-only"})
		dctx := repositories.DetectionContext{Files: []string{"README.md", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "docs-only", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should still detect update-go when a manifest accompanies the CHANGELOG", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "CHANGELOG.md"}}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "update-go", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should not detect anything with empty file list", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"update-go"})
		dctx := repositories.DetectionContext{Files: []string{}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect anything with nil enabled list", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry(nil)
		dctx := repositories.DetectionContext{Files: []string{"go.mod", "go.sum"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should detect update-node PR with package.json and lock file", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files: []string{"package.json", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": packageJSONVersionDiff,
			}},
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should detect bump-python PR with __init__.py and CHANGELOG.md", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{
			Files: []string{"mypackage/__init__.py", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md":          releaseChangelogDiff,
				"mypackage/__init__.py": pythonInitVersionDiff,
			}},
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-python", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should not detect bump-python when __init__.py path has no parent dir", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-python"})
		dctx := repositories.DetectionContext{Files: []string{"__init__.py", "CHANGELOG.md"}}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should approve bump-go with autobump when all version files present", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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
		t.Parallel()

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
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md":          releaseChangelogDiff,
				"mypackage/__init__.py": pythonInitVersionDiff,
			}},
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "bump-python", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should approve bump-node with autobump typescript section", func(t *testing.T) {
		t.Parallel()

		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  typescript:\n    version_files:\n      - path: \"package.json\"\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files:              []string{"package.json", "CHANGELOG.md"},
			RepoName:           "my-app",
			FileContentFetcher: fetcher,
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": packageJSONVersionDiff,
			}},
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
		assert.Contains(t, result.Summary, ".autobump.yaml")
	})

	t.Run("should reject bump-node with autobump when version file from config is missing", func(t *testing.T) {
		t.Parallel()

		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  typescript:\n    version_files:\n      - path: \"package.json\"\n      - path: \"src/version.ts\"\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files:              []string{"package.json", "CHANGELOG.md"},
			RepoName:           "my-app",
			FileContentFetcher: fetcher,
		}

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		assert.True(t, found)
		assert.Equal(t, "reject", result.Verdict)
		assert.Contains(t, result.Summary, "src/version.ts")
	})

	t.Run("should approve bump-node with autobump when additional version files are present", func(t *testing.T) {
		t.Parallel()

		// given
		fetcher := &stubFileContentFetcher{
			files: map[string]string{
				".autobump.yaml": "languages:\n  typescript:\n    version_files:\n      - path: \"package.json\"\n      - path: \"src/version.ts\"\n",
			},
		}
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files:              []string{"package.json", "CHANGELOG.md", "src/version.ts"},
			RepoName:           "my-app",
			FileContentFetcher: fetcher,
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md":   releaseChangelogDiff,
				"package.json":   packageJSONVersionDiff,
				"src/version.ts": lineChangeDiff("export const version = '1.3.0';", "export const version = '1.4.0';"),
			}},
		}

		// when
		detector, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should fall back to default patterns when autobump fetch fails", func(t *testing.T) {
		t.Parallel()

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
		t.Parallel()

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

func TestNewDetectorRegistryFromConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dctx := repositories.DetectionContext{Files: []string{"README.md"}}

	cases := []struct {
		name      string
		cfg       entities.TrivialConfig
		wantFound bool
	}{
		{
			name:      "should yield empty registry when Enabled=false (drift guard)",
			cfg:       entities.TrivialConfig{Enabled: false, Adapters: []string{"docs-only"}},
			wantFound: false,
		},
		{
			name:      "should yield empty registry when Adapters is empty",
			cfg:       entities.TrivialConfig{Enabled: true, Adapters: nil},
			wantFound: false,
		},
		{
			name:      "should register the configured adapters when Enabled=true and Adapters non-empty",
			cfg:       entities.TrivialConfig{Enabled: true, Adapters: []string{"docs-only"}},
			wantFound: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistryFromConfig(tc.cfg)

			// when
			_, _, found := registry.Detect(ctx, dctx)

			// then
			assert.Equal(t, tc.wantFound, found)
		})
	}
}

func TestBumpDetectorsReadVersionFileDiffs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// goRelease is a Go service's release as AutoBump cuts it in a chlog
	// repository: the changelog gains the release heading, the pending
	// fragments it compiled are deleted, and the Swagger annotation and the
	// generated docs move to the release version. overrides replaces or adds
	// diffs by path; each call builds its own fetcher.
	goRelease := func(overrides map[string]string) repositories.DetectionContext {
		diffs := map[string]string{
			"CHANGELOG.md": releaseChangelogDiff,
			"cmd/main.go": "diff --git a/cmd/main.go b/cmd/main.go\n" +
				"--- a/cmd/main.go\n" +
				"+++ b/cmd/main.go\n" +
				lineChangeDiff("// @version 1.3.0", "// @version 1.4.0"),
			"docs/docs.go":      lineChangeDiff(`	Version:          "1.3.0",`, `	Version:          "1.4.0",`),
			"docs/swagger.json": lineChangeDiff(`        "version": "1.3.0"`, `        "version": "1.4.0"`),
			"docs/swagger.yaml": lineChangeDiff("  version: 1.3.0", "  version: 1.4.0"),
		}
		maps.Copy(diffs, overrides)
		return repositories.DetectionContext{
			Files: []string{
				".changes/unreleased/1790000000000000001-aaaa.yaml",
				".changes/unreleased/1790000000000000002-bbbb.yaml",
				"cmd/main.go",
				"docs/swagger.yaml",
				"docs/docs.go",
				"docs/swagger.json",
				"CHANGELOG.md",
			},
			DeletedFiles: map[string]bool{
				".changes/unreleased/1790000000000000001-aaaa.yaml": true,
				".changes/unreleased/1790000000000000002-bbbb.yaml": true,
			},
			RepoName:        "widget-http",
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: diffs},
		}
	}

	t.Run(
		"should approve a Go release that compiles chlog fragments and moves the Swagger version",
		func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistry([]string{"bump-go"})
			dctx := goRelease(nil)

			// when
			detector, result, found := registry.Detect(ctx, dctx)

			// then
			require.True(t, found)
			assert.Equal(t, "bump-go", detector.Name())
			assert.Equal(t, "approve", result.Verdict)
			assert.Contains(t, result.Summary, "7 files")
		},
	)

	t.Run("should approve a Go release for a command under cmd/<name>/", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := repositories.DetectionContext{
			Files: []string{"CHANGELOG.md", "cmd/server/main.go"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md":       releaseChangelogDiff,
				"cmd/server/main.go": lineChangeDiff("// @version 1.3.0", "// @version 1.4.0"),
			}},
		}

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should approve a release whose version carries a v prefix", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(map[string]string{
			"docs/swagger.json": lineChangeDiff(`        "version": "v1.3.0"`, `        "version": "v1.4.0"`),
		})

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should approve a release that only compiles chlog fragments into the changelog", func(t *testing.T) {
		t.Parallel()

		// given -- no version file, so there is no diff to read
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		fragment := ".changes/unreleased/1790000000000000001-aaaa.yaml"
		dctx := repositories.DetectionContext{
			Files:        []string{fragment, "CHANGELOG.md"},
			DeletedFiles: map[string]bool{fragment: true},
		}

		// when
		_, result, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should not detect a release when a version file changes more than the version", func(t *testing.T) {
		t.Parallel()

		// given -- the annotation moves to the release, and a line of code rides along
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(map[string]string{
			"cmd/main.go": "@@ -3,3 +3,4 @@\n" +
				" context above\n" +
				"-// @version 1.3.0\n" +
				"+// @version 1.4.0\n" +
				"+import _ \"os/exec\"\n" +
				" context below\n",
		})

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release when a line changes beside its version", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(map[string]string{
			"docs/swagger.yaml": lineChangeDiff("  version: 1.3.0", "  version: 1.4.0 # see https://example.com"),
		})

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run(
		"should not detect a release when a version file moves to a version the changelog does not name",
		func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistry([]string{"bump-go"})
			dctx := goRelease(map[string]string{
				"docs/docs.go": lineChangeDiff(`	Version:          "1.3.0",`, `	Version:          "1.5.0",`),
			})

			// when
			_, _, found := registry.Detect(ctx, dctx)

			// then
			assert.False(t, found)
		},
	)

	t.Run("should not detect a release when a version file's diff cannot be read", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(nil)
		delete(dctx.FileDiffFetcher.(*doubles.StubFileDiffFetcher).Diffs, "docs/swagger.json")

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release when the changelog's diff cannot be read", func(t *testing.T) {
		t.Parallel()

		// given -- without the changelog there is no release version to check against
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(nil)
		delete(dctx.FileDiffFetcher.(*doubles.StubFileDiffFetcher).Diffs, "CHANGELOG.md")

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run(
		"should not detect a release when a version file's line moves without its version changing",
		func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistry([]string{"bump-go"})
			dctx := goRelease(map[string]string{
				"docs/swagger.yaml": lineChangeDiff("  version: 1.4.0", "  version: 1.4.0"),
			})

			// when
			_, _, found := registry.Detect(ctx, dctx)

			// then
			assert.False(t, found)
		},
	)

	t.Run("should not detect a release with version files when no diff is available", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(nil)
		dctx.FileDiffFetcher = nil

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release whose changelog gains no version heading", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(map[string]string{"CHANGELOG.md": unreleasedChangelogDiff})

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release whose changelog gains headings for two versions", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(map[string]string{
			"CHANGELOG.md": releaseChangelogDiff + "@@ -20,2 +26,3 @@\n+## [1.3.1] - 2026-01-15\n",
		})

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release that deletes a file other than a pending chlog fragment", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(nil)
		dctx.Files = append(dctx.Files, "internal/legacy.go")
		dctx.DeletedFiles["internal/legacy.go"] = true

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a release that adds a chlog fragment", func(t *testing.T) {
		t.Parallel()

		// given -- a fragment the pull request adds is a pending change, not one a release compiled
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := goRelease(nil)
		delete(dctx.DeletedFiles, ".changes/unreleased/1790000000000000002-bbbb.yaml")

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a Go release that touches a Go file AutoBump does not version", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-go"})
		dctx := repositories.DetectionContext{
			Files: []string{"CHANGELOG.md", "internal/version.go"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md":        releaseChangelogDiff,
				"internal/version.go": lineChangeDiff(`const Version = "1.3.0"`, `const Version = "1.4.0"`),
			}},
		}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a Node release whose package.json moves a dependency", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files: []string{"package.json", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": lineChangeDiff(`    "left-pad": "^2.0.0",`, `    "left-pad": "^2.0.1",`),
			}},
		}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})

	t.Run("should not detect a Node release whose package.json gains a script", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistry([]string{"bump-node"})
		dctx := repositories.DetectionContext{
			Files: []string{"package.json", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": packageJSONVersionDiff + "+  \"scripts\": { \"postinstall\": \"node fetch.js\" },\n",
			}},
		}

		// when
		_, _, found := registry.Detect(ctx, dctx)

		// then
		assert.False(t, found)
	})
}

func TestNonBumpDetectorsDeclineReleases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	unreadable := errors.New("diff unavailable")

	cases := []struct {
		name      string
		adapter   string
		files     []string
		fetcher   *doubles.StubFileDiffFetcher
		wantFound bool
	}{
		{
			name:    "should not detect a Node release as a dependency update",
			adapter: "update-node",
			files:   []string{"package.json", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": packageJSONVersionDiff,
			}},
			wantFound: false,
		},
		{
			name:    "should not detect a Node release that refreshed its lockfile as a dependency update",
			adapter: "update-node",
			files:   []string{"package.json", "yarn.lock", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
			}},
			wantFound: false,
		},
		{
			name:    "should still detect a Node dependency update that records its entry under [Unreleased]",
			adapter: "update-node",
			files:   []string{"package.json", "package-lock.json", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": unreleasedChangelogDiff,
			}},
			wantFound: true,
		},
		{
			name:    "should not detect a Python release as a dependency update",
			adapter: "update-python",
			files:   []string{"pyproject.toml", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
			}},
			wantFound: false,
		},
		{
			name:      "should not detect a dependency update whose changelog diff cannot be read",
			adapter:   "update-go",
			files:     []string{"go.mod", "go.sum", "CHANGELOG.md"},
			fetcher:   &doubles.StubFileDiffFetcher{Err: unreadable},
			wantFound: false,
		},
		{
			name:      "should detect a dependency update that leaves the changelog alone without reading a diff",
			adapter:   "update-go",
			files:     []string{"go.mod", "go.sum"},
			fetcher:   &doubles.StubFileDiffFetcher{Err: unreadable},
			wantFound: true,
		},
		{
			name:    "should not detect a release that also edits documentation as docs-only",
			adapter: "docs-only",
			files:   []string{"README.md", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
			}},
			wantFound: false,
		},
		{
			name:    "should still detect docs-only when the changelog gains no release",
			adapter: "docs-only",
			files:   []string{"README.md", "CHANGELOG.md"},
			fetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": unreleasedChangelogDiff,
			}},
			wantFound: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistry([]string{tc.adapter})
			dctx := repositories.DetectionContext{Files: tc.files, FileDiffFetcher: tc.fetcher}

			// when
			_, _, found := registry.Detect(ctx, dctx)

			// then
			assert.Equal(t, tc.wantFound, found)
			if !slices.Contains(tc.files, "CHANGELOG.md") {
				assert.Empty(t, tc.fetcher.Requested, "a change without a changelog has no release heading to look for")
			}
		})
	}
}

func TestNewDetectorRegistryFromConfigRunsApproveOnlyAdaptersFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("should settle a pull request both kinds claim with the approve-only adapter", func(t *testing.T) {
		t.Parallel()

		// given -- a CHANGELOG-only release matches bump-go and bump-node alike
		registry := trivial.NewDetectorRegistryFromConfig(entities.TrivialConfig{
			Enabled:             true,
			Adapters:            []string{"bump-go"},
			ApproveOnlyAdapters: []string{"bump-node"},
		})
		dctx := repositories.DetectionContext{Files: []string{"CHANGELOG.md"}}

		// when
		detector, _, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "bump-node", detector.Name())
	})

	t.Run("should settle a Node release with bump-node and a dependency update with update-node", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistryFromConfig(entities.TrivialConfig{
			Enabled:             true,
			Adapters:            []string{"update-node"},
			ApproveOnlyAdapters: []string{"bump-node"},
		})
		release := repositories.DetectionContext{
			Files: []string{"package.json", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": releaseChangelogDiff,
				"package.json": packageJSONVersionDiff,
			}},
		}
		update := repositories.DetectionContext{
			Files: []string{"package.json", "package-lock.json", "CHANGELOG.md"},
			FileDiffFetcher: &doubles.StubFileDiffFetcher{Diffs: map[string]string{
				"CHANGELOG.md": unreleasedChangelogDiff,
			}},
		}

		// when
		releaseDetector, _, releaseFound := registry.Detect(ctx, release)
		updateDetector, _, updateFound := registry.Detect(ctx, update)

		// then
		require.True(t, releaseFound)
		assert.Equal(t, "bump-node", releaseDetector.Name())
		require.True(t, updateFound)
		assert.Equal(t, "update-node", updateDetector.Name())
	})

	t.Run("should register the approve-only adapters when no other adapter is listed", func(t *testing.T) {
		t.Parallel()

		// given
		registry := trivial.NewDetectorRegistryFromConfig(entities.TrivialConfig{
			Enabled:             true,
			ApproveOnlyAdapters: []string{"docs-only"},
		})
		dctx := repositories.DetectionContext{Files: []string{"README.md"}}

		// when
		detector, _, found := registry.Detect(ctx, dctx)

		// then
		require.True(t, found)
		assert.Equal(t, "docs-only", detector.Name())
	})
}

// releaseChangelogDiff is the CHANGELOG.md diff of a release that compiles
// its pending entries under a heading for 1.4.0.
const releaseChangelogDiff = "@@ -1,6 +1,12 @@\n" +
	" # Changelog\n" +
	" \n" +
	" ## [Unreleased]\n" +
	" \n" +
	"+## [1.4.0] - 2026-01-31\n" +
	"+\n" +
	"+### Added\n" +
	"+\n" +
	"+- added the widget endpoint\n" +
	"+\n" +
	" ## [1.3.0] - 2026-01-01\n"

// unreleasedChangelogDiff is the CHANGELOG.md diff of a change that records
// its entry under [Unreleased] without cutting a release, as a dependency
// update does.
const unreleasedChangelogDiff = "@@ -3,4 +3,5 @@\n" +
	" ## [Unreleased]\n" +
	" \n" +
	" ### Changed\n" +
	" \n" +
	"+- changed the widget dependency to its latest release\n"

// packageJSONVersionDiff moves a package.json's own version to the release.
const packageJSONVersionDiff = "@@ -1,4 +1,4 @@\n" +
	" {\n" +
	"   \"name\": \"widget\",\n" +
	"-  \"version\": \"1.3.0\",\n" +
	"+  \"version\": \"1.4.0\",\n" +
	"   \"private\": true,\n"

// pythonInitVersionDiff moves a package's __version__ to the release.
const pythonInitVersionDiff = "@@ -1,3 +1,3 @@\n" +
	" \"\"\"The widget package.\"\"\"\n" +
	" \n" +
	"-__version__ = \"1.3.0\"\n" +
	"+__version__ = \"1.4.0\"\n"

// lineChangeDiff is the diff of a file whose one line before became after.
func lineChangeDiff(before, after string) string {
	return "@@ -3,3 +3,3 @@\n" +
		" context above\n" +
		"-" + before + "\n" +
		"+" + after + "\n" +
		" context below\n"
}

func TestDependencyUpdateDetectorsAcceptPendingChlogFragments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fragment := ".changes/unreleased/1790000000000000001-aaaa.yaml"
	otherFragment := ".changes/unreleased/1790000000000000002-bbbb.yaml"

	cases := []struct {
		name      string
		adapter   string
		files     []string
		deleted   map[string]bool
		wantFound bool
	}{
		{
			name:      "should detect a Go dependency update that records itself in a chlog fragment",
			adapter:   "update-go",
			files:     []string{fragment, "go.mod", "go.sum"},
			wantFound: true,
		},
		{
			name:      "should detect a Go dependency update of a module in a subdirectory",
			adapter:   "update-go",
			files:     []string{fragment, "services/api/go.mod", "services/api/go.sum"},
			wantFound: true,
		},
		{
			name:      "should detect a Go dependency update that edits the workspace sums",
			adapter:   "update-go",
			files:     []string{fragment, "go.mod", "go.sum", "go.work.sum"},
			wantFound: true,
		},
		{
			name:      "should detect a Node dependency update that edits two pending fragments",
			adapter:   "update-node",
			files:     []string{fragment, otherFragment, "package.json"},
			wantFound: true,
		},
		{
			name:      "should detect a Python dependency update that edits the PDM lock file",
			adapter:   "update-python",
			files:     []string{fragment, "pdm.lock", "pyproject.toml"},
			wantFound: true,
		},
		{
			name:      "should detect a Python dependency update that only refreshes the lock file",
			adapter:   "update-python",
			files:     []string{"pdm.lock"},
			wantFound: true,
		},
		{
			name:      "should not detect a pull request that only adds a chlog fragment",
			adapter:   "update-go",
			files:     []string{fragment},
			wantFound: false,
		},
		{
			name:      "should not detect a pull request that only edits changelog records",
			adapter:   "update-go",
			files:     []string{fragment, "CHANGELOG.md"},
			wantFound: false,
		},
		{
			name:      "should not detect a pull request that deletes a chlog fragment, which only a release does",
			adapter:   "update-go",
			files:     []string{fragment, "go.mod", "go.sum"},
			deleted:   map[string]bool{fragment: true},
			wantFound: false,
		},
		{
			name:      "should not take a file nested under the fragment directory for a fragment",
			adapter:   "update-go",
			files:     []string{".changes/unreleased/scripts/fetch.yaml", "go.mod", "go.sum"},
			wantFound: false,
		},
		{
			name:      "should not take a non-YAML file in the fragment directory for a fragment",
			adapter:   "update-go",
			files:     []string{".changes/unreleased/notes.md", "go.mod", "go.sum"},
			wantFound: false,
		},
		{
			name:      "should not detect a dependency update that also changes code",
			adapter:   "update-go",
			files:     []string{fragment, "go.mod", "go.sum", "internal/client.go"},
			wantFound: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistry([]string{tc.adapter})
			dctx := repositories.DetectionContext{Files: tc.files, DeletedFiles: tc.deleted}

			// when
			detector, result, found := registry.Detect(ctx, dctx)

			// then
			require.Equal(t, tc.wantFound, found)
			if tc.wantFound {
				assert.Equal(t, tc.adapter, detector.Name())
				assert.Equal(t, "approve", result.Verdict)
			}
		})
	}

	t.Run(
		"should leave a dependency update of a chlog repository to update-go behind the approve-only bump-go",
		func(t *testing.T) {
			t.Parallel()

			// given
			registry := trivial.NewDetectorRegistryFromConfig(entities.TrivialConfig{
				Enabled:             true,
				Adapters:            []string{"update-go"},
				ApproveOnlyAdapters: []string{"bump-go"},
			})
			dctx := repositories.DetectionContext{Files: []string{fragment, "go.mod", "go.sum"}}

			// when
			detector, _, found := registry.Detect(ctx, dctx)

			// then
			require.True(t, found)
			assert.Equal(
				t,
				"update-go",
				detector.Name(),
				"an added fragment is a pending change, not one a release compiled",
			)
		},
	)
}
