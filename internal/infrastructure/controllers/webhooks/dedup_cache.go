package webhooks

import (
	"context"
	"sync"
	"time"
)

// WebhookDedup is the contract every dedup backend must satisfy. It
// gates webhook deliveries against duplicate processing — typically
// keyed by `provider:repo_id:pr_id` — and exposes a `Forget` rollback
// so callers can release the slot when the post-check work fails.
//
// The contract is intentionally narrow (two methods) so backends can
// be swapped without touching handler code:
//
//   - `webhookDedupCache` (in-memory) is the default for tests and
//     local single-pod deployments;
//   - `K8sLeaseDedup` (this package, see `dedup_lease.go`) is the
//     production backend on AKS where the bot runs with `replicas: 2`
//     and the K8s `Service` load-balances each ADO delivery pair to
//     a different pod (causing duplicate reviews — the original gap
//     the in-memory cache could not close).
//
// Implementations MUST be safe under concurrent calls on the same key
// from multiple goroutines (the K8s Service can produce two webhook
// handlers racing on the same PR within microseconds).
type WebhookDedup interface {
	// SeenRecently returns true when the key has already been
	// processed inside the dedup window. The first caller observes
	// false and is expected to perform the gated work; subsequent
	// callers within the window observe true and short-circuit.
	//
	// `ctx` lets the implementation bound external calls (e.g. the
	// K8s API server) so a wedged backend never stalls webhook
	// delivery beyond the caller-supplied deadline. Best-effort
	// implementations MAY return false on a backend error — that
	// degrades to "process the webhook" (the same baseline as
	// having no dedup at all), never worse.
	SeenRecently(ctx context.Context, key string) bool

	// Forget removes the record made by `SeenRecently` so a webhook
	// retry inside the dedup window is allowed onto the worker
	// queue. Callers invoke this when the work AFTER the dedup gate
	// (typically `submitter.Submit`) fails — without rollback the
	// retry would be silently dropped because the record would
	// still report the duplicate as seen. Calling `Forget` for an
	// unknown key MUST be a no-op so the contract is safe under any
	// caller order (including double-rollback in defensive cleanup
	// paths).
	Forget(ctx context.Context, key string)

	// Renew refreshes a record made by `SeenRecently` so a long-running
	// review can hold the dedup slot beyond a single freshness window.
	// The K8s-Lease backend implements this as a `Patch` on the lease's
	// `renewTime`; the in-memory backend treats it as a no-op because
	// per-pod cache entries already last for the full TTL set at
	// construction. Calling `Renew` for an unknown key MUST be a no-op
	// so a renewer that races with `Forget` cannot resurrect a released
	// slot. Implementations log renewal failures at warn but MUST NOT
	// panic — a transient backend blip must not orphan an in-flight
	// review.
	Renew(ctx context.Context, key string)
}

// webhookDedupCache is a tiny in-memory TTL cache used to short-circuit
// duplicate webhook deliveries inside a single pod. Azure DevOps fires
// both `git.pullrequest.created` AND `git.pullrequest.updated` for a
// new PR (observed live across `#NNNN`, `#NNNN`, `#NNNN` on
// `2026-05-01`); when the K8s Service routes them both to the same
// pod, the cache eats the second one. Cross-pod duplicates (each
// delivery routed to a different replica) are handled by the
// `K8sLeaseDedup` implementation in `dedup_lease.go` — the in-memory
// cache remains the default for tests and single-pod local runs.
//
// The TTL is short on purpose: any logically-duplicate delivery from
// ADO lands within milliseconds (the longest gap we observed was
// 4 seconds on PR #NNNN). A 30-second window catches every retry
// shape we have seen while keeping the risk of "user pushed a new
// commit and got dedup'd" effectively zero — real follow-up pushes
// happen minutes apart, never seconds.
//
// Eviction is lazy: each call walks the map and prunes expired
// entries. At our webhook volume (single-digit per minute) that is
// O(N) on a map of at most a few dozen entries — far cheaper than
// running a background goroutine and easier to reason about. If
// volume ever climbs into the hundreds-per-second range, swap this
// for a heap-based eviction.
type webhookDedupCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// newWebhookDedupCache returns a fresh cache with the supplied TTL.
// A zero or negative TTL disables the cache (every call returns
// "not seen") so wiring tests can construct a no-op variant.
func newWebhookDedupCache(ttl time.Duration) *webhookDedupCache {
	return &webhookDedupCache{
		seen: make(map[string]time.Time),
		ttl:  ttl,
	}
}

