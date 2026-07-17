package support

import (
	"errors"
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
