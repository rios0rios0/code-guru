//go:build unit

package webhooks_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// These tests pin the contract of the per-pod webhook dedup cache.
// The cache is the bot's defence against ADO firing
// `git.pullrequest.created` AND `git.pullrequest.updated` for the
// same PR creation (verified live across `#NNNN`, `#NNNN`,
// `#NNNN` on `2026-05-01`); when both deliveries land on the same
// pod the cache eats the second one. Cross-pod duplicates are out
// of scope here (deployment-side fix).

func TestWebhookDedupCache_SeenRecently(t *testing.T) {
	t.Parallel()

	t.Run("should return false on the first call and record the timestamp", func(t *testing.T) {
		// given: a fresh cache with a 1-minute TTL
		cache := webhooks.NewWebhookDedupCache(time.Minute)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)

		// when
		seen := cache.SeenRecently("ado:repo-id:12345", now)

		// then
		assert.False(t, seen, "first delivery for a key must always pass through to the worker")
	})

	t.Run("should return true on a duplicate within the TTL window", func(t *testing.T) {
		// given: simulating a `pullrequest.created` followed by a
		// `pullrequest.updated` 4 seconds later — the longest gap we
		// captured in production (PR #NNNN).
		cache := webhooks.NewWebhookDedupCache(30 * time.Second)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
		_ = cache.SeenRecently("ado:repo-id:NNNN", now)

		// when
		dup := cache.SeenRecently("ado:repo-id:NNNN", now.Add(4*time.Second))

		// then
		assert.True(t, dup, "the second delivery within TTL must be treated as a duplicate")
	})

	t.Run("should refresh the timestamp and return false after the TTL has elapsed", func(t *testing.T) {
		// given: a real follow-up push happens minutes after the
		// initial review — the cache must NOT swallow it. Pin the
		// behaviour so a future "let me extend the TTL to 5 minutes"
		// refactor surfaces here before it ships.
		cache := webhooks.NewWebhookDedupCache(30 * time.Second)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
		_ = cache.SeenRecently("ado:repo-id:NNNN", now)

		// when
		later := cache.SeenRecently("ado:repo-id:NNNN", now.Add(2*time.Minute))

		// then
		assert.False(t, later, "after TTL the cache must let the next event through to the worker")
	})

	t.Run("should treat distinct keys as independent (one PR's duplicate does not block another)", func(t *testing.T) {
		// given: PR #NNNN was already enqueued
		cache := webhooks.NewWebhookDedupCache(time.Minute)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
		_ = cache.SeenRecently("ado:repo-id:NNNN", now)

		// when: PR #NNNN arrives 1 second later (different key)
		other := cache.SeenRecently("ado:repo-id:NNNN", now.Add(time.Second))

		// then
		assert.False(t, other, "different (repo, pr) pairs must not poison each other's first-call flag")
	})

	t.Run("should be a no-op when constructed with a zero or negative TTL (test hook)", func(t *testing.T) {
		// given: tests that need a permissive cache use TTL=0; pin
		// the contract so wiring tests can build a `*webhookDedupCache`
		// without affecting downstream behaviour.
		cache := webhooks.NewWebhookDedupCache(0)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)

		// when
		first := cache.SeenRecently("ado:repo-id:NNNN", now)
		second := cache.SeenRecently("ado:repo-id:NNNN", now.Add(time.Millisecond))

		// then
		assert.False(t, first, "TTL=0 must always return false (cache is disabled)")
		assert.False(t, second, "TTL=0 must keep returning false even on the immediate duplicate")
	})

	t.Run("should be safe under concurrent calls on the same key (only one wins)", func(t *testing.T) {
		// given: the worst-case race the K8s Service can produce on a
		// single pod is two webhook handler goroutines arriving for
		// the same PR within microseconds. Exactly one must observe
		// "not seen"; every other must observe "seen". `sync.Mutex`
		// inside the cache makes this true; the test pins the
		// contract so a future "let me try sync.Map for speed"
		// refactor surfaces a regression here.
		cache := webhooks.NewWebhookDedupCache(time.Minute)
		now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
		const goroutines = 50

		var notSeenCount int32
		var mu sync.Mutex
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				if !cache.SeenRecently("ado:repo-id:NNNN", now) {
					mu.Lock()
					notSeenCount++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		// then
		assert.Equal(t, int32(1), notSeenCount,
			"out of 50 racing goroutines, exactly one must observe the first-call branch")
	})
}
