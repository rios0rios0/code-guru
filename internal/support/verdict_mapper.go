package support

import forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"

// Verdict strings emitted by the AI backend response parser and the trivial
// PR detectors. Kept centralised so both call sites stay in sync with the
// gitforge ReviewVerdict mapping below.
const (
	verdictApprove = "approve"
	verdictReject  = "reject"
	verdictComment = "comment"
)

// MapVerdictToReview translates a code-guru verdict string and its summary
// body into a gitforge ReviewSubmission suitable for SubmitPullRequestReview.
// The ok return is false when the verdict has no native-review equivalent
// (i.e. the caller should skip the API call); today this is only the
// "comment" verdict since posting an empty native comment review on every
// completed AI run would be noisy. Verdicts the parser does not recognise
// also return false so a corrupt LLM payload cannot cause a spurious vote.
func MapVerdictToReview(verdict, summary string) (forgeEntities.ReviewSubmission, bool) {
	switch verdict {
	case verdictApprove:
		return forgeEntities.ReviewSubmission{
			Verdict: forgeEntities.ReviewVerdictApprove,
			Body:    summary,
		}, true
	case verdictReject:
		return forgeEntities.ReviewSubmission{
			Verdict: forgeEntities.ReviewVerdictRequestChanges,
			Body:    summary,
		}, true
	case verdictComment:
		return forgeEntities.ReviewSubmission{}, false
	}
	return forgeEntities.ReviewSubmission{}, false
}
