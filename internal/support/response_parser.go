package support

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// jsonCodeBlockPattern matches content inside markdown code fences.
var jsonCodeBlockPattern = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.+?)\\n```")

// rawResponseLogLimit caps the number of bytes echoed to the log on a final
// parse failure so a runaway model does not flood the log volume.
const rawResponseLogLimit = 4096

// repairBufferSlack pre-allocates room for a handful of `\` escapes injected
// by `repairJSONStrings`. The exact value barely matters — it just keeps the
// `strings.Builder` from reallocating on the common (one-or-two-quote) repair.
const repairBufferSlack = 16

const defaultVerdict = "comment"

// ErrUnparseableResponse is returned when the AI response cannot be parsed
// even after a repair pass. Callers in the command layer treat this as a
// hard failure so no malformed JSON ends up posted to a PR thread.
var ErrUnparseableResponse = errors.New("AI response is not valid JSON, even after repair")

// ParseReviewResponse parses an AI response string into a `ReviewResult`.
//
// Strategy, in order:
//  1. strict `json.Unmarshal` of the entire content;
//  2. extract a fenced ```json ... ``` block and unmarshal that;
//  3. run a repair pass that escapes unescaped double quotes inside string
//     values, then unmarshal the repaired content;
//  4. give up — log the raw content (truncated) and return
//     `ErrUnparseableResponse` so the worker logs the failure and does not
//     post anything to the PR.
//
// Step 3 exists because LLMs occasionally forget to escape a `"` inside a
// generated string value (e.g. `"body":"... — "Always use ..."."`) and the
// stock parser then dropped the whole response into a PR thread as plain
// text. See `code-guru` PR review of `internal/auth-service#NNNN` thread
// `71418` for the canonical failure trace.
func ParseReviewResponse(content string) (*entities.ReviewResult, error) {
	// 1. strict parse
	if result, ok := tryUnmarshal(content); ok {
		return result, nil
	}

	// 2. fenced JSON
	if matches := jsonCodeBlockPattern.FindStringSubmatch(content); len(matches) > 1 {
		if result, ok := tryUnmarshal(matches[1]); ok {
			return result, nil
		}
	}

	// 3. repair pass: escape unescaped quotes inside string values
	repaired := repairJSONStrings(content)
	if repaired != content {
		if result, ok := tryUnmarshal(repaired); ok {
			logger.Warn("AI response required JSON repair before parsing succeeded")
			return result, nil
		}
	}

	// 4. give up
	logger.WithFields(logger.Fields{
		"length": len(content),
		"head":   truncate(content, rawResponseLogLimit),
	}).Error("failed to parse AI response as JSON; refusing to post raw content as a PR thread")

	return nil, fmt.Errorf("%w (length=%d)", ErrUnparseableResponse, len(content))
}

func tryUnmarshal(s string) (*entities.ReviewResult, bool) {
	var result entities.ReviewResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &result); err != nil {
		return nil, false
	}

	normalizeVerdict(&result)

	return &result, true
}

// normalizeVerdict ensures the verdict field has a valid value.
func normalizeVerdict(result *entities.ReviewResult) {
	switch result.Verdict {
	case "approve", "request_changes", "comment":
		// valid, keep as-is
	default:
		result.Verdict = defaultVerdict
	}
}

// repairJSONStrings walks the input and escapes any double quote that
// appears inside a JSON string value but is *not* the legitimate
// string-terminator. Heuristic: a `"` is treated as the closing quote when
// the next non-whitespace character is one of `,`, `:`, `}`, `]`, or end of
// input. Anything else means the `"` is part of the string content and
// needs an escape so `json.Unmarshal` accepts it.
//
// The function deliberately leaves already-escaped quotes (`\"`) and
// already-correct JSON untouched — running it on a valid input returns the
// input unchanged.
func repairJSONStrings(s string) string {
	var out strings.Builder
	out.Grow(len(s) + repairBufferSlack)

	inString := false
	escapeNext := false
	for i := range len(s) {
		c := s[i]

		if escapeNext {
			out.WriteByte(c)
			escapeNext = false

			continue
		}

		if inString && c == '\\' {
			out.WriteByte(c)
			escapeNext = true

			continue
		}

		if c == '"' {
			if !inString {
				inString = true
				out.WriteByte(c)

				continue
			}
			// inside a string — decide whether this `"` is the closer
			if isJSONStringTerminator(s, i+1) {
				inString = false
				out.WriteByte(c)
			} else {
				out.WriteString(`\"`)
			}

			continue
		}

		out.WriteByte(c)
	}

	return out.String()
}

// isJSONStringTerminator scans forward from `i` skipping ASCII whitespace
// and reports whether the next non-whitespace byte is one of the JSON
// structural tokens that can legitimately follow a closing quote
// (`,`, `:`, `}`, `]`) — or the end of the input.
func isJSONStringTerminator(s string, i int) bool {
	for ; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case ',', ':', '}', ']':
			return true
		default:
			return false
		}
	}

	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "...[truncated]"
}
