//go:build unit

package claude_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	"github.com/rios0rios0/codeguru/internal/support"
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

	t.Run("should return ErrUnparseableResponse when CLI result is plain text", func(t *testing.T) {
		// given: the parser refuses to fabricate a `Summary: content` result
		// because the command layer would otherwise post the raw model output
		// straight onto the PR. See `internal/support/response_parser.go`.
		content := "I couldn't find any issues with this PR."
		cliResp := map[string]string{"result": content}
		output, _ := json.Marshal(cliResp)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.Error(t, err)
		require.ErrorIs(t, err, support.ErrUnparseableResponse)
		assert.Nil(t, result)
	})

	t.Run("should repair unescaped quotes inside string values", func(t *testing.T) {
		// given: the canonical LLM failure observed on
		// `internal/auth-service#NNNN` — the `body` field contains an
		// unescaped quoted phrase. The repair pass escapes the inner quotes
		// so the parse succeeds.
		innerJSON := `{"verdict":"comment","summary":"ok","comments":[{"file":"a.go","line":1,"severity":"info","body":"Rule: "Always escape quotes" applies"}]}`
		cliResp := map[string]string{"result": innerJSON}
		output, _ := json.Marshal(cliResp)

		// when
		result, err := claude.ParseClaudeResponse(output)

		// then
		require.NoError(t, err)
		require.Len(t, result.Comments, 1)
		assert.Equal(t, `Rule: "Always escape quotes" applies`, result.Comments[0].Body)
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
