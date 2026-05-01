//go:build unit

package webhooks_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// fakeLeaseClient is a hand-rolled stand-in for
// `coordination/v1.LeaseInterface`. We do NOT pull in
// `client-go/kubernetes/typed/coordination/v1/fake` because that
// package drags in `client-go/testing` and the rest of the fake-action
// machinery (a heavy transitive cost for a single dedup contract).
//
// The fake holds an in-memory map keyed by lease name and exposes:
//   - the K8s API server's optimistic-concurrency contract on Create
//     (concurrent calls for the same name → exactly one succeeds, the
//     rest get `IsAlreadyExists`);
//   - configurable hook errors so the "any other error → fall back to
//     process the webhook" branch is exercisable;
//   - a call counter per lease name so tests can pin "the second
//     call did go to the API server, it just got the dup back".
type fakeLeaseClient struct {
	mu          sync.Mutex
	leases      map[string]*coordinationv1.Lease
	uidCounter  int
	createErr   error // when non-nil AND not AlreadyExists, every Create returns this
	deleteErr   error // when non-nil AND not NotFound, every Delete returns this
	getErr      error // when non-nil AND not NotFound, every Get returns this
	createCalls int
	deleteCalls int
	getCalls    int
}

func newFakeLeaseClient() *fakeLeaseClient {
	return &fakeLeaseClient{leases: map[string]*coordinationv1.Lease{}}
}

func (f *fakeLeaseClient) Create(_ context.Context, lease *coordinationv1.Lease, _ metav1.CreateOptions) (*coordinationv1.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	if _, exists := f.leases[lease.Name]; exists {
		// Mirror the real K8s API server's 409 AlreadyExists shape so
		// the production path's `apierrors.IsAlreadyExists` check
		// returns true. Building it via the typed constructor avoids
		// embedding wire bytes in the test.
		return nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			lease.Name,
		)
	}
	stored := lease.DeepCopy()
	f.uidCounter++
	stored.UID = types.UID(fmt.Sprintf("uid-%d", f.uidCounter))
	f.leases[lease.Name] = stored
	return stored, nil
}

func (f *fakeLeaseClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*coordinationv1.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	lease, ok := f.leases[name]
	if !ok {
		return nil, apierrors.NewNotFound(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			name,
		)
	}
	return lease.DeepCopy(), nil
}

func (f *fakeLeaseClient) Delete(_ context.Context, name string, opts metav1.DeleteOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	existing, ok := f.leases[name]
	if !ok {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			name,
		)
	}
	// Honour the UID precondition the takeover path sends — the real
	// API server returns 409 Conflict if the UID does not match. The
	// test surfaces this as IsConflict so the production code's
	// "another pod renewed → treat as duplicate" branch is exercised.
	if opts.Preconditions != nil && opts.Preconditions.UID != nil && *opts.Preconditions.UID != existing.UID {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"},
			name,
			fmt.Errorf("uid mismatch"),
		)
	}
	delete(f.leases, name)
	return nil
}

func (f *fakeLeaseClient) WithCreateError(err error) *fakeLeaseClient { f.createErr = err; return f }
func (f *fakeLeaseClient) WithDeleteError(err error) *fakeLeaseClient { f.deleteErr = err; return f }

// AgeAllLeases backdates every stored lease's acquire/renew time so
// the next takeover-path Get observes them as stale. Lets a test set
// up "what if pod-a crashed mid-review" without needing to know the
// hashed lease name produced by `sanitizeLeaseName`.
func (f *fakeLeaseClient) AgeAllLeases(ageSeconds int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	old := metav1.NewMicroTime(time.Now().Add(-time.Duration(ageSeconds) * time.Second))
	for _, lease := range f.leases {
		lease.Spec.AcquireTime = &old
		lease.Spec.RenewTime = &old
	}
}

func (f *fakeLeaseClient) StoredCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.leases)
}

