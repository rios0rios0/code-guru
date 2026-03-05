//go:build unit

package claude_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	entitybuilders "github.com/rios0rios0/codeguru/test/domain/entitybuilders"
)

func TestParseClaudeResponse(t *testing.T) {
	t.Parallel()

	t.Run("should parse direct JSON from CLI output", func(t *testing.T) {
		// given
		review := entitybuilders.NewReviewResultBuilder().
			WithSummary("looks good").
			WithComments([]entities.ReviewComment{
				entitybuilders.NewReviewCommentBuilder().
					WithFilePath("main.go").
					WithLine(10).
					WithBody("fix this").
					WithSeverity("error").
					BuildReviewComment(),
			}).
			BuildReviewResult()
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
		review := entitybuilders.NewReviewResultBuilder().
			WithSummary("raw output").
			WithComments([]entities.ReviewComment{
				entitybuilders.NewReviewCommentBuilder().
					WithFilePath("test.go").
					WithLine(5).
					WithBody("nit").
					WithSeverity("info").
					BuildReviewComment(),
			}).
			BuildReviewResult()
		output, _ := json.Marshal(review)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		assert.Equal(t, "raw output", result.Summary)
		assert.Len(t, result.Comments, 1)
	})
}
