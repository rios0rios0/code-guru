//go:build unit

package autobump_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial/autobump"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	t.Run("should parse valid YAML with multiple languages", func(t *testing.T) {
		// given
		content := `
languages:
  go:
    version_files: []
  python:
    version_files:
      - path: "{project_name}/__init__.py"
  typescript:
    version_files:
      - path: "package.json"
`

		// when
		cfg, err := autobump.ParseConfig(content)

		// then
		require.NoError(t, err)
		assert.Len(t, cfg.Languages, 3)
		assert.Empty(t, cfg.Languages["go"].VersionFiles)
		assert.Len(t, cfg.Languages["python"].VersionFiles, 1)
		assert.Equal(t, "{project_name}/__init__.py", cfg.Languages["python"].VersionFiles[0].Path)
		assert.Len(t, cfg.Languages["typescript"].VersionFiles, 1)
	})

	t.Run("should return error for invalid YAML", func(t *testing.T) {
		// given
		content := `languages: [invalid`

		// when
		cfg, err := autobump.ParseConfig(content)

		// then
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})
}

func TestResolveVersionFilePaths(t *testing.T) {
	t.Parallel()

	t.Run("should resolve {project_name} placeholder", func(t *testing.T) {
		// given
		cfg := &autobump.Config{
			Languages: map[string]autobump.LanguageConfig{
				"python": {
					VersionFiles: []autobump.VersionFile{
						{Path: "{project_name}/__init__.py"},
					},
				},
			},
		}

		// when
		paths := autobump.ResolveVersionFilePaths(cfg, "python", "mypackage")

		// then
		assert.Equal(t, []string{"mypackage/__init__.py"}, paths)
	})

	t.Run("should return nil for missing language", func(t *testing.T) {
		// given
		cfg := &autobump.Config{
			Languages: map[string]autobump.LanguageConfig{
				"go": {VersionFiles: nil},
			},
		}

		// when
		paths := autobump.ResolveVersionFilePaths(cfg, "python", "mypackage")

		// then
		assert.Nil(t, paths)
	})

	t.Run("should return empty slice for language with no version files", func(t *testing.T) {
		// given
		cfg := &autobump.Config{
			Languages: map[string]autobump.LanguageConfig{
				"go": {VersionFiles: []autobump.VersionFile{}},
			},
		}

		// when
		paths := autobump.ResolveVersionFilePaths(cfg, "go", "myrepo")

		// then
		assert.Empty(t, paths)
	})

	t.Run("should return nil for nil config", func(t *testing.T) {
		// given / when
		paths := autobump.ResolveVersionFilePaths(nil, "go", "myrepo")

		// then
		assert.Nil(t, paths)
	})

	t.Run("should resolve multiple version files", func(t *testing.T) {
		// given
		cfg := &autobump.Config{
			Languages: map[string]autobump.LanguageConfig{
				"java": {
					VersionFiles: []autobump.VersionFile{
						{Path: "build.gradle"},
						{Path: "pom.xml"},
					},
				},
			},
		}

		// when
		paths := autobump.ResolveVersionFilePaths(cfg, "java", "myapp")

		// then
		assert.Equal(t, []string{"build.gradle", "pom.xml"}, paths)
	})
}
