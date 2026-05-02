//go:build unit

package webhooks_test

import (
	"context"
	"sync"
	"testing"
	"time"

	configEntities "github.com/rios0rios0/gitforge/pkg/config/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// recordingDedup is a hand-rolled WebhookDedup stub that records every
// call so the dispatcher-level tests can pin (1) which keys reach the
// backend, (2) the renewal cadence, and (3) the mass-release contract.
// Mutex-guarded because the renewal goroutine fires from a background
// context.
type recordingDedup struct {
	mu         sync.Mutex
	seen       []string
	forgotten  []string
	renewed    []string
	seenResult bool // what SeenRecently returns; default false (acquire)
}

func newRecordingDedup() *recordingDedup { return &recordingDedup{} }

func (r *recordingDedup) SeenRecently(_ context.Context, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, key)
	return r.seenResult
}

func (r *recordingDedup) Forget(_ context.Context, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgotten = append(r.forgotten, key)
}

func (r *recordingDedup) Renew(_ context.Context, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewed = append(r.renewed, key)
}

func (r *recordingDedup) snapshot() (seen, forgotten, renewed []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...),
		append([]string(nil), r.forgotten...),
		append([]string(nil), r.renewed...)
}

func newDispatcherWithDedup(t *testing.T, dedup webhooks.WebhookDedup) *webhooks.Dispatcher {
	t.Helper()
	settings := &entities.Settings{
		Providers: []configEntities.ProviderConfig{{Type: "azuredevops", Token: "tok"}},
	}
	d, _ := newDispatcherWithSettings(t, settings)
	d.SetDedup(dedup)
	return d
}

func TestRenewDedupLoopExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	t.Run("should stop calling Renew once the job context is cancelled", func(t *testing.T) {
		t.Parallel()

		// given: a dispatcher wired with the recording dedup stub. The
		// loop's Renew cadence is `dedupRenewInterval` (30 s in
		// production) — too long to wait in a unit test, so the test
		// proves the cancel-stop contract instead of pinning the
		// cadence value (the cadence is exported via
		// DedupRenewIntervalForTest for the invariant test).
		dedup := newRecordingDedup()
		d := newDispatcherWithDedup(t, dedup)

		// when: kick off the loop, then cancel before the first tick.
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			d.RenewDedup(ctx, "ado:repo-1:42")
			close(done)
		}()
		cancel()

		// then: the loop returns within the test's timeout (cancel
		// took effect) and Renew was never called.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("RenewDedup did not return after ctx cancel")
		}
		_, _, renewed := dedup.snapshot()
		assert.Empty(t, renewed, "loop must not Renew before the first tick")
	})

	t.Run("should be a no-op for an empty key", func(t *testing.T) {
		t.Parallel()

		// given
		dedup := newRecordingDedup()
		d := newDispatcherWithDedup(t, dedup)

		// when: an empty DedupKey on a Job (e.g. tests that build a
		// Job by hand without going through the handlers) must not
		// panic and must not start a loop.
		require.NotPanics(t, func() {
			d.RenewDedup(context.Background(), "")
		})

		// then
		_, _, renewed := dedup.snapshot()
		assert.Empty(t, renewed)
	})
}

func TestReleaseAllInFlight(t *testing.T) {
	t.Parallel()

	t.Run("should be a no-op when no keys are in flight", func(t *testing.T) {
		t.Parallel()

		// given
		dedup := newRecordingDedup()
		d := newDispatcherWithDedup(t, dedup)

		// when
		d.ReleaseAllInFlight(context.Background())

		// then
		_, forgotten, _ := dedup.snapshot()
		assert.Empty(t, forgotten, "ReleaseAllInFlight on an empty set must be a no-op")
	})

	t.Run("should Forget every in-flight key", func(t *testing.T) {
		t.Parallel()

		// given: populate the in-flight set via the test-only
		// `MarkInFlightForTest` helper (production code populates it
		// from `dedupSeen` after `SeenRecently` returns false). Three
		// distinct keys cover the iteration path so a future implementer
		// who switches the underlying map for an ordered slice or a
		// channel still hits the "every key" assertion.
		dedup := newRecordingDedup()
		d := newDispatcherWithDedup(t, dedup)
		d.MarkInFlightForTest("ado:repo-1:42")
		d.MarkInFlightForTest("ado:repo-1:43")
		d.MarkInFlightForTest("gh:org/repo:7")

		// when
		d.ReleaseAllInFlight(context.Background())

		// then
		_, forgotten, _ := dedup.snapshot()
		assert.ElementsMatch(t,
			[]string{"ado:repo-1:42", "ado:repo-1:43", "gh:org/repo:7"},
			forgotten,
			"every in-flight key must reach the dedup backend's Forget")
	})

	t.Run("should clear the set so a second release is a no-op", func(t *testing.T) {
		t.Parallel()

		// given
		dedup := newRecordingDedup()
		d := newDispatcherWithDedup(t, dedup)
		d.MarkInFlightForTest("ado:repo-1:42")

		// when
		d.ReleaseAllInFlight(context.Background())
		d.ReleaseAllInFlight(context.Background())

		// then: the first release Forgets once; the second is a no-op
		// because the set was drained under the lock.
		_, forgotten, _ := dedup.snapshot()
		assert.Equal(t, []string{"ado:repo-1:42"}, forgotten,
			"the set must be drained on the first release so the second is a no-op")
	})
}

func TestRenewIntervalInvariantPreCheck(t *testing.T) {
	t.Parallel()

	// Pin the dispatcher-level cadence relationship: the renewal
	// interval the loop uses MUST stay strictly less than the K8s lease
	// freshness window minus one API timeout, otherwise an in-flight
	// review's own lease could expire between two renew ticks. The
	// stricter invariant lives in dedup_lease_test (it ties the
	// constants together); this row is the dispatcher-side guard so a
	// future refactor that splits the constants by package surfaces
	// here too.
	t.Run("should keep dedupRenewInterval below the lease freshness window", func(t *testing.T) {
		t.Parallel()

		// given / when
		dispatcherInterval := webhooks.DedupRenewIntervalForTest
		leaseDuration := time.Duration(webhooks.LeaseDurationSecondsForTest) * time.Second

		// then
		assert.Less(t, dispatcherInterval, leaseDuration,
			"dedupRenewInterval must be < leaseDurationSeconds so a successful renewal lands inside the freshness window")
	})
}
