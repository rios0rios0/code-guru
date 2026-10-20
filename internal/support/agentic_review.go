package support

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// ErrWorkspaceUnavailable marks a review that could not start because the
// pull request could not be checked out. It is classified apart from a
// backend failure so the PR notice names the real cause.
var ErrWorkspaceUnavailable = errors.New("the pull request could not be checked out for review")

// ErrAgenticReviewIncomplete marks an agentic run that stopped at one of its
// bounds — the turn cap, the spend cap, the wall-clock timeout, or the CLI's
// structured-output retry cap — before producing a review. Like a
// context-window overflow it is NOT retried: the same pull request would hit
// the same bound again, at the same cost.
var ErrAgenticReviewIncomplete = errors.New("the agentic review stopped at a limit before finishing")

// redactedPlaceholder replaces every secret found in text that is about to be
// posted.
const redactedPlaceholder = "[redacted]"

// minRedactableSecretLength keeps short, common strings out of the redactor:
// replacing every "true" or "main" in a review because a misconfigured
// variable held that value would mangle the review and protect nothing.
const minRedactableSecretLength = 12

// anthropicCredentialPattern matches the shape of Anthropic API keys and
// OAuth tokens, so a credential the redactor was not handed by value is still
// caught.
var anthropicCredentialPattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{16,}`)

// ApplyConfidenceThreshold moves every scored finding below `threshold` out of
// the posted comments and into `Dropped`, with the reason saying so. The
// agentic prompt asks the model to apply the same bar itself; this is the
// guarantee that a model which did not still posts nothing below it. A
// finding is scored when it carries a dimension — the agentic vocabulary —
// so a score of 0 is filtered like any other low score, while diff-path
// comments, which have no dimension, are left alone.
func ApplyConfidenceThreshold(result *entities.ReviewResult, threshold int) {
	if result == nil || threshold <= 0 {
		return
	}
	kept := result.Comments[:0]
	for _, comment := range result.Comments {
		if comment.Dimension != "" && comment.Confidence < threshold {
			result.Dropped = append(result.Dropped, entities.DroppedFinding{
				FilePath:   comment.FilePath,
				Line:       comment.Line,
				Dimension:  comment.Dimension,
				Confidence: comment.Confidence,
				Reason:     fmt.Sprintf("below the confidence threshold of %d", threshold),
			})
			continue
		}
		kept = append(kept, comment)
	}
	result.Comments = kept
}

// RedactSecrets replaces, in every text field of the result — the paths,
// dimensions and severities the completion notice prints included, since the
// model writes those too — each given secret, verbatim and in its base64 and
// hex encodings, plus anything shaped like an Anthropic credential, with a
// placeholder.
//
// The agentic reviewer runs a model over untrusted pull request content with
// tools in hand, and the only channel out of its sandbox is the review it
// returns, which is posted to the pull request. A prompt injection that gets
// the model to read a credential would use exactly that channel; this closes
// it for the encodings an instruction can ask for without tools.
func RedactSecrets(result *entities.ReviewResult, secrets ...string) {
	if result == nil {
		return
	}
	replacer := secretReplacer(secrets)
	redact := func(text string) string {
		if text == "" {
			return text
		}
		if replacer != nil {
			text = replacer.Replace(text)
		}
		return anthropicCredentialPattern.ReplaceAllString(text, redactedPlaceholder)
	}

	result.PullRequestURL = redact(result.PullRequestURL)
	result.Verdict = redact(result.Verdict)
	result.Summary = redact(result.Summary)
	result.Coverage.Correctness = redact(result.Coverage.Correctness)
	result.Coverage.Security = redact(result.Coverage.Security)
	result.Coverage.Design = redact(result.Coverage.Design)
	for i := range result.Comments {
		comment := &result.Comments[i]
		comment.FilePath = redact(comment.FilePath)
		comment.Body = redact(comment.Body)
		comment.Severity = redact(comment.Severity)
		comment.Suggestion = redact(comment.Suggestion)
		comment.Dimension = redact(comment.Dimension)
	}
	for i := range result.Dropped {
		dropped := &result.Dropped[i]
		dropped.FilePath = redact(dropped.FilePath)
		dropped.Dimension = redact(dropped.Dimension)
		dropped.Reason = redact(dropped.Reason)
	}
	for i := range result.ThreadResolutions {
		resolution := &result.ThreadResolutions[i]
		resolution.ID = redact(resolution.ID)
		resolution.FilePath = redact(resolution.FilePath)
		resolution.Status = redact(resolution.Status)
		resolution.Explanation = redact(resolution.Explanation)
	}
}

// secretReplacer builds one replacer for every form of every secret long
// enough to be worth redacting.
func secretReplacer(secrets []string) *strings.Replacer {
	var pairs []string
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < minRedactableSecretLength {
			continue
		}
		raw := []byte(secret)
		for _, form := range []string{
			secret,
			base64.StdEncoding.EncodeToString(raw),
			base64.RawStdEncoding.EncodeToString(raw),
			base64.URLEncoding.EncodeToString(raw),
			base64.RawURLEncoding.EncodeToString(raw),
			hex.EncodeToString(raw),
		} {
			pairs = append(pairs, form, redactedPlaceholder)
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return strings.NewReplacer(pairs...)
}

// reviewJobStampFormat is how a review Job's name is written into the closing
// line of an outcome notice — `_Completed at <ts> · review job `<name>`._` —
// by the Job's pod for the notices it posts, and by the server for the one it
// posts when the pod died before it could.
const reviewJobStampFormat = " \xc2\xb7 review job `%s`._"

// StampReviewJob writes a review Job's name into a notice's closing
// `_… at <ts>._` line. An empty name (the review runs anywhere but in a review
// Job) or a body without that line leaves the body as it is, so every notice
// outside the Kubernetes executor reads exactly as before.
func StampReviewJob(body, jobName string) string {
	if jobName == "" || !strings.HasSuffix(body, "._") {
		return body
	}
	return strings.TrimSuffix(body, "._") + fmt.Sprintf(reviewJobStampFormat, jobName)
}

// HasReviewOutcomeOf reports whether any of the bodies is a review outcome — a
// completion or a failure notice, the ones carrying the review-once marker —
// stamped with the named review Job. The scheduler uses it to tell a Job whose
// pod posted its own failure notice and then exited from one that died before
// posting anything, so a failure is never reported twice and never left
// unreported.
//
// It matches the Job by name, never by time. A re-review queued behind another
// review starts seconds after that review's completion notice, so any window
// wide enough to absorb clock skew would also match the previous review's
// outcome and swallow the failure of the one that followed. Job names are
// unique (a millisecond stamp and a random tail), so a stamped notice can only
// be that Job's.
func HasReviewOutcomeOf(bodies []string, jobName string) bool {
	if jobName == "" {
		return false
	}
	stamp := fmt.Sprintf(reviewJobStampFormat, jobName)
	for _, body := range bodies {
		if strings.Contains(body, botReviewCompleteMarker) && strings.Contains(body, stamp) {
			return true
		}
	}
	return false
}
