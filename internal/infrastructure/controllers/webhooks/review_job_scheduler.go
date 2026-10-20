package webhooks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	logger "github.com/sirupsen/logrus"
)

// reviewJobAPITimeout bounds the API calls of one pass's start phase, and one
// claim, so a wedged API server cannot stall the loop (or an enqueue, which
// starts queued Jobs inline). It stays well under the scheduler lock's Lease
// duration, so a pass never outlives its own lock.
const reviewJobAPITimeout = 30 * time.Second

const (
	// reviewJobReportTimeout bounds one failure report: an installation
	// token, the closed-PR check, the comment listing and the post.
	reviewJobReportTimeout = 45 * time.Second

	// reviewJobReportAttempts is how many times one report is tried.
	reviewJobReportAttempts = 2

	// reviewJobReportBudget bounds the reporting of one pass, so a burst of
	// failures cannot hold up the next pass's starts.
	reviewJobReportBudget = 2 * time.Minute
)

// ReviewJobFailureReporter tells the pull request that its review Job died
// without posting a result. `reason` is the Job's failure reason (for example
// `DeadlineExceeded`).
type ReviewJobFailureReporter func(ctx context.Context, job ReviewJob, reason string) error

// ReviewJobScheduler starts queued review Jobs as slots free up and reports
// the ones that died silently. Every server replica runs one. Their passes are
// serialised by a lock (see Reconcile), so exactly one replica plans at a
// time, from one listing; a replica that finds the lock taken skips its pass,
// because the holder's pass, or the next poll, does the same work.
type ReviewJobScheduler struct {
	client        ReviewJobClient
	maxConcurrent int
	interval      time.Duration
	report        ReviewJobFailureReporter
	now           func() time.Time

	// locks serialises passes across replicas. Nil runs every pass
	// unlocked, which is only safe with a single replica.
	locks ReviewJobLocks

	// mu serialises this replica's reconciles: the poll loop and an
	// inline enqueue reconcile would otherwise race each other on the
	// same listing and lose half their patches to their own conflicts.
	mu sync.Mutex
}

// NewReviewJobScheduler builds a scheduler. `maxConcurrent` below one is
// raised to one, so a misconfiguration queues reviews instead of never
// starting any.
func NewReviewJobScheduler(
	client ReviewJobClient, maxConcurrent int, interval time.Duration, report ReviewJobFailureReporter,
) *ReviewJobScheduler {
	return &ReviewJobScheduler{
		client:        client,
		maxConcurrent: max(maxConcurrent, 1),
		interval:      interval,
		report:        report,
		now:           time.Now,
	}
}

// WithLocks makes every pass take the scheduler lock first, which is what
// keeps the concurrency cap exact across server replicas.
func (s *ReviewJobScheduler) WithLocks(locks ReviewJobLocks) *ReviewJobScheduler {
	s.locks = locks
	return s
}

