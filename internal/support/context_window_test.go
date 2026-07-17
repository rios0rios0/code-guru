//go:build unit

package support_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestLooksLikeContextWindowError(t *testing.T) {
	t.Parallel()

	t.Run("should match the provider-specific prompt-too-long messages", func(t *testing.T) {
		t.Parallel()

		// given: the real "too large" shapes each backend surfaces
		cases := map[string]string{
			"anthropic 400":  "prompt is too long: 258000 tokens > 200000 maximum",
			"openai message": "This model's maximum context length is 128000 tokens. However, your messages resulted in 210000 tokens",
			"openai code":    "context_length_exceeded",
			"generic window": "the request exceeds the model's context window",
			"case-insensitive": "PROMPT IS TOO LONG",
		}

		for name, msg := range cases {
			// when
			got := support.LooksLikeContextWindowError(msg)

			// then
			assert.Truef(t, got, "%q must be recognised as a context-window error", name)
		}
	})

	t.Run("should NOT match transient or unrelated backend errors", func(t *testing.T) {
		t.Parallel()

		// given: failures that are transient (retry helps) or otherwise
		// unrelated to input size — misclassifying any of these would wrongly
		// suppress retries and post the "split your PR" guidance for a blip
		cases := map[string]string{
			"socket drop":  "API Error: socket connection closed unexpectedly",
			"rate limit":   "rate_limit_exceeded: too many requests",
			"auth":         "authentication_error: invalid x-api-key",
			"5xx":          "anthropic API returned status 529: overloaded",
			"output cap":   "the response hit the max_tokens output limit",
			"empty string": "",
		}

		for name, msg := range cases {
			// when
			got := support.LooksLikeContextWindowError(msg)

			// then
			assert.Falsef(t, got, "%q must NOT be treated as a context-window error", name)
		}
	})
}

func TestErrContextWindowExceededUnwraps(t *testing.T) {
	t.Parallel()

	t.Run("should stay errors.Is-detectable through a backend wrapper envelope", func(t *testing.T) {
		t.Parallel()

		// given: the shape a backend produces — the sentinel wrapping the
		// provider detail so the command layer can classify with one check
		wrapped := fmt.Errorf("%w (anthropic: prompt is too long)", support.ErrContextWindowExceeded)

		// when / then
		require.ErrorIs(t, wrapped, support.ErrContextWindowExceeded)
	})
}
