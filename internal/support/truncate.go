package support

import "strconv"

// truncationSentinel is appended to a truncated value so log readers can tell
// the cut from the original content. The sentinel sits OUTSIDE the quoted
// region in the `*ForLog` helpers so a reader never confuses it with bytes
// the model or the wire actually delivered.
const truncationSentinel = "...[truncated]"

// Truncate returns the first n bytes of s plus a sentinel when the input is
// longer than n. Used for plain-text truncation that does not need to be
// log-injection safe (e.g., assembling a longer string for the operator).
//
// The cut is byte-based — a trailing multi-byte rune may be split, which is
// acceptable for the diagnostic surface this helper targets. Callers that
// embed the result in a log line should use `TruncateForLog` /
// `TruncateBytesForLog` instead.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + truncationSentinel
}

// TruncateForLog returns a `strconv.Quote`d, single-line representation of
// the first n bytes of s, with the truncation sentinel appended (outside
// the quotes) when the input is longer than n.
//
// Designed for the diagnostic-log path where the source is arbitrary
// untrusted content (model output, webhook bodies). `strconv.Quote`
// escapes newlines, tabs, ANSI sequences, and any non-printable byte —
// so a malicious input cannot inject a fake log line by including a
// `\n level=error msg="..."` sequence in its body. The output is also
// guaranteed to be valid UTF-8 even when the input is not.
//
// Use `TruncateBytesForLog` instead when the source is already a `[]byte`
// — it avoids the full-body string copy that `string(b)` would force.
func TruncateForLog(s string, n int) string {
	if len(s) <= n {
		return strconv.Quote(s)
	}

	return strconv.Quote(s[:n]) + truncationSentinel
}

// TruncateBytesForLog is the byte-slice variant of `TruncateForLog`.
// Critically, it converts only the first `min(n, len(b))` bytes to a
// string before quoting — which means a 50 MB request body never
// allocates a 50 MB intermediate string just to log the first 4 KB.
//
// This matters on the webhook-handler diagnostic path because a hostile
// caller could otherwise amplify a single forbidden request into a
// proportional memory spike. Truncating the byte slice up-front keeps
// the cost bounded by `n + truncationSentinel`.
func TruncateBytesForLog(b []byte, n int) string {
	if len(b) <= n {
		return strconv.Quote(string(b))
	}

	return strconv.Quote(string(b[:n])) + truncationSentinel
}
