package repositories

import (
	"sync"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// StubWebhookSubmitter captures jobs that webhook handlers enqueue. It can be
// configured with a sticky error to simulate a saturated or closed pool.
type StubWebhookSubmitter struct {
	mu   sync.Mutex
	jobs []webhooks.Job
	err  error
}

// NewStubWebhookSubmitter returns an empty submitter ready to capture jobs.
func NewStubWebhookSubmitter() *StubWebhookSubmitter {
	return &StubWebhookSubmitter{}
}

// WithError configures the submitter to fail every Submit call with err.
func (s *StubWebhookSubmitter) WithError(err error) *StubWebhookSubmitter {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	return s
}

// Submit captures the job (or returns the configured error).
func (s *StubWebhookSubmitter) Submit(job webhooks.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.jobs = append(s.jobs, job)
	return nil
}

// Jobs returns a copy of the captured jobs.
func (s *StubWebhookSubmitter) Jobs() []webhooks.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]webhooks.Job, len(s.jobs))
	copy(out, s.jobs)
	return out
}