// Run reconciles every interval until ctx is cancelled. The poll is what
// starts a queued Job once a running one finishes, and what notices a Job
// that failed; enqueues reconcile inline on top of it.
//
// The first pass runs immediately, so Jobs that were queued — or died — while
// no server was running are picked up on startup rather than one interval
// later.
func (s *ReviewJobScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			logger.Warnf("review job scheduler: reconcile failed (%v); retrying in %s", err, s.interval)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// reconcilePlan is what one reconcile pass reads from one listing.
type reconcilePlan struct {
	// queue is every queued Job, oldest first.
	queue []ReviewJob
	// report are the failed Jobs nobody has reported yet.
	report []ReviewJob
	// slots counts the free slots: the cap minus the Jobs holding one.
	slots int
	// busy names the pull requests (by their label hash) with a running Job.
	busy map[string]bool
}

// planReconcile classifies a listing. A Job is RUNNING when it is neither
// suspended nor finished — whether its pod is up yet or still being
// scheduled, it holds a slot — and QUEUED when it is suspended and not
// finished.
func planReconcile(jobs []ReviewJob, maxConcurrent int) reconcilePlan {
	plan := reconcilePlan{busy: map[string]bool{}}
	running := 0
	for _, job := range jobs {
		switch {
		case job.Finished():
			if _, failed := job.FailureReason(); failed && job.Metadata.Annotations[annotationFailureReported] == "" {
				plan.report = append(plan.report, job)
			}
		case job.Suspended():
			plan.queue = append(plan.queue, job)
		default:
			plan.busy[job.Metadata.Labels[reviewJobPullRequestLabel]] = true
			running++
		}
	}
	sort.SliceStable(plan.queue, func(i, j int) bool {
		a, b := enqueuedAt(plan.queue[i]), enqueuedAt(plan.queue[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return plan.queue[i].Metadata.Name < plan.queue[j].Metadata.Name
	})
	plan.slots = max(maxConcurrent-running, 0)
	return plan
}

// startOutcome is what one attempt to release a queued Job came to.
type startOutcome int

const (
	// jobStarted means the Job holds a slot now.
	jobStarted startOutcome = iota
	// jobRefused means the API server turned the start down, so the Job holds
	// no slot and the next one in line is offered it.
	jobRefused
	// jobUnsettled means the Job may hold a slot — it changed since the
	// listing, or the request failed without the API server's answer — so the
	// slot stays taken until the next pass re-plans from a fresh listing.
	jobUnsettled
)

// offerSlots offers the plan's free slots to its queued Jobs, oldest first,
// which makes the queue first-in, first-out, and returns the Jobs left
// waiting. A Job whose pull request already has one running or starting
// waits, and the queue moves past it: two reviews of one pull request at once
// would post over each other, and the queued one (a re-review a mention asked
// for) is meant to read what the running one posts. A refused start does not
// use its slot up, so one Job the API server keeps refusing cannot hold the
// rest of the queue.
func offerSlots(plan reconcilePlan, start func(ReviewJob) startOutcome) []ReviewJob {
	free := plan.slots
	taken := map[string]bool{}
	var waiting []ReviewJob
	for _, job := range plan.queue {
		pullRequest := job.Metadata.Labels[reviewJobPullRequestLabel]
		eligible := free > 0 && !plan.busy[pullRequest] && !taken[pullRequest]
		if !eligible || start(job) == jobRefused {
			waiting = append(waiting, job)
			continue
		}
		free--
		taken[pullRequest] = true
	}
	return waiting
}

// assumeStarted is the start a planning-only walk of the queue assumes.
func assumeStarted(ReviewJob) startOutcome { return jobStarted }

// Reconcile runs one pass: release queued Jobs into free slots, then report
// failed Jobs.
func (s *ReviewJobScheduler) Reconcile(ctx context.Context) error {
	failed, err := s.startQueued(ctx)
	s.reportFailures(ctx, failed)
	return err
}

// startQueued releases queued Jobs into free slots and returns the failed Jobs
// nobody has reported yet, for the caller to report.
//
// It runs under the scheduler lock. Whether a slot is free depends on every
// Job in the listing, so two replicas planning at once from listings taken a
// moment apart could each start a different Job into the same last slot — no
// write precondition covers that. A replica that finds the lock held skips the
// pass: the holder is doing the same work, and the next poll comes round
// within one interval.
//
// Every start is still a merge patch that carries the resourceVersion of the
// listing it was decided from, so the API server applies it only if the Job
// has not changed since — the backstop for a lock that expired under a stalled
// holder. A start the API server refuses (an admission policy turning the Job
// down, say) is logged and its slot offered to the next Job in line: one bad
// Job must not keep the rest of the queue waiting. A conflict, or a failure
// that carries no answer from the API server, keeps its slot until the next
// pass, because that Job may have started after all and the cap must hold.
func (s *ReviewJobScheduler) startQueued(ctx context.Context) ([]ReviewJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.locks != nil {
		release, acquired := s.locks.TryLock(ctx, reviewJobSchedulerLock)
		if !acquired {
			logger.Debug("review job scheduler: another replica is reconciling; skipping this pass")
			return nil, nil
		}
		defer release()
	}

	ctx, cancel := context.WithTimeout(ctx, reviewJobAPITimeout)
	defer cancel()

	jobs, err := s.client.List(ctx, reviewJobSelector)
	if err != nil {
		return nil, fmt.Errorf("listing review jobs: %w", err)
	}
	plan := planReconcile(jobs, s.maxConcurrent)

	var failures []error
	offerSlots(plan, func(job ReviewJob) startOutcome {
		patch := map[string]any{
			"metadata": map[string]any{"resourceVersion": job.Metadata.ResourceVersion},
			"spec":     map[string]any{"suspend": false},
		}
		patchErr := s.client.Patch(ctx, job.Metadata.Name, patch)
		switch {
		case patchErr == nil:
			logger.Infof("review job scheduler: started %s", job.Metadata.Name)
			return jobStarted
		case isConflict(patchErr):
			return jobUnsettled
		}
		failures = append(failures, fmt.Errorf("starting review job %s: %w", job.Metadata.Name, patchErr))
		if isRefusal(patchErr) {
			return jobRefused
		}
		return jobUnsettled
	})
	return plan.report, errors.Join(failures...)
}

// reportFailures reports the given failed Jobs one after another, each with
// its own time limit, until the pass's reporting budget runs out; the Jobs
// left over stay unclaimed for the next pass. Sharing one deadline instead
// would let a burst of failures — a node drain — claim the later Jobs with no
// time left to report them, and a claimed Job is never reported again.
func (s *ReviewJobScheduler) reportFailures(ctx context.Context, jobs []ReviewJob) {
	deadline := time.Now().Add(reviewJobReportBudget)
	for i, job := range jobs {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			logger.Infof("review job scheduler: %d failed review job(s) left for the next pass", len(jobs)-i)
			return
		}
		s.reportFailure(ctx, job)
	}
}

// reportFailure claims one failed Job and tells its pull request. A claim
// that loses the race (Conflict) means another replica has it. The report
// gets its own time limit and one retry: it is idempotent, because the notice
// it posts carries the Job's name and a second attempt finds the first one's
// notice. A report that still fails is logged and not retried on a later pass,
// because the claim already stands.
func (s *ReviewJobScheduler) reportFailure(ctx context.Context, job ReviewJob) {
	reason, _ := job.FailureReason()
	claim := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": job.Metadata.ResourceVersion,
			"annotations":     map[string]any{annotationFailureReported: s.now().UTC().Format(time.RFC3339)},
		},
	}
	claimCtx, cancelClaim := context.WithTimeout(ctx, reviewJobAPITimeout)
	err := s.client.Patch(claimCtx, job.Metadata.Name, claim)
	cancelClaim()
	if err != nil {
		if !isConflict(err) {
			logger.Warnf("review job scheduler: could not claim failed job %s: %v", job.Metadata.Name, err)
		}
		return
	}
	logger.Warnf("review job scheduler: job %s failed (%s)", job.Metadata.Name, reason)
	if s.report == nil {
		return
	}
	for attempt := 1; ; attempt++ {
		reportCtx, cancelReport := context.WithTimeout(ctx, reviewJobReportTimeout)
		err = s.report(reportCtx, job, reason)
		cancelReport()
		if err == nil || attempt >= reviewJobReportAttempts || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		logger.Warnf("review job scheduler: could not report failed job %s on its pull request: %v",
			job.Metadata.Name, err)
	}
}

