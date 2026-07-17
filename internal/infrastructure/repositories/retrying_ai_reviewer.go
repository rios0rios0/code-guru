package repositories

import (
	"context"
	"errors"
	"fmt"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// retryErrorLogLimit bounds the per-attempt error text echoed into the
// "attempt N failed" warn line so a runaway backend error (e.g. a
// multi-kilobyte claude CLI JSON envelope) cannot flood the log volume.
const retryErrorLogLimit = 512

// RetryingAIReviewer decorates an AIReviewerRepository with bounded retries.
// LLM backends fail in two ways a re-sample reliably recovers from:
//
//   - the model returns prose / markdown instead of the bare JSON object
//     the prompt demands (`support.ParseReviewResponse` -> `ErrUnparseableResponse`);
//   - a transient backend hiccup (the claude CLI's "socket connection was
//     closed unexpectedly", an API 5xx, a dropped connection).
//
// Both used to surface as a "review failed" annotation on the PR after a
// single try — and the transient case even dumped the raw CLI error
// envelope into that annotation. Because LLM output is non-deterministic,
// simply calling the backend again usually produces a clean, parseable
// review, so this decorator re-invokes ReviewDiff up to `attempts` times
// and returns the first success. Only when every attempt fails does it
// return an error, which the command layer renders as a single,
// content-free failure annotation (the raw output is logged, never posted).
//
// The decorator sets `request.Attempt` before each call so the prompt
// builder reinforces the JSON-only instruction on retries.
type RetryingAIReviewer struct {
	inner    repositories.AIReviewerRepository
	attempts int
}

// WithRetry wraps `inner` so ReviewDiff is retried up to `attempts` times.
// `attempts` < 1 is clamped to 1 (behaves like the bare backend) so a
// misconfigured zero can never disable reviews entirely.
func WithRetry(inner repositories.AIReviewerRepository, attempts int) *RetryingAIReviewer {
	if attempts < 1 {
		attempts = 1
	}
	return &RetryingAIReviewer{inner: inner, attempts: attempts}
}

// Name returns the wrapped backend's identifier unchanged — the retry
// wrapper is transparent, so logs / config still read as the real backend.
func (r *RetryingAIReviewer) Name() string {
	return r.inner.Name()
}

// ReviewDiff invokes the wrapped backend, retrying on any error up to the
// configured attempt budget. Honours context cancellation: a cancelled or
// deadline-exceeded context between attempts stops the loop immediately
// rather than burning the remaining budget on a review nobody is waiting
// for (PR closed mid-flight, pod draining).
func (r *RetryingAIReviewer) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	var lastErr error
	for attempt := 1; attempt <= r.attempts; attempt++ {
		req := request
		req.Attempt = attempt

		result, err := r.inner.ReviewDiff(ctx, req)
		if err == nil {
			if attempt > 1 {
				logger.Infof("AI review (%s) succeeded on attempt %d/%d", r.inner.Name(), attempt, r.attempts)
			}
			return result, nil
		}
		lastErr = err

		// Do not retry a review whose context is already gone — the work
		// is no longer wanted and the next attempt would fail the same way.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("AI review cancelled after %d attempt(s): %w", attempt, ctxErr)
		}
		// Do not retry a prompt-too-long failure: the prompt is byte-for-byte
		// identical on every attempt, so a re-sample is guaranteed to fail the
		// same way. Return immediately (the error already carries the sentinel)
		// so the command layer posts the "split your PR" guidance instead of
		// burning the whole budget — and, on paid backends, the cost — on a
		// review that cannot succeed until the PR itself shrinks.
		if errors.Is(err, support.ErrContextWindowExceeded) {
			logger.Warnf(
				"AI review (%s) failed on attempt %d/%d: the pull request exceeds the model context window; not retrying",
				r.inner.Name(),
				attempt,
				r.attempts,
			)

			return nil, err
		}
		// A content-safety refusal is deterministic on the same content — the
		// model declines the identical diff the same way — so a re-sample is
		// futile. Return immediately so the command layer posts the "declined"
		// guidance instead of burning the budget. (Any per-model recovery has
		// already happened inside the backend via a configured fallback model.)
		if errors.Is(err, support.ErrContentSafetyRefusal) {
			logger.Warnf(
				"AI review (%s) failed on attempt %d/%d: the model's content-safety system declined the content; not retrying",
				r.inner.Name(),
				attempt,
				r.attempts,
			)

			return nil, err
		}
		if attempt < r.attempts {
			logger.Warnf(
				"AI review (%s) attempt %d/%d failed (%s); retrying",
				r.inner.Name(), attempt, r.attempts,
				support.TruncateForLog(err.Error(), retryErrorLogLimit),
			)
		}
	}
	return nil, fmt.Errorf("AI review failed after %d attempt(s): %w", r.attempts, lastErr)
}
