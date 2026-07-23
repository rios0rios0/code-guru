package support

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// ErrContextWindowExceeded marks a review failure whose root cause is that
// the assembled prompt (the PR diff plus rules, guidelines, metadata, and
// conversation) is larger than the AI model's context window — i.e. the pull
// request is simply too big to review in a single pass.
//
// It is a distinct class from a transient backend hiccup or an unparseable
// response for two reasons:
//
//   - It is DETERMINISTIC. The prompt is identical on every attempt, so a
//     re-sample fails identically — the retry decorator short-circuits it
//     instead of burning the whole attempt budget (and, on paid backends,
//     the cost) on a review that cannot succeed until the PR shrinks.
//   - It has a DIFFERENT remedy. A transient failure clears on retry; this
//     one clears only when the author makes the change smaller. The command
//     layer renders a dedicated, actionable annotation for it (split the PR,
//     exclude generated/lock files) instead of the generic "usually
//     transient — push a new commit" message, which is actively wrong here:
//     pushing more commits only grows the diff.
//
// Each backend wraps its provider-specific "prompt too long" error with this
// sentinel (via LooksLikeContextWindowError) so the command layer can
// classify the failure with a single [errors.Is] check, regardless of which
// backend hit the limit.
var ErrContextWindowExceeded = errors.New("ai prompt exceeds the model context window")

// LooksLikeContextWindowError reports whether a backend error message names a
// context-window / prompt-too-long failure. Backends call it on their raw
// error text to decide whether to wrap the error with ErrContextWindowExceeded.
// The match is case-insensitive and substring-based so it survives the varying
// envelopes each provider wraps the core message in.
//
// The marker set is deliberately conservative: matching a genuinely transient
// error here would wrongly suppress retries and post the "too large" guidance
// for a blip. Every marker names input/context size specifically, never an
// output (max_tokens) cap. The shapes covered are:
//
//   - Anthropic Messages API 400: "prompt is too long: N tokens > M maximum"
//   - OpenAI chat completions:     "This model's maximum context length is ..."
//     (error code "context_length_exceeded")
//   - Claude CLI:                  wraps the Anthropic "prompt is too long" text
func LooksLikeContextWindowError(msg string) bool {
	markers := []string{
		"prompt is too long",
		"context window",
		"context length",
		"context_length_exceeded",
		"maximum context",
		"context limit",
		"input is too long",
	}

	lower := strings.ToLower(msg)
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

// contextWindowTokenPattern captures the token figures a provider reports
// alongside a prompt-too-long error. It deliberately requires the number to
// be immediately followed by `token(s)` or `maximum`, rather than scraping
// every integer in the message: an error envelope routinely also carries a
// dated model id (`claude-sonnet-4-20250514`) or a request id, and treating
// those as token counts would produce a nonsense ratio.
//
// The three digit minimum drops incidental small numbers (an HTTP status,
// a retry count) — no real context window is under 1000 tokens.
var contextWindowTokenPattern = regexp.MustCompile(`(\d{3,})\s*(?:tokens?|maximum)`)

// minContextWindowFigures is how many token figures a message must carry
// before the pair is usable: one for what the prompt used, one for the
// limit it blew past. A single figure names one side of the comparison and
// says nothing about the ratio, which is the only thing callers want.
const minContextWindowFigures = 2

// ParseContextWindowOverage extracts the "how much too big was it" figures
// from a backend's prompt-too-long error, returning the token count the
// prompt used and the model's limit. `ok` is false when the message carries
// no usable pair, which is the normal case for backends that phrase the
// failure without numbers — callers must always have a fallback.
//
// It exists so the batching fallback can size its FIRST batch from the real
// overage instead of blindly halving. On a pull request several times the
// window, halving costs one wasted round trip per level — and every one of
// those uploads the whole multi-megabyte prompt again before the API
// rejects it. Reading `used` and `limit` off the error turns that search
// into a single step.
//
// The shapes covered (both are `used > limit`, which is also the sanity
// check applied before returning):
//
//   - Anthropic: `prompt is too long: 1234567 tokens > 200000 maximum`
//   - OpenAI:    `This model's maximum context length is 128000 tokens.
//     However, your messages resulted in 130000 tokens.`
//
// Note the two providers order the figures differently, so the pair is
// derived positionally-agnostically: the largest matched figure is what the
// prompt used, the smallest is the limit it blew past.
func ParseContextWindowOverage(msg string) (int, int, bool) {
	matches := contextWindowTokenPattern.FindAllStringSubmatch(strings.ToLower(msg), -1)
	if len(matches) < minContextWindowFigures {
		return 0, 0, false
	}

	used, limit := 0, 0
	for _, match := range matches {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if value > used {
			used = value
		}
		if limit == 0 || value < limit {
			limit = value
		}
	}

	if limit <= 0 || used <= limit {
		return 0, 0, false
	}

	return used, limit, true
}
