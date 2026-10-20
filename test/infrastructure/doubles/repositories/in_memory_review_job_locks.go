package repositories

import (
	"context"
	"strings"
	"sync"
)

// InMemoryReviewJobLocks is an in-memory ReviewJobLocks for the review
// executor's tests: a set of held keys, shared by every scheduler and launcher
// built on it the way replicas share the cluster's Leases. A release frees a
// key only for the acquisition it came from, like the Lease-backed locks. It
// records every call, and can be told to report keys with a given prefix as
// busy for a number of attempts, the way a lock held by another replica would.
type InMemoryReviewJobLocks struct {
	mu         sync.Mutex
	held       map[string]int
	generation int
	busy       map[string]int
	TryLocks   []string
	Unlocks    []string
}

// NewInMemoryReviewJobLocks returns a lock set with nothing held.
func NewInMemoryReviewJobLocks() *InMemoryReviewJobLocks {
	return &InMemoryReviewJobLocks{held: map[string]int{}, busy: map[string]int{}}
}

// TryLock takes the key unless it is held or reported busy.
func (l *InMemoryReviewJobLocks) TryLock(_ context.Context, key string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.TryLocks = append(l.TryLocks, key)
	for prefix, attempts := range l.busy {
		if attempts > 0 && strings.HasPrefix(key, prefix) {
			l.busy[prefix] = attempts - 1
			return nil, false
		}
	}
	if _, taken := l.held[key]; taken {
		return nil, false
	}
	l.generation++
	acquisition := l.generation
	l.held[key] = acquisition
	return func() { l.release(key, acquisition) }, true
}

func (l *InMemoryReviewJobLocks) release(key string, acquisition int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Unlocks = append(l.Unlocks, key)
	if l.held[key] == acquisition {
		delete(l.held, key)
	}
}

// Hold takes the key on behalf of another replica.
func (l *InMemoryReviewJobLocks) Hold(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.generation++
	l.held[key] = l.generation
}

// BusyFor reports every key starting with prefix as held by another replica
// for the next `attempts` TryLock calls.
func (l *InMemoryReviewJobLocks) BusyFor(prefix string, attempts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.busy[prefix] = attempts
}

// HeldWithPrefix reports whether any held key starts with prefix.
func (l *InMemoryReviewJobLocks) HeldWithPrefix(prefix string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.held {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