// TestK8sLeaseDedup pins the cross-pod dedup contract. Every row
// covers a distinct outcome the K8s API server can produce on the
// `Create` (or `Delete`) call so a future refactor of the lease
// dance surfaces here before it ships to production.
func TestK8sLeaseDedup(t *testing.T) {
	t.Parallel()

	const key = "ado:abc-uuid:12345"

	t.Run("should return false when Create succeeds (this pod acquires the lease)", func(t *testing.T) {
		// given: a fresh fake with no pre-existing leases — the very
		// first webhook delivery for a PR; nobody else holds the lock.
		client := newFakeLeaseClient()
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")

		// when
		seen := dedup.SeenRecently(context.Background(), key)

		// then
		assert.False(t, seen, "first delivery must observe `not seen` and proceed to the worker")
		assert.Equal(t, 1, client.createCalls, "the production path must hit the API server exactly once on the first call")
		assert.Equal(t, 1, client.StoredCount(), "the lease must be persisted so a concurrent pod sees AlreadyExists")
	})

	t.Run("should return true when Create returns AlreadyExists and the holding lease is fresh (real cross-pod duplicate)", func(t *testing.T) {
		// given: pod-a already grabbed the lease; pod-b's webhook
		// delivery races in second — exactly the cross-pod
		// duplicate the K8s Lease backend exists to suppress.
		client := newFakeLeaseClient()
		podA := webhooks.NewK8sLeaseDedup(client, "pod-a")
		podB := webhooks.NewK8sLeaseDedup(client, "pod-b")
		require.False(t, podA.SeenRecently(context.Background(), key), "precondition: pod-a acquires first")

		// when
		dup := podB.SeenRecently(context.Background(), key)

		// then
		assert.True(t, dup, "second delivery (different pod) must observe `seen` and short-circuit")
		assert.Equal(t, 2, client.createCalls, "both pods must hit the API server (the dedup IS the API call)")
		assert.Equal(t, 1, client.getCalls, "the takeover path must Get the holding lease once to check freshness")
		assert.Equal(t, 0, client.deleteCalls, "a fresh lease must NOT be deleted — only stale leases trigger takeover")
	})

	t.Run("should take over a stale lease (previous holder crashed mid-review) and re-acquire", func(t *testing.T) {
		// given: pod-a acquired the lease and then "crashed" — we
		// simulate the crash by aging the stored lease past the
		// 300-s leaseDurationSeconds. Without takeover this would
		// block every future webhook for the PR forever because
		// Kubernetes does not auto-delete Lease objects.
		client := newFakeLeaseClient()
		podA := webhooks.NewK8sLeaseDedup(client, "pod-a-crashed")
		require.False(t, podA.SeenRecently(context.Background(), key), "precondition: pod-a acquires before crashing")
		client.AgeAllLeases(2000) // far past the 900-s freshness window

		podB := webhooks.NewK8sLeaseDedup(client, "pod-b")

		// when
		seen := podB.SeenRecently(context.Background(), key)

		// then
		assert.False(t, seen, "stale lease must be taken over — the next pod re-acquires and processes the webhook")
		assert.Equal(t, 3, client.createCalls, "podA's first acquire (1) + podB's contended Create (2) + takeover retry Create (3)")
		assert.Equal(t, 1, client.getCalls, "exactly one Get to inspect the holder's freshness")
		assert.Equal(t, 1, client.deleteCalls, "the stale lease must be Delete'd before the retry Create")
		assert.Equal(t, 1, client.StoredCount(), "after takeover, exactly one lease (the new holder's) must be persisted")
	})

	t.Run("should NOT take over a fresh lease even when Get returns it (renewer is still alive)", func(t *testing.T) {
		// given: pod-a holds a lease that is well within its
		// freshness window (acquired ~1 s ago). pod-b's takeover
		// path must observe "still fresh" and NOT delete.
		client := newFakeLeaseClient()
		podA := webhooks.NewK8sLeaseDedup(client, "pod-a")
		podB := webhooks.NewK8sLeaseDedup(client, "pod-b")
		require.False(t, podA.SeenRecently(context.Background(), key))

		// when
		seen := podB.SeenRecently(context.Background(), key)

		// then
		assert.True(t, seen, "fresh lease → real duplicate → return true")
		assert.Equal(t, 0, client.deleteCalls, "fresh leases must never be deleted by the takeover path")
	})

	t.Run("should let a forgotten key re-acquire (rollback contract for queue-full or new push)", func(t *testing.T) {
		// given: caller acquired the lease then either (a) Submit
		// failed and they want a retry, or (b) the review finished
		// successfully and a real follow-up push minutes later
		// should not be blocked by the TTL.
		client := newFakeLeaseClient()
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")
		require.False(t, dedup.SeenRecently(context.Background(), key))

		// when
		dedup.Forget(context.Background(), key)
		retry := dedup.SeenRecently(context.Background(), key)

		// then
		assert.False(t, retry, "after Forget the next delivery must be treated as fresh, not a duplicate")
		assert.Equal(t, 1, client.deleteCalls, "Forget must hit the API server (the lease is shared state)")
	})

	t.Run("should return false when Create returns a non-AlreadyExists error (best-effort fallback)", func(t *testing.T) {
		// given: the K8s API server is wedged / RBAC missing /
		// network blip — the dedup degrades to "process the
		// webhook" so the bot is never WORSE than the no-dedup
		// baseline. Using a sentinel that is neither AlreadyExists
		// nor NotFound proves the production code does not
		// accidentally swallow it as one of the two known outcomes.
		client := newFakeLeaseClient().WithCreateError(errors.New("connection refused"))
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")

		// when
		seen := dedup.SeenRecently(context.Background(), key)

		// then
		assert.False(t, seen, "transient API-server errors must fall through to processing — never worse than no dedup")
	})

	t.Run("should be a no-op when Forget hits a NotFound (idempotency for double-rollback)", func(t *testing.T) {
		// given: defensive cleanup paths invoke Forget without
		// knowing whether the key was ever recorded — typical when
		// the worker finishes after the lease has aged out via TTL
		// and the explicit Forget races the TTL eviction.
		client := newFakeLeaseClient()
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")

		// when: nothing recorded, then Forget — must not panic, must
		// not surface the underlying NotFound to the caller.
		dedup.Forget(context.Background(), "ado:never-existed:99999")

		// then: dedup remains usable for future calls
		assert.False(t, dedup.SeenRecently(context.Background(), key), "Forget on an unknown key must keep the backend operational")
	})

	t.Run("should produce distinct lease names for keys that would collide under a lossy character map", func(t *testing.T) {
		// given: two GitHub keys whose only difference is a `/` vs
		// `-` — under a naive `[^a-z0-9-] -> -` substitution they
		// both flatten to `gh-foo-bar-1`, which would make pod B's
		// dedup for `gh:foo-bar:1` silently swallow pod A's lease
		// for `gh:foo/bar:1` (or vice versa). The hash suffix
		// guarantees they map to different leases.
		client := newFakeLeaseClient()
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")

		// when: two genuinely-different-PR keys both go to
		// SeenRecently. Each must acquire its own lease — neither
		// must report `seen` because of the other.
		require.False(t, dedup.SeenRecently(context.Background(), "gh:foo/bar:1"))
		require.False(t, dedup.SeenRecently(context.Background(), "gh:foo-bar:1"))

		// then
		assert.Equal(t, 2, client.StoredCount(), "two distinct keys must produce two distinct leases — collision would only show one")
	})

	t.Run("should sanitise dedup keys into RFC 1123 lease names with the code-guru prefix", func(t *testing.T) {
		// given: ADO keys carry `:` (`ado:<uuid>:<pr_id>`) and
		// GitHub keys carry `/` (`gh:<owner>/<repo>:<pr_id>`).
		// Neither is a valid character in a K8s resource name —
		// the API server would reject the Create with `Invalid`
		// (a 400, not the AlreadyExists we rely on) and the dedup
		// would silently fall through. Pin the transformation so a
		// future "let me change the key shape" refactor surfaces
		// here before it ships.
		client := newFakeLeaseClient()
		dedup := webhooks.NewK8sLeaseDedup(client, "pod-a")

		// when
		require.False(t, dedup.SeenRecently(context.Background(), "ado:abc-uuid:12345"))
		require.False(t, dedup.SeenRecently(context.Background(), "gh:Org-Name/Repo-Name:99"))

		// then: both leases were stored under valid RFC 1123 names
		// (lowercase, alphanumeric + `-`, ≤ 253 chars, ends with
		// alphanumeric, namespaced under the `code-guru-` prefix).
		assert.Equal(t, 2, client.StoredCount(), "both keys must produce distinct, valid lease names")
		client.mu.Lock()
		defer client.mu.Unlock()
		for name := range client.leases {
			assert.True(t, strings.HasPrefix(name, "code-guru-"), "every lease must namespace under `code-guru-` so kubectl get leases distinguishes them")
			assert.LessOrEqual(t, len(name), 253, "lease names must fit RFC 1123 (≤ 253 chars)")
			assert.NotContains(t, name, ":", "colons must be transformed (API server rejects them)")
			assert.NotContains(t, name, "/", "slashes must be transformed (API server rejects them)")
			lastChar := rune(name[len(name)-1])
			assert.True(t,
				(lastChar >= 'a' && lastChar <= 'z') || (lastChar >= '0' && lastChar <= '9'),
				"lease names must end with an alphanumeric (RFC 1123)",
			)
		}
	})
}
