package webhooks

import (
	"sync"
	"time"
)

// webhookDedupCache is a tiny in-memory TTL cache used to short-circuit
// duplicate webhook deliveries inside a single pod. Azure DevOps fires
// both `git.pullrequest.created` AND `git.pullrequest.updated` for a
// new PR (observed live across `#NNNN`, `#NNNN`, `#NNNN` on
// `2026-05-01`); when the K8s Service routes them both to the same
// pod, the cache eats the second one. Cross-pod duplicates (each
// delivery routed to a different replica) are out of scope here —
// that requires either a single replica or dropping one ADO
// subscription. Both are tracked as deployment-side follow-ups.
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
