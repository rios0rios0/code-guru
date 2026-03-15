//go:build unit

package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestParseReviewResponse(t *testing.T) {
	t.Parallel()

	t.Run("should parse valid JSON with verdict and comments", func(t *testing.T) {
		// given
		content := `{"verdict":"approve","summary":"Looks good.","comments":[]}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "approve", result.Verdict)
		assert.Equal(t, "Looks good.", result.Summary)
		assert.Empty(t, result.Comments)
	})

	t.Run("should default verdict to comment when missing", func(t *testing.T) {
		// given
		content := `{"summary":"No issues found.","comments":[]}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "comment", result.Verdict)
	})

	t.Run("should default verdict to comment when invalid value", func(t *testing.T) {
		// given
		content := `{"verdict":"unknown_value","summary":"test","comments":[]}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "comment", result.Verdict)
	})

	t.Run("should parse JSON from markdown code fences", func(t *testing.T) {
		// given
		content := "Some text\n```json\n{\"verdict\":\"request_changes\",\"summary\":\"Issues found.\",\"comments\":[]}\n```\nMore text"

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "request_changes", result.Verdict)
		assert.Equal(t, "Issues found.", result.Summary)
	})

	t.Run("should parse comments with all fields", func(t *testing.T) {
		// given
		content := `{
			"verdict": "request_changes",
			"summary": "Found issues",
			"comments": [
				{
					"file": "main.go",
					"line": 42,
					"end_line": 45,
					"severity": "error",
					"body": "Missing error handling",
					"suggestion": "if err != nil { return err }"
				}
			]
		}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "request_changes", result.Verdict)
		require.Len(t, result.Comments, 1)
		assert.Equal(t, "main.go", result.Comments[0].FilePath)
		assert.Equal(t, 42, result.Comments[0].Line)
		assert.Equal(t, 45, result.Comments[0].EndLine)
		assert.Equal(t, "error", result.Comments[0].Severity)
		assert.Equal(t, "Missing error handling", result.Comments[0].Body)
		assert.Equal(t, "if err != nil { return err }", result.Comments[0].Suggestion)
	})

	t.Run("should fall back to plain text summary when not JSON", func(t *testing.T) {
		// given
		content := "This is not JSON at all, just a plain text response."

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "comment", result.Verdict)
		assert.Equal(t, content, result.Summary)
		assert.Empty(t, result.Comments)
	})
}
