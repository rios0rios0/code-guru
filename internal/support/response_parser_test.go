//go:build unit

package support_test

import (
	"fmt"
	"testing"

	logger "github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
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

	t.Run("should return ErrUnparseableResponse when content is not JSON", func(t *testing.T) {
		// given: previous behaviour was to return a `ReviewResult{Summary: content}`,
		// which the command layer then posted verbatim as a PR thread — exactly the
		// "raw JSON dumped onto the PR" symptom this fix targets. The parser now
		// refuses to fabricate a result; the worker logs the failure and posts
		// nothing.
		content := "This is not JSON at all, just a plain text response."

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.Error(t, err)
		require.ErrorIs(t, err, support.ErrUnparseableResponse)
		assert.Nil(t, result)
	})

	t.Run("should repair unescaped quotes inside string values (canonical LLM failure)", func(t *testing.T) {
		// given: this is the exact failure observed on backend/authenticator#12027
		// thread 71418 — the model embedded an unescaped quoted phrase ("Always
		// use ...") inside a `body` string. `json.Unmarshal` rejects it; the
		// repair pass escapes the unescaped quotes so the parse succeeds.
		content := `{
			"verdict": "request_changes",
			"summary": "Found issues",
			"comments": [
				{
					"file": "main.go",
					"line": 10,
					"severity": "warning",
					"body": "Logging rule: "Always use WithFields" applies here."
				}
			]
		}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, "request_changes", result.Verdict)
		require.Len(t, result.Comments, 1)
		assert.Equal(
			t,
			`Logging rule: "Always use WithFields" applies here.`,
			result.Comments[0].Body,
			"the inner quoted phrase must round-trip into the parsed body",
		)
	})

	t.Run("should leave already-escaped quotes untouched after repair", func(t *testing.T) {
		// given: the input is valid JSON. Repair should be a no-op and the
		// original `\"` sequences must reach the result unchanged.
		content := `{"verdict":"comment","summary":"He said \"hi\".","comments":[]}`

		// when
		result, err := support.ParseReviewResponse(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, `He said "hi".`, result.Summary)
	})

	t.Run("should not log raw content at ERROR level on parse failure", func(t *testing.T) {
		// given: the model occasionally echoes pieces of the prompt back, and
		// the prompt embeds the full PR diff — so a default-on raw log would
		// leak repository source / in-diff secrets into shared log stores.
		// The ERROR entry must carry only metadata; the raw content must be
		// gated behind DEBUG.
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()
		logger.StandardLogger().SetLevel(logger.DebugLevel)

		marker := "API_KEY-doNotLeakAtErrorLevel-XYZ123"
		payload := "definitely garbage " + marker + " not JSON"

		// when
		_, err := support.ParseReviewResponse(payload)

		// then
		require.ErrorIs(t, err, support.ErrUnparseableResponse)

		var errorEntry, debugEntry *logger.Entry
		for _, entry := range hook.AllEntries() {
			//nolint:exhaustive // only ERROR + DEBUG matter for this assertion
			switch entry.Level {
			case logger.ErrorLevel:
				errorEntry = entry
			case logger.DebugLevel:
				debugEntry = entry
			}
		}

		require.NotNil(t, errorEntry, "expected an ERROR-level entry on parse failure")
		assert.NotContains(
			t, fmt.Sprintf("%s %v", errorEntry.Message, errorEntry.Data), marker,
			"raw content must not appear at ERROR level (data leak)",
		)
		assert.Contains(t, errorEntry.Data, "length", "ERROR entry must carry the length metadata")
		assert.Contains(t, errorEntry.Data, "fingerprint", "ERROR entry must carry the fingerprint metadata")

		require.NotNil(t, debugEntry, "expected a DEBUG-level entry with the raw response gated behind verbose logging")
		assert.Contains(
			t, fmt.Sprint(debugEntry.Data), marker,
			"DEBUG entry should carry the raw content so operators can opt in for diagnosis",
		)
	})

	t.Run("should emit a stable fingerprint for the same input across calls", func(t *testing.T) {
		// given: operators correlate failures by greppping the fingerprint
		// across pods. Two failures of the same model output must produce
		// identical fingerprints; different inputs must differ. The hook
		// captures the structured fields directly so we never have to
		// re-parse a log line.
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()
		logger.StandardLogger().SetLevel(logger.DebugLevel)

		// when
		_, _ = support.ParseReviewResponse("garbage one")
		_, _ = support.ParseReviewResponse("garbage one")
		_, _ = support.ParseReviewResponse("garbage two")

		// then
		var fingerprints []string
		for _, entry := range hook.AllEntries() {
			if entry.Level != logger.ErrorLevel {
				continue
			}

			fp, ok := entry.Data["fingerprint"].(string)
			if !ok {
				continue
			}

			fingerprints = append(fingerprints, fp)
		}

		require.Len(t, fingerprints, 3, "expected three ERROR entries (one per call)")
		assert.Equal(t, fingerprints[0], fingerprints[1], "same input must yield the same fingerprint")
		assert.NotEqual(t, fingerprints[0], fingerprints[2], "different inputs must yield different fingerprints")
	})
}
