//go:build unit

package support

// Test-only re-exports of unexported helpers, so the external
// `support_test` package can pin their contracts directly instead of
// inferring them through a caller's boolean result.
//
// `HasMention` returns a single bool, which cannot observe token
// de-duplication, the exclusion of the built-in `MentionToken`, the
// stable output ordering, or the invariant that an empty token is never
// emitted (that one would hang `containsMentionToken` if it ever were).
// Extend this file rather than exporting production symbols for tests.
//
//nolint:gochecknoglobals // test-only re-exports of unexported helpers
var (
	MentionTokensFor     = mentionTokensFor
	ContainsMentionToken = containsMentionToken
)
