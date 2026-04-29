//go:build unit

package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestSplitUnifiedDiff(t *testing.T) {
	t.Parallel()

	t.Run("should split multi-file diff into per-file chunks", func(t *testing.T) {
		// given
		fullDiff := `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
diff --git a/util.go b/util.go
index 123..456 100644
--- a/util.go
+++ b/util.go
@@ -5,2 +5,3 @@
 func helper() {
+	return nil
 }`

		// when
		result := support.SplitUnifiedDiff(fullDiff)

		// then
		assert.Len(t, result, 2)
		assert.Contains(t, result["main.go"], "+import \"fmt\"")
		assert.Contains(t, result["util.go"], "+\treturn nil")
	})

	t.Run("should handle single-file diff", func(t *testing.T) {
		// given
		fullDiff := `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -1 +1,2 @@
 package app
+var x = 1`

		// when
		result := support.SplitUnifiedDiff(fullDiff)

		// then
		assert.Len(t, result, 1)
		assert.Contains(t, result["app.go"], "+var x = 1")
	})

	t.Run("should return empty map for empty diff", func(t *testing.T) {
		// given
		fullDiff := ""

		// when
		result := support.SplitUnifiedDiff(fullDiff)

		// then
		assert.Empty(t, result)
	})
}

func TestLookupChunkByPath(t *testing.T) {
	t.Parallel()

	t.Run("should find chunk when caller path matches the bare key", func(t *testing.T) {
		// given
		chunks := map[string]string{"README.md": "diff body"}

		// when
		chunk, ok := support.LookupChunkByPath(chunks, "README.md")

		// then
		assert.True(t, ok)
		assert.Equal(t, "diff body", chunk)
	})

	t.Run("should find chunk when caller path has a leading slash (Azure DevOps shape)", func(t *testing.T) {
		// given: SplitUnifiedDiff keys chunks by the bare new-side path, but
		// Azure DevOps's GetPullRequestFiles returns paths like "/README.md".
		// Without normalisation the lookup would always miss for ADO PRs and
		// the AI would receive an empty diff under each file header.
		chunks := map[string]string{"README.md": "diff body"}

		// when
		chunk, ok := support.LookupChunkByPath(chunks, "/README.md")

		// then
		assert.True(t, ok)
		assert.Equal(t, "diff body", chunk)
	})

	t.Run("should return false when the path does not exist in the chunks", func(t *testing.T) {
		// given
		chunks := map[string]string{"README.md": "diff body"}

		// when
		chunk, ok := support.LookupChunkByPath(chunks, "missing.go")

		// then
		assert.False(t, ok)
		assert.Empty(t, chunk)
	})

	t.Run("should preserve nested path segments after the leading slash", func(t *testing.T) {
		// given
		chunks := map[string]string{"src/util/helper.go": "diff body"}

		// when
		chunk, ok := support.LookupChunkByPath(chunks, "/src/util/helper.go")

		// then
		assert.True(t, ok)
		assert.Equal(t, "diff body", chunk)
	})
}
