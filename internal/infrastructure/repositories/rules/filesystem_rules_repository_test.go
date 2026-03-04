//go:build unit

package rules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/rules"
)

func TestLoadAll(t *testing.T) {
	t.Parallel()

	t.Run("should load markdown rules from directory", func(t *testing.T) {
		// given
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "security.md"), []byte("# Security\nDo not expose secrets."), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "golang.md"), []byte("# Go\nUse gofmt."), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, nil)

		// when
		result, err := repo.LoadAll()

		// then
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("should strip YAML frontmatter", func(t *testing.T) {
		// given
		dir := t.TempDir()
		content := "---\npaths:\n  - \"**/*.go\"\n---\n# Go Rules\nUse gofmt."
		require.NoError(t, os.WriteFile(filepath.Join(dir, "golang.md"), []byte(content), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, nil)

		// when
		result, err := repo.LoadAll()

		// then
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "# Go Rules\nUse gofmt.", result[0].Content)
		assert.Equal(t, []string{"**/*.go"}, result[0].FileGlobs)
	})

	t.Run("should skip non-markdown files", func(t *testing.T) {
		// given
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a rule"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "valid.md"), []byte("a rule"), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, nil)

		// when
		result, err := repo.LoadAll()

		// then
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "valid", result[0].Name)
	})

	t.Run("should return nil for empty path", func(t *testing.T) {
		// given
		repo := rules.NewFilesystemRulesRepository("", nil)

		// when
		result, err := repo.LoadAll()

		// then
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("should filter by categories when configured", func(t *testing.T) {
		// given
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "security.md"), []byte("sec rules"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "golang.md"), []byte("go rules"), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, []string{"security"})

		// when
		result, err := repo.LoadAll()

		// then
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "security", result[0].Name)
	})
}

func TestLoadForLanguages(t *testing.T) {
	t.Parallel()

	t.Run("should include universal and language-specific rules", func(t *testing.T) {
		// given
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "security.md"), []byte("sec"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "golang.md"), []byte("go"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "python.md"), []byte("py"), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, nil)

		// when
		result, err := repo.LoadForLanguages([]string{"golang"}, []string{"main.go"})

		// then
		require.NoError(t, err)
		names := make([]string, len(result))
		for i, r := range result {
			names[i] = r.Name
		}
		assert.Contains(t, names, "security") // universal
		assert.Contains(t, names, "golang")   // language match
		assert.NotContains(t, names, "python") // not requested
	})

	t.Run("should include rules matching file globs", func(t *testing.T) {
		// given
		dir := t.TempDir()
		content := "---\npaths:\n  - \"**/*.go\"\n---\ncustom go rules"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "custom-go.md"), []byte(content), 0o644))
		repo := rules.NewFilesystemRulesRepository(dir, nil)

		// when
		result, err := repo.LoadForLanguages([]string{}, []string{"internal/main.go"})

		// then
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "custom-go", result[0].Name)
	})
}
