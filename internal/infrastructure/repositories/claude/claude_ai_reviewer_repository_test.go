//go:build unit

package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	"github.com/rios0rios0/codeguru/internal/support"
	entitybuilders "github.com/rios0rios0/codeguru/test/domain/entitybuilders"
)

// writeFakeClaudeBinary drops a tiny `/bin/sh` script in `t.TempDir()`
// that mimics the real Claude CLI failure surface: it consumes stdin,
// optionally prints `$FAKE_STDOUT` to fd 1 and `$FAKE_STDERR` to fd 2,
// then exits with `$FAKE_EXIT`. Tests configure the env vars via
// `t.Setenv` (which means they cannot run with `t.Parallel`, since
// process-wide env mutation conflicts with parallel siblings — that
// trade-off is acceptable here because the tests are fast and the
// regression they pin is critical: every claude crash since the
// repository was written has been logged as `(stderr: )` because the
// wrapper threw away stdout, and the fake binary is the only way to
// drive the real `ReviewDiff` end-to-end without an actual `claude`
// install.
func writeFakeClaudeBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary uses /bin/sh; not portable to Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	body := `#!/bin/sh
cat > /dev/null
[ -n "$FAKE_STDOUT" ] && printf '%s' "$FAKE_STDOUT"
[ -n "$FAKE_STDERR" ] && printf '%s' "$FAKE_STDERR" >&2
exit "${FAKE_EXIT:-0}"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755)) //nolint:gosec // executable test fixture
	return path
}

// minimalReviewRequest constructs a request that exercises the prompt
// build path without bloating the test diff. The diff value itself does
// not matter — the fake binary discards stdin — but the entity must be
// well-formed so the wrapper's prompt construction does not short-circuit.
func minimalReviewRequest() entities.ReviewRequest {
	return entities.ReviewRequest{
		PullRequest: forgeEntities.PullRequestDetail{
			PullRequest:  forgeEntities.PullRequest{Title: "feat: smoke"},
			SourceBranch: "feat/x",
			TargetBranch: "main",
		},
		Diffs: []entities.FileDiff{
			{Path: "main.go", Diff: "@@ -1 +1 @@\n-old\n+new\n", Language: "go"},
		},
	}
}

func TestClaudeReviewer_ReviewDiff_FailureCapturesBothStreams(t *testing.T) {
	// `t.Setenv` panics under `t.Parallel`, and we control the failure
	// shape via env, so this group runs serially. The trade-off is
	// pinned in the writeFakeClaudeBinary doc above.

	t.Run("should include stdout in the error when claude exits non-zero with a JSON error envelope on stdout", func(t *testing.T) {
		// given: the canonical Anthropic CLI failure shape — a JSON
		// envelope on stdout (per `--output-format json`) plus a small
		// auxiliary message on stderr. Captured live across PRs
		// `#NNNN`, `#NNNN`, `#NNNN`, `#NNNN`, `#NNNN` on
		// `2026-05-01`, where every error line in production logs
		// showed `(stderr: )` because the wrapper at
		// `claude_ai_reviewer_repository.go:88` threw away stdout —
		// hiding the only diagnostic the CLI actually produced.
		bin := writeFakeClaudeBinary(t)
		t.Setenv("FAKE_STDOUT", `{"error":"rate_limit_exceeded","message":"too many requests"}`)
		t.Setenv("FAKE_STDERR", "auxiliary stderr context")
		t.Setenv("FAKE_EXIT", "1")
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		result, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.Error(t, err)
		assert.Nil(t, result)
		errMsg := err.Error()
		assert.Contains(t, errMsg, "rate_limit_exceeded",
			"stdout payload must reach the operator log so the failure is debuggable")
		assert.Contains(t, errMsg, "auxiliary stderr context",
			"stderr payload must keep being captured (no regression)")
		assert.Contains(t, errMsg, "exit status 1",
			"the wrapped error must keep the underlying exec.ExitError text")
	})

	t.Run("should include stderr alone when claude exits non-zero with no stdout output", func(t *testing.T) {
		// given: defensive — some claude failure modes only print to
		// stderr (e.g. invalid CLI args). The pre-existing stderr
		// capture must keep working; this is the negative-of-negative
		// test that would catch a regression in the new capture path.
		bin := writeFakeClaudeBinary(t)
		t.Setenv("FAKE_STDOUT", "")
		t.Setenv("FAKE_STDERR", "Error: --max-turns must be > 0")
		t.Setenv("FAKE_EXIT", "2")
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--max-turns must be > 0")
		assert.Contains(t, err.Error(), "exit status 2")
	})

	t.Run("should truncate captured streams to the documented cap so the error line stays bounded", func(t *testing.T) {
		// given: an oversized stdout (8 KB of `A`s, twice the 4 KB cap).
		// `support.TruncateForLog` quotes the value with `strconv.Quote`
		// and ends with a `…(truncated)` sentinel; pin both halves of
		// the contract so a future "let me un-truncate to make
		// debugging easier" refactor surfaces in the test before it
		// floods the log pipeline.
		bin := writeFakeClaudeBinary(t)
		oversized := strings.Repeat("A", 8192)
		t.Setenv("FAKE_STDOUT", oversized)
		t.Setenv("FAKE_STDERR", "")
		t.Setenv("FAKE_EXIT", "1")
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.Error(t, err)
		errMsg := err.Error()
		assert.Less(t, len(errMsg), 9000,
			"the error message must not echo the full 8 KB stdout — the truncation cap is the whole point")
		assert.Contains(t, errMsg, "AAAA",
			"some of the oversized stdout must still surface (truncate, not drop)")
	})
}

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
