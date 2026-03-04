//go:build unit

package claude_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
)

func TestParseClaudeResponse(t *testing.T) {
	t.Parallel()

	t.Run("should parse direct JSON from CLI output", func(t *testing.T) {
		// given
		review := entities.ReviewResult{
			Summary:  "looks good",
			Comments: []entities.ReviewComment{{FilePath: "main.go", Line: 10, Body: "fix this", Severity: "error"}},
		}
		innerJSON, _ := json.Marshal(review)
		cliResp := map[string]string{"result": string(innerJSON)}
		output, _ := json.Marshal(cliResp)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		assert.Equal(t, "looks good", result.Summary)
		assert.Len(t, result.Comments, 1)
		assert.Equal(t, "main.go", result.Comments[0].FilePath)
	})

	t.Run("should parse JSON wrapped in markdown code fence", func(t *testing.T) {
		// given
		review := `{"summary": "all good", "comments": []}`
		content := "Here is my review:\n```json\n" + review + "\n```\n"
		cliResp := map[string]string{"result": content}
		output, _ := json.Marshal(cliResp)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		assert.Equal(t, "all good", result.Summary)
		assert.Empty(t, result.Comments)
	})

	t.Run("should fallback to plain text summary", func(t *testing.T) {
		// given
		content := "I couldn't find any issues with this PR."
		cliResp := map[string]string{"result": content}
		output, _ := json.Marshal(cliResp)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		assert.Equal(t, content, result.Summary)
		assert.Empty(t, result.Comments)
	})

	t.Run("should handle raw JSON output without CLI wrapper", func(t *testing.T) {
		// given
		review := entities.ReviewResult{
			Summary: "raw output",
			Comments: []entities.ReviewComment{
				{FilePath: "test.go", Line: 5, Body: "nit", Severity: "info"},
			},
		}
		output, _ := json.Marshal(review)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		assert.Equal(t, "raw output", result.Summary)
		assert.Len(t, result.Comments, 1)
	})
}
