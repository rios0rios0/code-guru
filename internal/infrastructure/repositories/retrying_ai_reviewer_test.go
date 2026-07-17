//go:build unit

package repositories_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// fakeAIReviewer is a programmable AIReviewerRepository double: it returns
// the queued (result, error) for each successive call and records the
// Attempt number it saw on every invocation, so a test can assert both the
// retry count and that the decorator threads the attempt number through.
type fakeAIReviewer struct {
	results  []*entities.ReviewResult
	errs     []error
	calls    int
	attempts []int
}

func (f *fakeAIReviewer) Name() string { return "fake" }

func (f *fakeAIReviewer) ReviewDiff(
	_ context.Context, req entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	i := f.calls
	f.calls++
	f.attempts = append(f.attempts, req.Attempt)
	var res *entities.ReviewResult
	var err error
	if i < len(f.results) {
		res = f.results[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return res, err
}

func TestRetryingAIReviewer(t *testing.T) {
	t.Parallel()

	okResult := &entities.ReviewResult{Verdict: "approve", Summary: "ok"}

	t.Run("should return on the first attempt without retrying", func(t *testing.T) {
		t.Parallel()

		// given
		fake := &fakeAIReviewer{results: []*entities.ReviewResult{okResult}, errs: []error{nil}}
		reviewer := infraRepos.WithRetry(fake, 3)

		// when
		got, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.NoError(t, err)
		assert.Same(t, okResult, got)
		assert.Equal(t, 1, fake.calls, "a first-attempt success must not trigger a retry")
		assert.Equal(t, []int{1}, fake.attempts, "the first attempt carries Attempt=1")
	})

	t.Run("should retry and succeed on a later attempt after an unparseable response", func(t *testing.T) {
		t.Parallel()

		// given: the first call returns the unparseable sentinel, the second succeeds
		fake := &fakeAIReviewer{
			results: []*entities.ReviewResult{nil, okResult},
			errs:    []error{support.ErrUnparseableResponse, nil},
		}
		reviewer := infraRepos.WithRetry(fake, 3)

		// when
		got, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.NoError(t, err)
		assert.Same(t, okResult, got)
		assert.Equal(t, 2, fake.calls)
		assert.Equal(t, []int{1, 2}, fake.attempts,
			"Attempt increments per call so the prompt builder can reinforce JSON-only output on retries")
	})

	t.Run("should return a sentinel-wrapped error after exhausting every attempt", func(t *testing.T) {
		t.Parallel()

		// given: every attempt returns the unparseable sentinel
		fake := &fakeAIReviewer{errs: []error{
			support.ErrUnparseableResponse, support.ErrUnparseableResponse, support.ErrUnparseableResponse,
		}}
		reviewer := infraRepos.WithRetry(fake, 3)

		// when
		got, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Equal(t, 3, fake.calls, "all attempts are used before giving up")
		assert.ErrorIs(t, err, support.ErrUnparseableResponse,
			"the final error must still unwrap to the sentinel so the command layer classifies the failure without posting raw output")
	})

	t.Run("should NOT retry a prompt-too-long failure (deterministic — identical prompt each attempt)", func(t *testing.T) {
		t.Parallel()

		// given: the first call returns the context-window sentinel; a retry
		// would re-send the byte-for-byte identical oversized prompt and fail
		// the same way, so the decorator must stop after one attempt. The
		// later queued errors would only be consumed by a (wrong) retry.
		tooLong := fmt.Errorf("%w (anthropic: prompt is too long)", support.ErrContextWindowExceeded)
		fake := &fakeAIReviewer{errs: []error{tooLong, support.ErrUnparseableResponse, support.ErrUnparseableResponse}}
		reviewer := infraRepos.WithRetry(fake, 3)

		// when
		got, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Equal(t, 1, fake.calls,
			"a prompt-too-long failure must stop after the first attempt, never burning the retry budget")
		assert.ErrorIs(t, err, support.ErrContextWindowExceeded,
			"the returned error must still carry the sentinel so the command layer posts the 'split your PR' guidance")
	})

	t.Run("should stop early when the context is cancelled", func(t *testing.T) {
		t.Parallel()

		// given: a cancelled context and a backend that always errors
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fake := &fakeAIReviewer{errs: []error{errors.New("boom"), errors.New("boom"), errors.New("boom")}}
		reviewer := infraRepos.WithRetry(fake, 3)

		// when
		_, err := reviewer.ReviewDiff(ctx, entities.ReviewRequest{})

		// then
		require.Error(t, err)
		assert.Equal(t, 1, fake.calls, "a cancelled context must stop the retry loop after the first failure")
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("should clamp an attempt budget below 1 to a single attempt", func(t *testing.T) {
		t.Parallel()

		// given
		fake := &fakeAIReviewer{results: []*entities.ReviewResult{okResult}, errs: []error{nil}}
		reviewer := infraRepos.WithRetry(fake, 0)

		// when
		_, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.NoError(t, err)
		assert.Equal(t, 1, fake.calls,
			"attempts<1 must behave as a single attempt, never zero (which would disable reviews entirely)")
	})

	t.Run("should pass the wrapped backend name through unchanged", func(t *testing.T) {
		t.Parallel()

		// given / when / then
		assert.Equal(t, "fake", infraRepos.WithRetry(&fakeAIReviewer{}, 2).Name())
	})
}
