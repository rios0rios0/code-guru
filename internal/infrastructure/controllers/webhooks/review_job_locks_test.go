package webhooks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

func TestLeaseReviewJobLocks(t *testing.T) {
	t.Parallel()

	t.Run("should refuse a lock another replica holds until it is released", func(t *testing.T) {
		t.Parallel()

		// given: two replicas' locks on one cluster's Leases.
		leases := newFakeLeaseClient()
		replicaOne := webhooks.NewLeaseReviewJobLocks(leases, "pod-a")
		replicaTwo := webhooks.NewLeaseReviewJobLocks(leases, "pod-b")
		ctx := context.Background()

		// when
		release, first := replicaOne.TryLock(ctx, "review-job-scheduler")
		_, contended := replicaTwo.TryLock(ctx, "review-job-scheduler")
		release()
		_, afterRelease := replicaTwo.TryLock(ctx, "review-job-scheduler")

		// then
		assert.True(t, first)
		assert.False(t, contended, "a live holder keeps the lock")
		assert.True(t, afterRelease, "a released lock is free at once")
	})

	t.Run("should keep locks with different names independent", func(t *testing.T) {
		t.Parallel()

		// given
		locks := webhooks.NewLeaseReviewJobLocks(newFakeLeaseClient(), "pod-a")
		ctx := context.Background()

		// when
		_, scheduler := locks.TryLock(ctx, "review-job-scheduler")
		_, pullRequest := locks.TryLock(ctx, "review-job-enqueue:abc")

		// then
		assert.True(t, scheduler)
		assert.True(t, pullRequest)
	})

	t.Run("should treat an API error as not acquired", func(t *testing.T) {
		t.Parallel()

		// given: the webhook dedup degrades to "go ahead" here; a lock must not.
		leases := newFakeLeaseClient().WithCreateError(errors.New("connection refused"))
		locks := webhooks.NewLeaseReviewJobLocks(leases, "pod-a")

		// when
		_, acquired := locks.TryLock(context.Background(), "review-job-scheduler")

		// then
		assert.False(t, acquired)
	})

	t.Run("should take over a lock whose holder died", func(t *testing.T) {
		t.Parallel()

		// given: replica one took the lock and then stopped renewing it.
		leases := newFakeLeaseClient()
		replicaOne := webhooks.NewLeaseReviewJobLocks(leases, "pod-a")
		replicaTwo := webhooks.NewLeaseReviewJobLocks(leases, "pod-b")
		_, taken := replicaOne.TryLock(context.Background(), "review-job-scheduler")
		require.True(t, taken)
		leases.AgeAllLeases(3600)

		// when
		_, acquired := replicaTwo.TryLock(context.Background(), "review-job-scheduler")

		// then
		assert.True(t, acquired)
		assert.Equal(t, "pod-b", *leases.findLeaseByKey(t, "review-job-scheduler").Spec.HolderIdentity)
	})

	t.Run("should never release a lock the next holder took over, even within one replica", func(t *testing.T) {
		t.Parallel()

		// given: one goroutine's critical section outlived its Lease, and a
		// second goroutine of the SAME replica took the lock over.
		leases := newFakeLeaseClient()
		locks := webhooks.NewLeaseReviewJobLocks(leases, "pod-a")
		releaseStalled, taken := locks.TryLock(context.Background(), "review-job-enqueue:abc")
		require.True(t, taken)
		leases.AgeAllLeases(3600)
		_, takenOver := locks.TryLock(context.Background(), "review-job-enqueue:abc")
		require.True(t, takenOver)

		// when: the stalled goroutine finally releases.
		releaseStalled()

		// then
		assert.Equal(
			t,
			1,
			leases.StoredCount(),
			"the release carries the stalled acquisition's UID, which no longer matches",
		)
		_, stolen := webhooks.NewLeaseReviewJobLocks(leases, "pod-b").
			TryLock(context.Background(), "review-job-enqueue:abc")
		assert.False(t, stolen, "another replica must still find the lock held")
	})

	t.Run("should prove at startup that the replica may take locks, leaving nothing behind", func(t *testing.T) {
		t.Parallel()

		// given
		leases := newFakeLeaseClient()

		// when
		err := webhooks.ProbeReviewJobLocksForTest(leases, "pod-a")

		// then
		require.NoError(t, err)
		assert.Zero(t, leases.StoredCount())
		assert.Equal(t, 1, leases.getCalls, "takeover needs `get`, so the probe proves it too")
	})

	t.Run("should fail the startup probe when the replica may not create Leases", func(t *testing.T) {
		t.Parallel()

		// given: what a missing Lease RBAC looks like.
		leases := newFakeLeaseClient().WithCreateError(errors.New("leases is forbidden"))

		// when
		err := webhooks.ProbeReviewJobLocksForTest(leases, "pod-a")

		// then
		require.ErrorContains(t, err, "forbidden", "the executor must refuse to start rather than never schedule")
	})

	t.Run("should fail the startup probe when the replica may not read Leases", func(t *testing.T) {
		t.Parallel()

		// given
		leases := newFakeLeaseClient()
		leases.getErr = errors.New("get leases is forbidden")

		// when
		err := webhooks.ProbeReviewJobLocksForTest(leases, "pod-a")

		// then
		require.ErrorContains(t, err, "forbidden")
	})

	t.Run("should clear a probe Lease a previous run of the replica left behind", func(t *testing.T) {
		t.Parallel()

		// given
		leases := newFakeLeaseClient()
		_, leftover := webhooks.NewLeaseReviewJobLocks(leases, "pod-a").
			TryLock(context.Background(), "review-job-probe:pod-a")
		require.True(t, leftover)

		// when
		err := webhooks.ProbeReviewJobLocksForTest(leases, "pod-a")

		// then
		require.NoError(t, err)
		assert.Zero(t, leases.StoredCount())
	})
}