// enqueuedAt is when a Job was queued: the executor's own millisecond stamp,
// else the API server's creation time, which has whole-second precision and
// would let two Jobs queued within one second start in either order.
func enqueuedAt(job ReviewJob) time.Time {
	if stamped, err := time.Parse(time.RFC3339Nano, job.Metadata.Annotations[annotationEnqueuedAt]); err == nil {
		return stamped
	}
	return job.Metadata.CreationTimestamp
}

// QueueState is where a review Job stands after a reconcile.
type QueueState struct {
	// Queued is set while the Job waits.
	Queued bool
	// Position is its 1-based place among waiting Jobs when Queued.
	Position int
	// BehindOwnPullRequest is set when it waits for another Job of the same
	// pull request to finish rather than for a free slot.
	BehindOwnPullRequest bool
}

// QueuePosition reports where the named Job stands.
func (s *ReviewJobScheduler) QueuePosition(ctx context.Context, name string) (QueueState, error) {
	ctx, cancel := context.WithTimeout(ctx, reviewJobAPITimeout)
	defer cancel()

	jobs, err := s.client.List(ctx, reviewJobSelector)
	if err != nil {
		return QueueState{}, fmt.Errorf("listing review jobs: %w", err)
	}
	plan := planReconcile(jobs, s.maxConcurrent)
	// A Job this listing would release is about to start, not waiting.
	for i, job := range offerSlots(plan, assumeStarted) {
		if job.Metadata.Name == name {
			pullRequest := job.Metadata.Labels[reviewJobPullRequestLabel]
			return QueueState{Queued: true, Position: i + 1, BehindOwnPullRequest: plan.busy[pullRequest]}, nil
		}
	}
	for _, job := range jobs {
		if job.Metadata.Name == name {
			return QueueState{}, nil
		}
	}
	return QueueState{}, errors.New("the review job is not in the listing")
}
