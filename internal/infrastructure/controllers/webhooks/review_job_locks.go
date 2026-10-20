package webhooks

import (
	"context"
	"errors"
	"time"

	logger "github.com/sirupsen/logrus"
)

const (
	// reviewJobSchedulerLock serialises reconcile passes across replicas.
	// The conditional patches alone only stop two replicas from starting
	// the SAME Job; whether a slot is free depends on every other Job,
	// which no precondition covers, so two replicas planning from
	// different listings at once could start one Job each into the same
	// last slot.
	reviewJobSchedulerLock = "review-job-scheduler"

	// reviewJobEnqueueLockPrefix, plus the pull request's key, serialises
	// the list-then-create of one pull request's Jobs across replicas, so
	// two deliveries for it — PR-wide mentions bypass the webhook dedup —
	// do not both find no Job and both create one.
	reviewJobEnqueueLockPrefix = "review-job-enqueue:"

	// enqueueLockWait bounds how long an enqueue waits for a concurrent
	// enqueue of the same pull request to finish. The critical section is
	// one list and one create, well under a second.
	enqueueLockWait = 10 * time.Second

	// enqueueLockRetry is the pause between attempts while waiting.
	enqueueLockRetry = 250 * time.Millisecond

	// reviewJobProbeLockPrefix, plus the replica's holder identity, names
	// the Lease the startup probe creates, reads and deletes.
	reviewJobProbeLockPrefix = "review-job-probe:"
)

// errLeaseHeld reports a lock whose Lease belongs to a live holder.
var errLeaseHeld = errors.New("the lease is held by a live holder")

// ReviewJobLocks hands out the cross-replica locks the Kubernetes executor
// needs: one around every scheduler pass and one per pull request around every
// enqueue.
type ReviewJobLocks interface {
	// TryLock takes the named lock and returns the function that releases
	// exactly that acquisition, or false at once while a live holder
	// elsewhere has it. A lock whose holder died is taken over once its
	// freshness window passes. The release is bound to the acquisition, not
	// to the name, so a holder whose lock expired under it and was taken
	// over cannot release the next holder's.
	TryLock(ctx context.Context, key string) (func(), bool)
}

// leaseReviewJobLocks implements ReviewJobLocks on `coordination.k8s.io` Lease
// objects, the ones the webhook dedup uses, under the same RBAC: a lock is a
// Lease that exists. Create acquires it, AlreadyExists means a holder has it,
// and a Lease past its freshness window — its holder died — is taken over.
//
// It differs from the dedup in the two places a lock cannot afford to be
// lenient. An API error is "not acquired", never "acquired" — the dedup's
// fallback would hand the lock to a replica that created nothing. And a
// release deletes only the Lease that acquisition created, by its UID.
type leaseReviewJobLocks struct {
	client LeaseClient
	holder string
}

// NewLeaseReviewJobLocks builds the executor's locks on the Lease API.
// `holder` is recorded on every Lease so `kubectl get leases` names the
// replica holding a lock.
func NewLeaseReviewJobLocks(client LeaseClient, holder string) ReviewJobLocks {
	return newLeaseReviewJobLocks(client, holder)
}

func newLeaseReviewJobLocks(client LeaseClient, holder string) *leaseReviewJobLocks {
	return &leaseReviewJobLocks{client: client, holder: holder}
}

// probe proves this replica may use locks at all, by creating, reading and
// deleting a Lease no other replica uses — every verb TryLock and its takeover
// need. Locks fail closed, so a server missing any of them would otherwise
// skip every scheduler pass and leave every review queued, with nothing to
// show for it but a warning per pass; the executor runs the probe at startup
// and refuses to start without it.
func (l *leaseReviewJobLocks) probe(ctx context.Context) error {
	name := sanitizeLeaseName(reviewJobProbeLockPrefix + l.holder)
	callCtx, cancel := context.WithTimeout(ctx, leaseAPITimeout)
	defer cancel()
	// A previous run of this replica may have died between the calls.
	if err := l.client.Delete(callCtx, name, ""); err != nil && !isNotFound(err) {
		return err
	}
	created, err := l.client.Create(callCtx, l.lease(name))
	if err != nil {
		return err
	}
	if _, err = l.client.Get(callCtx, name); err != nil {
		return err
	}
	return l.client.Delete(callCtx, name, created.Metadata.UID)
}

