//go:build unit

package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestClassifyFile(t *testing.T) {
	t.Parallel()

	t.Run("should classify Go files", func(t *testing.T) {
		// given
		path := "internal/main.go"

		// when
		result := support.ClassifyFile(path)

		// then
		assert.Equal(t, "golang", result)
	})

	t.Run("should classify JavaScript files", func(t *testing.T) {
		// given
		path := "src/index.ts"

		// when
		result := support.ClassifyFile(path)

		// then
		assert.Equal(t, "javascript", result)
	})

	t.Run("should classify Python files", func(t *testing.T) {
		// given
		path := "app.py"

		// when
		result := support.ClassifyFile(path)

		// then
		assert.Equal(t, "python", result)
	})

	t.Run("should classify YAML files", func(t *testing.T) {
		// given
		path := "config.yaml"

		// when
		result := support.ClassifyFile(path)

		// then
		assert.Equal(t, "yaml", result)
	})

	t.Run("should return empty for unknown extensions", func(t *testing.T) {
		// given
		path := "notes.txt"

		// when
		result := support.ClassifyFile(path)

		// then
		assert.Equal(t, "", result)
	})
}

func TestClassifyFiles(t *testing.T) {
	t.Parallel()

	t.Run("should return unique languages", func(t *testing.T) {
		// given
		paths := []string{"a.go", "b.go", "c.py", "d.ts"}

		// when
		result := support.ClassifyFiles(paths)

		// then
		assert.Len(t, result, 3)
		assert.Contains(t, result, "golang")
		assert.Contains(t, result, "python")
		assert.Contains(t, result, "javascript")
	})

	t.Run("should skip unknown files", func(t *testing.T) {
		// given
		paths := []string{"a.txt", "b.md"}

		// when
		result := support.ClassifyFiles(paths)

		// then
		assert.Empty(t, result)
	})
}
