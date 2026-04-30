//go:build unit

package support_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestTruncate(t *testing.T) {
	t.Parallel()

	t.Run("should return input unchanged when shorter than the limit", func(t *testing.T) {
		// given
		input := "short"

		// when
		got := support.Truncate(input, 10)

		// then
		assert.Equal(t, "short", got)
	})

	t.Run("should return input unchanged when exactly at the limit", func(t *testing.T) {
		// given: byte length equals the cap — no sentinel must be added,
		// because the value is fully represented and any sentinel would
		// confuse the reader.
		input := "0123456789"

		// when
		got := support.Truncate(input, 10)

		// then
		assert.Equal(t, "0123456789", got)
	})

	t.Run("should clip and append the truncation sentinel when over the limit", func(t *testing.T) {
		// given
		input := strings.Repeat("a", 20)

		// when
		got := support.Truncate(input, 10)

		// then
		assert.Equal(t, strings.Repeat("a", 10)+"...[truncated]", got)
	})

	t.Run("should handle empty input", func(t *testing.T) {
		// given
		input := ""

		// when
		got := support.Truncate(input, 10)

		// then
		assert.Empty(t, got)
	})

	t.Run("should be byte-based and may split a multi-byte rune at the boundary", func(t *testing.T) {
		// given: `é` is 2 bytes in UTF-8 (0xC3 0xA9). Cutting at byte 2
		// preserves it; cutting at byte 1 would split it. The contract
		// is byte-based and the helper does NOT search for a rune
		// boundary — that property is what `TruncateForLog` later relies
		// on to bound memory cost.
		input := "héllo" // 6 bytes total

		// when
		got := support.Truncate(input, 1)

		// then
		assert.Len(t, got, 1+len("...[truncated]"))
	})
}

func TestTruncateForLog(t *testing.T) {
	t.Parallel()

	t.Run("should quote a short string and skip the sentinel", func(t *testing.T) {
		// given: under the cap → fully represented. Quoting wraps the
		// content in `"..."` and escapes any control byte.
		input := "hello"

		// when
		got := support.TruncateForLog(input, 10)

		// then
		assert.Equal(t, `"hello"`, got)
	})

	t.Run("should quote and append sentinel OUTSIDE the quotes when over the limit", func(t *testing.T) {
		// given: the sentinel must sit OUTSIDE the quoted region so a
		// reader never confuses it with bytes that were actually in the
		// body. This contract is also what makes the output unambiguous
		// when the body itself contains the literal `...[truncated]`
		// substring.
		input := strings.Repeat("a", 20)

		// when
		got := support.TruncateForLog(input, 5)

		// then
		assert.Equal(t, `"aaaaa"...[truncated]`, got)
	})

	t.Run("should escape newlines so they cannot inject fake log lines (log-injection defence)", func(t *testing.T) {
		// given: an attacker-controlled body could otherwise embed a
		// `\nlevel=error msg="forged"` sequence that the logrus
		// TextFormatter would render as a separate log entry.
		// `strconv.Quote` collapses every control byte into its escape
		// form, neutralising the attack.
		input := "before\nlevel=error msg=\"forged\"\nafter"

		// when
		got := support.TruncateForLog(input, 1024)

		// then
		assert.NotContains(t, got, "\n", "newlines must be escaped, never emitted literally")
		assert.Contains(t, got, `\n`, "the escaped form should appear instead")
		assert.Contains(t, got, `\"forged\"`, "embedded quotes must also be escaped")
	})

	t.Run("should escape ANSI / control bytes so the log stays single-line", func(t *testing.T) {
		// given: a body with a \x1b[31m ANSI escape and a \r\n CRLF
		// pair — both common log-injection vectors.
		input := "\x1b[31mred text\x1b[0m\r\nNEXT_LINE"

		// when
		got := support.TruncateForLog(input, 1024)

		// then
		assert.NotContains(t, got, "\x1b", "raw ANSI escapes must not survive into the log")
		assert.NotContains(t, got, "\r", "raw CR must not survive")
		assert.NotContains(t, got, "\n", "raw LF must not survive")
		assert.True(t, strings.HasPrefix(got, `"`) && strings.HasSuffix(got, `"`),
			"output must be wrapped in a single pair of quotes")
	})

	t.Run("should handle empty input as an empty quoted string with no sentinel", func(t *testing.T) {
		// given
		input := ""

		// when
		got := support.TruncateForLog(input, 10)

		// then
		assert.Equal(t, `""`, got)
	})

	t.Run("should produce valid UTF-8 even when the input contains an invalid sequence", func(t *testing.T) {
		// given: `\xff` is not a valid UTF-8 start byte; logging it raw
		// would corrupt JSON loggers and some terminals. `strconv.Quote`
		// escapes it to `\xff` (4 ASCII bytes), guaranteeing UTF-8 output.
		input := "ok-then-\xff-junk"

		// when
		got := support.TruncateForLog(input, 1024)

		// then
		assert.NotContains(t, got, "\xff", "raw invalid UTF-8 byte must not survive")
		assert.Contains(t, got, `\xff`, "the escaped form should appear instead")
	})
}