func (l *leaseReviewJobLocks) TryLock(ctx context.Context, key string) (func(), bool) {
	name := sanitizeLeaseName(key)
	callCtx, cancel := context.WithTimeout(ctx, leaseAPITimeout)
	defer cancel()

	created, err := l.client.Create(callCtx, l.lease(name))
	if isAlreadyExists(err) {
		created, err = l.takeOver(callCtx, name)
	}
	if err != nil {
		if !errors.Is(err, errLeaseHeld) {
			logger.Warnf("review job executor: could not take lock %q (%v); treating it as held", key, err)
		}
		return nil, false
	}
	uid := created.Metadata.UID
	return func() { l.release(context.WithoutCancel(ctx), key, name, uid) }, true
}

// takeOver acquires a Lease that already exists when its holder is gone: the
// Lease was deleted in the meantime, or it is past its freshness window. A
// stale Lease is deleted under its UID, so a holder that renewed it a moment
// ago keeps it.
func (l *leaseReviewJobLocks) takeOver(ctx context.Context, name string) (*Lease, error) {
	existing, err := l.client.Get(ctx, name)
	if isNotFound(err) {
		return l.client.Create(ctx, l.lease(name))
	}
	if err != nil {
		return nil, err
	}
	if !leaseExpired(existing, time.Now()) {
		return nil, errLeaseHeld
	}
	if err = l.client.Delete(ctx, name, existing.Metadata.UID); err != nil && !isNotFound(err) {
		if isConflict(err) {
			return nil, errLeaseHeld
		}
		return nil, err
	}
	created, err := l.client.Create(ctx, l.lease(name))
	if isAlreadyExists(err) {
		return nil, errLeaseHeld
	}
	return created, err
}

// release deletes the Lease one acquisition created. A Lease that expired and
// was taken over carries another UID, so the delete fails its precondition and
// the next holder keeps the lock.
func (l *leaseReviewJobLocks) release(ctx context.Context, key, name, uid string) {
	callCtx, cancel := context.WithTimeout(ctx, leaseAPITimeout)
	defer cancel()
	err := l.client.Delete(callCtx, name, uid)
	if err != nil && !isNotFound(err) && !isConflict(err) {
		logger.Warnf("review job executor: could not release lock %q (%v); it frees itself when it expires", key, err)
	}
}

// lease builds the Lease this replica creates to hold a lock.
func (l *leaseReviewJobLocks) lease(name string) *Lease {
	duration := leaseDurationSeconds
	now := MicroTime{Time: time.Now()}
	holder := l.holder
	return &Lease{
		APIVersion: leaseAPIVersion,
		Kind:       leaseKind,
		Metadata:   LeaseMeta{Name: name},
		Spec: LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &duration,
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}
}

// lockOrProceed takes the named lock, waiting up to enqueueLockWait while
// another replica holds it, and returns the function that releases it. It
// never fails: a lock that cannot be had in time — its holder died moments
// ago, or the API server is failing — is logged and the caller proceeds
// unlocked, because a rare duplicate review costs less than an unanswered
// request.
func lockOrProceed(ctx context.Context, locks ReviewJobLocks, key string) func() {
	if locks == nil {
		return func() {}
	}
	deadline := time.Now().Add(enqueueLockWait)
	for {
		if release, acquired := locks.TryLock(ctx, key); acquired {
			return release
		}
		if !time.Now().Before(deadline) {
			logger.Warnf(
				"review job executor: lock %q still held after %s; proceeding without it",
				key,
				enqueueLockWait,
			)
			return func() {}
		}
		select {
		case <-ctx.Done():
			return func() {}
		case <-time.After(enqueueLockRetry):
		}
	}
}
