package repositories

import (
	"context"
	"sync"
)

// StubWebhookDedup is a recording WebhookDedup that captures every
// SeenRecently / Forget / Renew call so dispatcher-level tests can pin
// (1) which keys reach the backend, (2) the renewal cadence, and (3)
// the mass-release contract. Lives next to StubWebhookSubmitter under
// test/infrastructure/doubles/repositories/ per the project's
// convention for infrastructure-layer doubles — kept out of the
// production webhooks package so the WebhookDedup interface, defined
// in dedup_cache.go, can have a single shared implementation across
// the test files that need it.
//
// Mutex-guarded because the dispatcher's renewal goroutine fires from
// a background context that may race with the test goroutine reading
// the recorded slices.
type StubWebhookDedup struct {
	mu         sync.Mutex
	seen       []string
	forgotten  []string
	renewed    []string
	seenResult bool // what SeenRecently returns; default false (acquire)
}

// NewStubWebhookDedup returns an empty dedup stub. The default
// SeenRecently result is false — i.e. every key is treated as
// freshly acquired. Tests that want to simulate a duplicate delivery
// flip the field via WithSeenResult before driving the handler.
func NewStubWebhookDedup() *StubWebhookDedup {
	return &StubWebhookDedup{}
}

// WithSeenResult configures what SeenRecently returns. The chained
// shape mirrors StubWebhookSubmitter.WithError so the doubles in this
// package read consistently at call sites.
func (s *StubWebhookDedup) WithSeenResult(result bool) *StubWebhookDedup {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenResult = result
	return s
}

// SeenRecently records the call and returns the configured result.
// Context is unused — the test stub never blocks on external IO.
func (s *StubWebhookDedup) SeenRecently(_ context.Context, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, key)
	return s.seenResult
}

// Forget records the call. The WebhookDedup contract has no error
// return on Forget so the stub neither stores nor surfaces one.
func (s *StubWebhookDedup) Forget(_ context.Context, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgotten = append(s.forgotten, key)
}

// Renew records the call. The WebhookDedup contract pins Renew as a
// best-effort operation that never panics; the stub matches by simply
// recording the call.
func (s *StubWebhookDedup) Renew(_ context.Context, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewed = append(s.renewed, key)
}

// Snapshot returns copies of the recorded call lists so a test can
// assert call ordering and counts without holding the stub's mutex.
// Returning copies (rather than the live slices) keeps the assertion
// safe under a renewal goroutine that may still be ticking. Return
// order is `(seen, forgotten, renewed)` — callers typically destructure
// with positional names.
func (s *StubWebhookDedup) Snapshot() ([]string, []string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...),
		append([]string(nil), s.forgotten...),
		append([]string(nil), s.renewed...)
}