func TestTruncateBytesForLog(t *testing.T) {
	t.Parallel()

	t.Run("should quote a short byte slice and skip the sentinel", func(t *testing.T) {
		// given
		input := []byte("hello")

		// when
		got := support.TruncateBytesForLog(input, 10)

		// then
		assert.Equal(t, `"hello"`, got)
	})

	t.Run("should quote and append sentinel when over the limit", func(t *testing.T) {
		// given
		input := []byte(strings.Repeat("b", 20))

		// when
		got := support.TruncateBytesForLog(input, 5)

		// then
		assert.Equal(t, `"bbbbb"...[truncated]`, got)
	})

	t.Run("should escape newlines (log-injection defence)", func(t *testing.T) {
		// given: same threat model as `TruncateForLog`, but the source
		// is the raw HTTP body bytes from a webhook — exactly the
		// allowlist-rejection diagnostic path. Pin the property here so
		// a future "let me skip the quoting on the byte path" change
		// has to surface in the test.
		input := []byte("a\n\n\nb")

		// when
		got := support.TruncateBytesForLog(input, 1024)

		// then
		assert.NotContains(t, got, "\n", "raw LF must not survive into the log")
		assert.Contains(t, got, `\n`, "escaped form should appear")
	})

	t.Run("should NOT allocate a full-size string when input is much larger than the cap", func(t *testing.T) {
		// given: a 1 MiB input. The byte-slice variant exists precisely
		// to avoid the `string(b)` full-body copy that the string-based
		// variant would force. Pin the budget by asserting the OUTPUT
		// length stays bounded — the only allocation that matters
		// downstream is the returned string. (We can't directly assert
		// internal allocations without runtime/testing/allocs.)
		input := make([]byte, 1024*1024)
		for i := range input {
			input[i] = 'x'
		}

		// when
		got := support.TruncateBytesForLog(input, 4096)

		// then: 4096 quoted bytes (every `x` survives quoting as a
		// single byte) + 2 surrounding quotes + sentinel.
		assert.Len(t, got, 4096+2+len("...[truncated]"))
	})

	t.Run("should handle empty byte slice as an empty quoted string", func(t *testing.T) {
		// given: cover both the `nil` slice and the explicit empty-but-
		// non-nil `[]byte{}` since they are distinct in Go and a regression
		// could trigger only one of them.
		var nilBytes []byte
		emptyBytes := []byte{}

		// when
		gotNil := support.TruncateBytesForLog(nilBytes, 10)
		gotEmpty := support.TruncateBytesForLog(emptyBytes, 10)

		// then
		assert.Equal(t, `""`, gotNil)
		assert.Equal(t, `""`, gotEmpty)
	})
}
