//go:build unit

package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	openaiRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/openai"
)

func TestParseReviewResponse(t *testing.T) {
	t.Parallel()

	t.Run("should parse valid JSON response", func(t *testing.T) {
		// given
		content := `{"summary": "no issues", "comments": [{"file": "app.go", "line": 3, "body": "test", "severity": "info"}]}`

		// when
		result, err := openaiRepo.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "no issues", result.Summary)
		assert.Len(t, result.Comments, 1)
		assert.Equal(t, "app.go", result.Comments[0].FilePath)
	})

	t.Run("should parse JSON from markdown code fence", func(t *testing.T) {
		// given
		content := "```json\n{\"summary\": \"fenced\", \"comments\": []}\n```"

		// when
		result, err := openaiRepo.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fenced", result.Summary)
	})

	t.Run("should fallback to plain text summary", func(t *testing.T) {
		// given
		content := "This PR looks fine to me."

		// when
		result, err := openaiRepo.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, content, result.Summary)
		assert.Empty(t, result.Comments)
	})
}
