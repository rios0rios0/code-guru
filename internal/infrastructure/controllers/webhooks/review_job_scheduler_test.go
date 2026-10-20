package webhooks_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	doubles "github.com/rios0rios0/codeguru/test/infrastructure/doubles/repositories"
)

// createQueuedJob stores one suspended review Job the way the launcher does,
// and returns its name.
func createQueuedJob(t *testing.T, client *doubles.InMemoryReviewJobClient, name string) string {
	t.Helper()
	return createQueuedJobFor(t, client, name, "pr-"+name)
}

// createQueuedJobFor is createQueuedJob for a given pull request label, so a
// test can put two Jobs on one pull request.
func createQueuedJobFor(t *testing.T, client *doubles.InMemoryReviewJobClient, name, pullRequest string) string {
	t.Helper()
	return createQueuedJobWith(t, client, name, pullRequest, nil)
}

// createQueuedJobWith is createQueuedJobFor with extra annotations.
func createQueuedJobWith(
	t *testing.T, client *doubles.InMemoryReviewJobClient, name, pullRequest string, annotations map[string]any,
) string {
	t.Helper()
	all := map[string]any{
		"code-guru/provider":        "azuredevops",
		"code-guru/pull-request-id": "42",
	}
	maps.Copy(all, annotations)
	_, err := client.Create(context.Background(), map[string]any{
		"metadata": map[string]any{
			"name":        name,
			"labels":      map[string]any{"code-guru/review-job": "true", "code-guru/pull-request": pullRequest},
			"annotations": all,
		},
		"spec": map[string]any{"suspend": true},
	})
	require.NoError(t, err)
	return name
}

// runningJobs counts the Jobs holding a slot.
func runningJobs(t *testing.T, client *doubles.InMemoryReviewJobClient) int {
	t.Helper()
	jobs, err := client.List(context.Background(), "code-guru/review-job=true")
	require.NoError(t, err)
	running := 0
	for _, job := range jobs {
		if !job.Suspended() && !job.Finished() {
			running++
		}
	}
	return running
}

// recordingReporter records every failure the scheduler reports. It returns
// `err` on every call — or, when `failCalls` is set, only on that many first
// calls — and counts the calls that succeeded.
type recordingReporter struct {
	mu        sync.Mutex
	reports   []string
	err       error
	failCalls int
	succeeded int
}

func (r *recordingReporter) report(_ context.Context, job webhooks.ReviewJob, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, job.Metadata.Name+":"+reason)
	if r.err != nil && (r.failCalls == 0 || len(r.reports) <= r.failCalls) {
		return r.err
	}
	r.succeeded++
	return nil
}

func (r *recordingReporter) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reports...)
}

