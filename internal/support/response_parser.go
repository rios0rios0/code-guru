package support

import (
	"crypto/sha256"
	"encoding/hex"
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

// rawResponseLogLimit caps the number of bytes echoed at `DEBUG` level on a
// parse failure so a runaway model does not flood the log volume even when
// the operator has explicitly opted into raw-content logging.
const rawResponseLogLimit = 4096

// fingerprintHexLen is the prefix length of the response SHA-256 digest emitted
// at `ERROR` so two failures with the same model output can be correlated
// without exposing the model output itself.
const fingerprintHexLen = 16

// repairBufferSlack pre-allocates room for a handful of `\` escapes injected
// by `repairJSONStrings`. The exact value barely matters — it just keeps the
// `strings.Builder` from reallocating on the common (one-or-two-quote) repair.
const repairBufferSlack = 16

const defaultVerdict = "comment"

// ErrUnparseableResponse is returned when the AI response cannot be parsed
// even after a repair pass. Callers in the command layer treat this as a
// hard failure so no malformed JSON ends up posted to a PR thread.
var ErrUnparseableResponse = errors.New("ai response is not valid JSON, even after repair")

// ParseReviewResponse parses an AI response string into a `ReviewResult`.
//
// Strategy, in order:
//  1. strict `json.Unmarshal` of the entire content;
//  2. extract a fenced ```json ... ``` block and unmarshal that;
//  3. run a repair pass that escapes unescaped double quotes inside string
//     values, then unmarshal the repaired content;
//  4. give up — log a length + content fingerprint at `ERROR`, log the raw
//     content (truncated) at `DEBUG` only, and return `ErrUnparseableResponse`
//     so the worker logs the failure and does not post anything to the PR.
//
// Step 3 exists because LLMs occasionally forget to escape a `"` inside a
// generated string value (e.g. `"body":"... — "Always use ..."."`) and the
// stock parser then dropped the whole response into a PR thread as plain
// text. See `code-guru` PR review of `backend/authenticator#12027` thread
// `71418` for the canonical failure trace.
//
// The raw content is intentionally NOT emitted at `ERROR` because the model
// echoes pieces of the prompt back in the body field and the prompt embeds
// the full PR diff — so an unconditional raw-content log would dump
// arbitrary repository source (and any in-diff secrets) into shared log
// stores. Operators who need the raw output for diagnosis can drop the log
// level to `DEBUG` on a single pod.
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
		"length":      len(content),
		"fingerprint": fingerprintContent(content),
	}).Error("failed to parse AI response as JSON; refusing to post raw content as a PR thread")
	logger.WithField("raw", TruncateForLog(content, rawResponseLogLimit)).
		Debug("unparseable AI response (raw, truncated) — gated behind DEBUG to avoid leaking diff content")

	return nil, fmt.Errorf("%w (length=%d)", ErrUnparseableResponse, len(content))
}

// fingerprintContent returns the first `fingerprintHexLen` hex chars of the
// SHA-256 digest of `s`. Two failures emitting the same fingerprint are the
// same model output, so operators can grep across pods/runs without exposing
// the content itself.
func fingerprintContent(s string) string {
	digest := sha256.Sum256([]byte(s))

	return hex.EncodeToString(digest[:])[:fingerprintHexLen]
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
