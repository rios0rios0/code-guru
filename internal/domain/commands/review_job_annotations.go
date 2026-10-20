package commands

import (
	"context"
	"fmt"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/support"
)

// PostReviewQueuedNotice tells the pull request its review is waiting — for a
// free review slot, at the given 1-based queue position, or, when
// `behindOwnReview` is set, for the review of this same pull request that is
// still running. Best-effort, like every other annotation: a failure is logged
// and never blocks the queue.
func PostReviewQueuedNotice(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	position int,
	behindOwnReview bool,
) {
	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	body := buildReviewQueuedBody(time.Now().UTC(), position)
	if behindOwnReview {
		body = buildReReviewQueuedBody(time.Now().UTC())
	}
	if err := provider.PostPullRequestComment(
		timeoutCtx, repo, prID, body, forgeEntities.WithThreadStatus(annotationThreadStatus),
	); err != nil {
		logger.Warnf("PR #%d: failed to post the 'review queued' notice: %v", prID, err)
	}
}

// ReportReviewJobFailure tells the pull request that its review Job died
// before posting a result, and reports whether it did.
//
// It stays silent in two cases. A closed pull request gets nothing, exactly as
// the review itself would post nothing on one. And a pull request that already
// carries a completion or failure notice stamped with `jobName` gets nothing
// either: that notice came from the Job's own pod, which reports every failure
// it can see itself (a timeout, a backend error) and only then exits non-zero.
// The Job failing afterwards is the same failure, already told. The notice it
// does post carries the Job's name too, which also hands the operator the Job
// whose logs to read.
func ReportReviewJobFailure(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	reason string,
	jobName string,
) (bool, error) {
	if isPullRequestClosed(ctx, provider, repo, prID) {
		return false, nil
	}
	comments, err := provider.ListPullRequestComments(ctx, repo, prID)
	if err != nil {
		return false, fmt.Errorf("listing the comments of PR #%d: %w", prID, err)
	}
	bodies := make([]string, 0, len(comments))
	for _, comment := range comments {
		bodies = append(bodies, comment.Body)
	}
	if support.HasReviewOutcomeOf(bodies, jobName) {
		return false, nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	body := support.StampReviewJob(buildReviewJobFailedBody(time.Now().UTC(), reason), jobName)
	if err = provider.PostPullRequestComment(
		timeoutCtx, repo, prID, body, forgeEntities.WithThreadStatus(annotationThreadStatus),
	); err != nil {
		return false, fmt.Errorf("posting the review job failure on PR #%d: %w", prID, err)
	}
	return true, nil
}

// buildReviewQueuedBody renders the notice the webhook server posts when it
// hands a pull request to a review Job that has to wait for a free slot. It
// tells the author the request was received — without it, a review queued
// behind a burst of others would look exactly like one that was dropped.
//
// The headline carries the `**Code Guru ` annotation marker (so re-reviews
// recognise the bot's own account from it) but deliberately NOT
// `**Code Guru review`: that substring is the review-once gate, and setting
// the gate before the review has run would make the review Job skip itself
// when it finally starts.
func buildReviewQueuedBody(now time.Time, position int) string {
	ahead := "It is next in line"
	if waiting := position - 1; waiting > 0 {
		ahead = fmt.Sprintf("%d %s ahead of it", waiting, pluralize(waiting, "review is", "reviews are"))
	}
	return fmt.Sprintf(
		"\xe2\x8f\xb3 **Code Guru queued this pull request for review.**\n\n"+
			"Every review slot is busy right now. %s; the review starts automatically as soon as a slot "+
			"frees up, and the \"reviewing\" notice follows when it does.\n\n"+
			"_Queued at %s._",
		ahead,
		now.UTC().Format(time.RFC3339),
	)
}

// buildReReviewQueuedBody renders the notice for a re-review a mention asked
// for while the previous review of the pull request was still running: it
// waits for that review on purpose, so it can read the threads it posts. The
// same marker rules as buildReviewQueuedBody apply — `**Code Guru ` yes,
// `**Code Guru review` no.
func buildReReviewQueuedBody(now time.Time) string {
	return fmt.Sprintf(
		"\xe2\x8f\xb3 **Code Guru queued the re-review you asked for.**\n\n"+
			"A review of this pull request is still running. The re-review starts as soon as it finishes, "+
			"and reads the threads it posts.\n\n"+
			"_Queued at %s._",
		now.UTC().Format(time.RFC3339),
	)
}

// reviewJobFailureExplanations maps a Kubernetes Job failure reason to what it
// means for the author (Mapper pattern). The reasons are the ones the Job
// controller sets on a Job's `Failed` condition.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var reviewJobFailureExplanations = map[string]string{
	"DeadlineExceeded":     "it ran past its time limit",
	"BackoffLimitExceeded": "its process stopped unexpectedly (for example, it ran out of memory)",
	"PodFailurePolicy":     "its process stopped unexpectedly",
	"Evicted":              "the node it ran on reclaimed its resources",
}

// buildReviewJobFailedBody renders the notice the webhook server posts for a
// review Job that died without posting anything itself — killed at its
// deadline, out of memory, evicted. A review pod reports every failure it can
// see on its own; this covers the ones it cannot, so a pull request is never
// left with a "reviewing" notice and nothing after it.
//
// The headline carries the `**Code Guru review` review-once marker, like every
// other failure notice: otherwise each push would start another review that
// dies the same way, at the same cost. A mention requests a retry.
func buildReviewJobFailedBody(now time.Time, reason string) string {
	explanation, ok := reviewJobFailureExplanations[reason]
	if !ok {
		explanation = fmt.Sprintf("it failed (`%s`)", reason)
	}
	return fmt.Sprintf(
		"\xe2\x9a\xa0\xef\xb8\x8f **Code Guru review failed.**\n\n"+
			"The review job stopped before it could post a review: %s. Mention `@code-guru` in a comment "+
			"to try again; if it keeps happening, the operator can find the job's logs in the cluster.\n\n"+
			"_Failed at %s._",
		explanation,
		now.UTC().Format(time.RFC3339),
	)
}

// pluralize picks the singular or plural form for a count.
func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