// seenRecently returns true when the key was recorded within the
// TTL — the caller treats that as "duplicate, skip". When the call
// returns false the cache has recorded the current timestamp, so a
// subsequent call within TTL will see the duplicate.
//
// `now` is supplied by the caller so tests can drive the cache with
// a frozen clock; production code passes `time.Now()`.
func (c *webhookDedupCache) seenRecently(key string, now time.Time) bool {
	if c.ttl <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Lazy eviction: prune anything stale before consulting the map.
	// Cheap at our volume; see the type-level doc for the trade-off.
	for k, t := range c.seen {
		if now.Sub(t) > c.ttl {
			delete(c.seen, k)
		}
	}

	if t, ok := c.seen[key]; ok && now.Sub(t) <= c.ttl {
		return true
	}
	c.seen[key] = now
	return false
}

// forget removes the key from the cache so a subsequent
// `seenRecently` returns "not seen" and the caller is allowed to
// re-record it. Used to roll back a record when the submission step
// AFTER the dedup check fails (e.g. `submitter.Submit` returns
// "queue full"); without rollback, a webhook retry inside the TTL
// would be dropped permanently because the cache would still report
// the duplicate as "seen". Calling `forget` for an unknown key is a
// no-op so the contract is safe under any caller order.
func (c *webhookDedupCache) forget(key string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.seen, key)
}

// inMemoryDedup adapts the per-pod `webhookDedupCache` to the
// `WebhookDedup` interface. Kept as a thin wrapper (rather than
// hanging the interface methods directly on `webhookDedupCache`) so
// the cache's existing two-argument test-facing API
// `seenRecently(key, now)` / `forget(key)` keeps the test-only
// frozen-clock entry point intact — re-exported via `export_test.go`
// for the six BDD rows in `dedup_cache_test.go` that were pinned in
// PR `#100`. Without the wrapper the type would expose two methods
// of the same name with different signatures, which Go will not
// compile.
type inMemoryDedup struct {
	cache *webhookDedupCache
}

// SeenRecently adapts the in-memory cache to the WebhookDedup
// interface. The context is unused (the in-memory implementation
// never blocks on external IO) — it exists on the interface only
// because the K8s-Lease backend needs it to bound API-server calls.
func (d *inMemoryDedup) SeenRecently(_ context.Context, key string) bool {
	return d.cache.seenRecently(key, time.Now())
}

// Forget adapts the in-memory cache to the WebhookDedup interface.
// The context is unused for the same reason as `SeenRecently`.
func (d *inMemoryDedup) Forget(_ context.Context, key string) {
	d.cache.forget(key)
}

// Renew is a no-op for the in-memory backend. The TTL set at construction
// already covers the worst-case review wall-time on the single-pod path
// (no cross-pod takeover to defend against), so there is nothing to
// refresh — the entry simply lasts until `Forget` removes it. The method
// exists only to satisfy the WebhookDedup contract; the K8s-Lease backend
// is the one that actually patches the lease's `renewTime`.
func (d *inMemoryDedup) Renew(_ context.Context, _ string) {}

// newInMemoryDedup is the production constructor for the in-memory
// dedup backend. It is the default the dispatcher wires when no
// cross-pod backend is supplied (local dev runs, unit tests).
func newInMemoryDedup(ttl time.Duration) *inMemoryDedup {
	return &inMemoryDedup{cache: newWebhookDedupCache(ttl)}
}
