package support

import (
	"strings"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
)

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

// botAnnotationMarker is the substring shared by ALL of the bot's
// PR-wide status annotations: the "is reviewing"
// (`buildReviewingMarkerBody`), "review complete"
// (`buildReviewCompleteBody`), and "review failed"
// (`buildReviewFailedBody`) bodies all render `**Code Guru <...>`.
// Broader than `botReviewCompleteMarker` (which deliberately matches
// only the completed/failed shapes for the review-once gate): here we
// want to recognise the bot from ANY status annotation it has posted,
// including the "reviewing" marker that exists when a review crashed
// before completing.
const botAnnotationMarker = "**Code Guru "

// DetectBotAuthors returns the distinct set of comment authors that
// posted one of the bot's PR-wide status annotations on this PR (the
// "reviewing" / "review complete" / "review failed" notices, all of
// which carry the botAnnotationMarker substring).
//
// It exists so the re-review conversation walk can recognise the bot's
// own prior comments REGARDLESS of the account name the deployment
// posts under. On GitHub the bot is `code-guru[bot]` and the built-in
// matcher in `IsBotAuthor` recognises it, but a self-hosted Azure
// DevOps deployment commonly posts under an organisation service
// account (e.g. an `automation` / `svc-*` identity) whose name does
// not start with `code-guru`. Without self-detection,
// `BuildReviewConversation` finds zero prior bot threads, the LLM
// re-reviews from scratch on every re-review, and the bot re-posts
// findings the PR author has already addressed or rebutted.
//
// Only PR-wide comments (`Line <= 0`) carrying the annotation marker
// are considered, so a human who merely quotes the bot inline is not
// mis-identified. The returned identities are suitable to pass straight
// to `IsBotAuthor` as additional bot identities. Order is the
// first-seen order of the input; duplicates are collapsed.
func DetectBotAuthors(comments []forgeEntities.PullRequestComment) []string {
	seen := make(map[string]struct{})
	var authors []string
	for _, comment := range comments {
		if comment.Line > 0 || comment.Author == "" {
			continue
		}
		if !looksLikeBotAnnotation(comment.Body) {
			continue
		}
		if _, ok := seen[comment.Author]; ok {
			continue
		}
		seen[comment.Author] = struct{}{}
		authors = append(authors, comment.Author)
	}
	return authors
}

// looksLikeBotAnnotation reports whether body is one of the bot's own
// PR-wide status annotations rather than a human comment that merely
// quotes or discusses the marker. All three annotation bodies open with
// a single status emoji, a space, then the bold marker
// (`🤖/✅/⚠️ **Code Guru …`), so the marker sits at the very START of the
// body modulo that leading emoji/whitespace decoration.
//
// The detector therefore requires `botAnnotationMarker` to appear with
// nothing but decoration before it — specifically no ASCII letter and no
// backtick. A human PR-wide comment that discusses the annotation
// (e.g. "should we reword `✅ **Code Guru review complete.**`?") always
// has letters and/or a backtick before the marker, so it no longer trips
// the detector; without this anchor, that human would be mis-identified
// as the bot and their inline threads pulled in as prior bot threads —
// which a re-review could then auto-reply to and resolve. Anchoring on
// the marker (rather than hard-coding each annotation's exact emoji +
// wording) keeps the check resilient to future tweaks of the annotation
// bodies, the same rationale `botReviewCompleteMarker` documents. Pinned
// per Copilot review on PR #163.
func looksLikeBotAnnotation(body string) bool {
	before, _, ok := strings.Cut(body, botAnnotationMarker)
	if !ok {
		return false
	}
	for _, r := range before {
		if r == '`' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
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