func TestReviewJobSchedulerReconcile(t *testing.T) {
	t.Parallel()

	t.Run("should start the oldest queued Jobs up to the cap and leave the rest queued", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		first := createQueuedJob(t, client, "job-c")
		second := createQueuedJob(t, client, "job-a")
		third := createQueuedJob(t, client, "job-b")
		scheduler := webhooks.NewReviewJobScheduler(client, 2, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then: creation order, not name order, decides who goes first.
		require.NoError(t, err)
		for name, wantSuspended := range map[string]bool{first: false, second: false, third: true} {
			job, _ := client.Job(name)
			assert.Equal(t, wantSuspended, job.Suspended(), name)
		}
	})

	t.Run("should start a queued Job only when a running one finishes", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		running := createQueuedJob(t, client, "running")
		waiting := createQueuedJob(t, client, "waiting")
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil)
		require.NoError(t, scheduler.Reconcile(context.Background()))
		job, _ := client.Job(waiting)
		require.True(t, job.Suspended(), "the cap holds while the first Job runs")

		// when
		client.Finish(running, "")
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		job, _ = client.Job(waiting)
		assert.False(t, job.Suspended())
	})

	t.Run("should never run two Jobs of one pull request at once, and move the queue past the waiting one",
		func(t *testing.T) {
			t.Parallel()

			// given: a review and a re-review of the same pull request, then another pull request.
			client := doubles.NewInMemoryReviewJobClient()
			review := createQueuedJobFor(t, client, "review", "pr-1")
			reReview := createQueuedJobFor(t, client, "re-review", "pr-1")
			other := createQueuedJobFor(t, client, "other", "pr-2")
			scheduler := webhooks.NewReviewJobScheduler(client, 3, time.Minute, nil)

			// when
			err := scheduler.Reconcile(context.Background())

			// then
			require.NoError(t, err)
			for name, wantSuspended := range map[string]bool{review: false, reReview: true, other: false} {
				job, _ := client.Job(name)
				assert.Equal(t, wantSuspended, job.Suspended(), name)
			}
			state, stateErr := scheduler.QueuePosition(context.Background(), reReview)
			require.NoError(t, stateErr)
			assert.Equal(t, webhooks.QueueState{Queued: true, Position: 1, BehindOwnPullRequest: true}, state)

			// when: the first review finishes
			client.Finish(review, "")
			require.NoError(t, scheduler.Reconcile(context.Background()))

			// then
			job, _ := client.Job(reReview)
			assert.False(t, job.Suspended(), "the re-review starts once its pull request is free")
		})

	t.Run("should skip a Job another replica changed since the listing", func(t *testing.T) {
		t.Parallel()

		// given: a stale view, as when two replicas reconcile at once —
		// the patch carries a resourceVersion that no longer matches.
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "contended")
		stale := &staleListingClient{InMemoryReviewJobClient: client, onList: func() { client.Bump(name) }}
		scheduler := webhooks.NewReviewJobScheduler(stale, 1, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err, "a Conflict is somebody else's win, not a failure")
		job, _ := client.Job(name)
		assert.True(t, job.Suspended(), "the conditional patch must not apply to a changed Job")
	})

	t.Run("should report a failed Job exactly once, across reconciles", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "doomed")
		reporter := &recordingReporter{}
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report)
		require.NoError(t, scheduler.Reconcile(context.Background()))
		client.Finish(name, "DeadlineExceeded")

		// when
		require.NoError(t, scheduler.Reconcile(context.Background()))
		require.NoError(t, scheduler.Reconcile(context.Background()))

		// then
		assert.Equal(t, []string{"doomed:DeadlineExceeded"}, reporter.all())
		job, _ := client.Job(name)
		assert.NotEmpty(t, job.Metadata.Annotations["code-guru/failure-reported"], "the claim is recorded on the Job")
	})

	t.Run("should not report a failure twice when two replicas race for it", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "raced")
		require.NoError(t, webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil).Reconcile(context.Background()))
		client.Finish(name, "BackoffLimitExceeded")
		reporter := &recordingReporter{}
		replicas := []*webhooks.ReviewJobScheduler{
			webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report),
			webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report),
		}

		// when
		var wg sync.WaitGroup
		for _, replica := range replicas {
			wg.Go(func() {
				_ = replica.Reconcile(context.Background())
			})
		}
		wg.Wait()

		// then
		assert.Len(t, reporter.all(), 1)
	})

	t.Run("should not report a Job that completed", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "fine")
		reporter := &recordingReporter{}
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report)
		require.NoError(t, scheduler.Reconcile(context.Background()))
		client.Finish(name, "")

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		assert.Empty(t, reporter.all())
	})

	t.Run("should retry a failed report once in the same pass and never on a later one", func(t *testing.T) {
		t.Parallel()

		// given: a provider outage at report time.
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "unlucky")
		reporter := &recordingReporter{err: errors.New("provider down")}
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report)
		require.NoError(t, scheduler.Reconcile(context.Background()))
		client.Finish(name, "DeadlineExceeded")

		// when
		require.NoError(t, scheduler.Reconcile(context.Background()))
		afterFirstPass := len(reporter.all())
		require.NoError(t, scheduler.Reconcile(context.Background()))

		// then
		assert.Equal(t, 2, afterFirstPass, "one retry: the report is idempotent, its notice carries the Job's name")
		assert.Len(t, reporter.all(), 2, "the claim stands, so a later pass does not report again")
	})

	t.Run("should report on the retry when only the first attempt fails", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "flaky")
		reporter := &recordingReporter{err: errors.New("timeout"), failCalls: 1}
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, reporter.report)
		require.NoError(t, scheduler.Reconcile(context.Background()))
		client.Finish(name, "BackoffLimitExceeded")

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"flaky:BackoffLimitExceeded", "flaky:BackoffLimitExceeded"}, reporter.all())
		assert.Equal(t, 1, reporter.succeeded, "the retry is what posts the notice")
	})

	t.Run("should keep starting and reporting when one start fails", func(t *testing.T) {
		t.Parallel()

		// given: one queued Job the API refuses to start, another that can
		// start, and a Job that failed.
		client := doubles.NewInMemoryReviewJobClient()
		refused := createQueuedJob(t, client, "refused")
		startable := createQueuedJob(t, client, "startable")
		failed := createQueuedJob(t, client, "failed")
		client.Finish(failed, "DeadlineExceeded")
		client.SetPatchError(
			refused,
			webhooks.NewAPIStatusErrorForTest("patch", http.StatusUnprocessableEntity, "Invalid"),
		)
		reporter := &recordingReporter{}
		scheduler := webhooks.NewReviewJobScheduler(client, 2, time.Minute, reporter.report)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.ErrorContains(t, err, "starting review job refused")
		job, _ := client.Job(startable)
		assert.False(t, job.Suspended(), "the rest of the queue is not held up")
		assert.Equal(t, []string{"failed:DeadlineExceeded"}, reporter.all())
	})

	t.Run("should offer the only slot to the next Job when the oldest one's start is refused", func(t *testing.T) {
		t.Parallel()

		// given: a cap of one, and an oldest Job an admission policy turns down
		// on every pass.
		client := doubles.NewInMemoryReviewJobClient()
		refused := createQueuedJob(t, client, "refused")
		next := createQueuedJob(t, client, "next")
		client.SetPatchError(
			refused,
			webhooks.NewAPIStatusErrorForTest("patch", http.StatusUnprocessableEntity, "Invalid"),
		)
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.ErrorContains(t, err, "starting review job refused")
		job, _ := client.Job(next)
		assert.False(t, job.Suspended(), "a refused start must not hold the only slot")
		job, _ = client.Job(refused)
		assert.True(t, job.Suspended())
		assert.Equal(t, 1, runningJobs(t, client), "the cap still holds")
	})

	t.Run("should keep the slot of a start that may have landed", func(t *testing.T) {
		t.Parallel()

		// given: a cap of one, and a start that failed without the API
		// server's answer.
		client := doubles.NewInMemoryReviewJobClient()
		unanswered := createQueuedJob(t, client, "unanswered")
		next := createQueuedJob(t, client, "next")
		client.SetPatchError(unanswered, context.DeadlineExceeded)
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.ErrorContains(t, err, "starting review job unanswered")
		job, _ := client.Job(next)
		assert.True(t, job.Suspended(), "starting another could put two Jobs in one slot")
	})

	t.Run("should surface a listing failure", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		client.ListErr = errors.New("api server unavailable")
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.ErrorContains(t, err, "listing review jobs")
	})

	t.Run("should skip the pass while another replica holds the scheduler lock", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		queued := createQueuedJob(t, client, "queued")
		locks := doubles.NewInMemoryReviewJobLocks()
		locks.Hold("review-job-scheduler")
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil).WithLocks(locks)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err, "a busy lock is not an error: the holder is doing the same work")
		job, _ := client.Job(queued)
		assert.True(t, job.Suspended())
		assert.Empty(t, locks.Unlocks, "a lock this replica never took is not released")
	})

	t.Run("should release the scheduler lock when the pass ends", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		queued := createQueuedJob(t, client, "queued")
		locks := doubles.NewInMemoryReviewJobLocks()
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil).WithLocks(locks)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		job, _ := client.Job(queued)
		assert.False(t, job.Suspended())
		assert.Equal(t, []string{"review-job-scheduler"}, locks.Unlocks)
		assert.False(t, locks.HeldWithPrefix("review-job-scheduler"))
	})

	t.Run("should keep the cap when another replica's listing orders the queue differently", func(t *testing.T) {
		t.Parallel()

		// given: replica one lists while only `first` exists. Before it acts,
		// `second` arrives with an earlier enqueue stamp — skewed clocks on
		// two creating replicas do this — and replica two reconciles. On
		// their own listings the two replicas would pick different Jobs, and
		// no write precondition stops both from starting.
		client := doubles.NewInMemoryReviewJobClient()
		createQueuedJob(t, client, "first")
		locks := doubles.NewInMemoryReviewJobLocks()
		replicaOne := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil).WithLocks(locks)
		replicaTwo := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil).WithLocks(locks)
		var interleaved atomic.Bool
		var replicaTwoErr error
		client.AfterList = func() {
			// Only replica one's listing interleaves; replica two's own
			// listing, if it gets that far, runs straight through.
			if !interleaved.CompareAndSwap(false, true) {
				return
			}
			createQueuedJobWith(t, client, "second", "pr-second",
				map[string]any{"code-guru/enqueued-at": "2025-12-31T23:00:00.000Z"})
			replicaTwoErr = replicaTwo.Reconcile(context.Background())
		}

		// when
		err := replicaOne.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		require.NoError(t, replicaTwoErr)
		assert.Equal(t, 1, runningJobs(t, client), "the cap holds across replicas")
	})

	t.Run("should order queued Jobs by their enqueue stamp before their creation time", func(t *testing.T) {
		t.Parallel()

		// given: creation times have whole-second precision on a real API
		// server; the executor's own stamp is what orders a burst.
		client := doubles.NewInMemoryReviewJobClient()
		later := createQueuedJobWith(t, client, "later", "pr-later",
			map[string]any{"code-guru/enqueued-at": "2026-01-01T00:00:01.900Z"})
		earlier := createQueuedJobWith(t, client, "earlier", "pr-earlier",
			map[string]any{"code-guru/enqueued-at": "2026-01-01T00:00:01.100Z"})
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		job, _ := client.Job(earlier)
		assert.False(t, job.Suspended(), "the first one queued starts first")
		job, _ = client.Job(later)
		assert.True(t, job.Suspended())
	})

	t.Run("should treat a cap below one as one", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "only")
		scheduler := webhooks.NewReviewJobScheduler(client, 0, time.Minute, nil)

		// when
		err := scheduler.Reconcile(context.Background())

		// then
		require.NoError(t, err)
		job, _ := client.Job(name)
		assert.False(t, job.Suspended(), "a misconfigured cap must queue reviews, never stall them all")
	})
}

func TestReviewJobSchedulerRun(t *testing.T) {
	t.Parallel()

	t.Run("should reconcile immediately on start, then stop when cancelled", func(t *testing.T) {
		t.Parallel()

		// given: a Job left queued while no server was running.
		client := doubles.NewInMemoryReviewJobClient()
		name := createQueuedJob(t, client, "left-over")
		scheduler := webhooks.NewReviewJobScheduler(client, 1, time.Hour, nil)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})

		// when
		go func() {
			scheduler.Run(ctx)
			close(done)
		}()

		// then
		require.Eventually(t, func() bool {
			job, _ := client.Job(name)
			return !job.Suspended()
		}, 5*time.Second, 10*time.Millisecond, "the first pass must not wait a whole interval")
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancellation")
		}
	})
}

// staleListingClient runs a hook after each listing, so a test can change a
// Job between the scheduler's read and its write.
type staleListingClient struct {
	*doubles.InMemoryReviewJobClient

	onList func()
}

func (c *staleListingClient) List(ctx context.Context, selector string) ([]webhooks.ReviewJob, error) {
	jobs, err := c.InMemoryReviewJobClient.List(ctx, selector)
	if c.onList != nil {
		c.onList()
	}
	return jobs, err
}
