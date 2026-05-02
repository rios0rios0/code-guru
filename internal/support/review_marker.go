package support

import "strings"

// botReviewCompleteMarker is the unique substring the bot writes into
// every "review complete" / "review failed" PR-wide annotation. The
// prefix is shared across both annotation bodies (see
// `commands.buildReviewCompleteBody` and `commands.buildReviewFailedBody`)
// so a single substring match catches both shapes — meaning a previous
// successful review AND a previous failed review both count as "this
// PR has already been touched, do not re-review on the next push".
//
// The "is reviewing" marker (`commands.buildReviewingMarkerBody`) is
// intentionally NOT in this set: it signals an in-flight review, not a
// completed one, so a webhook arriving while another pod is still
// reviewing should still go through the K8s-Lease cross-pod dedup
// rather than the marker gate.
const botReviewCompleteMarker = "**Code Guru review"

// HasCompletedReviewMarker returns true when any of the supplied
// PR-wide comment bodies looks like the bot's "review complete" or
// "review failed" annotation. The check is intentionally substring-
// based (not exact-match) so a future tweak to the annotation body
// does not accidentally bypass the gate; the marker is rare enough
// in real human comments that the false-positive risk is acceptable.
//
// Used by `ReviewCommand.Execute` as the review-once-per-PR gate:
// when this returns true and the user did NOT mention the bot, the
// review is short-circuited so the PR is not flooded with duplicate
// reviews on every push.
func HasCompletedReviewMarker(bodies []string) bool {
	for _, body := range bodies {
		if strings.Contains(body, botReviewCompleteMarker) {
			return true
		}
	}
	return false
}

// MentionToken is the literal the bot looks for in a user's PR comment
// to treat the comment as a re-review request. Case-insensitive match
// is performed by HasMention; word-boundary checks prevent a substring
// match against unrelated `@code-guru-foo` mentions.
const MentionToken = "@code-guru"

// HasMention returns true when the comment body contains a `@code-guru`
// mention. Case-insensitive; rejects substrings that continue past the
// token (e.g. `@code-guru-bot` is NOT a match because the next byte is
// not whitespace / punctuation / EOF). A future refactor that needs to
// distinguish between "mention" and "mention-with-instructions" can
// extend this without touching the call sites.
func HasMention(body string) bool {
	lower := strings.ToLower(body)
	for {
		idx := strings.Index(lower, MentionToken)
		if idx == -1 {
			return false
		}
		end := idx + len(MentionToken)
		// Word-boundary check on the right side: the next byte must be
		// whitespace, punctuation, or end-of-string. Letters / digits /
		// `-` / `_` mean the match is part of a longer identifier (e.g.
		// `@code-guru-staging`) and we should keep scanning.
		if end == len(lower) || !isMentionWordChar(lower[end]) {
			return true
		}
		lower = lower[end:]
	}
}

// isMentionWordChar reports whether b is a character that would be part
// of a longer mention identifier. The set matches GitHub / Azure DevOps
// mention rules: alphanumerics, `-`, `_`. Anything else is a word
// boundary and the preceding `@code-guru` counts as a real mention.
func isMentionWordChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '-' || b == '_':
		return true
	default:
		return false
	}
}
