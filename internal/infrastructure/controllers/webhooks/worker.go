package webhooks

import (
	"context"
	"errors"
	"sync"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"
)

// ErrPoolClosed is returned when work is submitted to a pool that has shut down.
var ErrPoolClosed = errors.New("worker pool is closed")

// ErrPoolFull is returned when the pool's queue is at capacity and cannot accept new work.
var ErrPoolFull = errors.New("worker pool queue is full")

// Job represents a single review request enqueued onto the pool.
type Job struct {
	Provider forgeEntities.ReviewProvider
	Repo     forgeEntities.Repository
	PR       forgeEntities.PullRequestDetail
	CIPassed bool
}

// JobHandler processes a single Job. Returning an error logs the failure but does not
// stop the worker.
type JobHandler func(ctx context.Context, job Job) error

// Pool is a bounded worker pool that drains review jobs concurrently.
type Pool struct {
	queue   chan Job
	handler JobHandler
	wg      sync.WaitGroup

	mu     sync.Mutex
	closed bool
}

// NewPool starts workerCount workers reading from a queue of size queueSize.
// queueSize <= 0 defaults to 100; workerCount <= 0 defaults to 1.
func NewPool(workerCount, queueSize int, handler JobHandler) *Pool {
	if queueSize <= 0 {
		queueSize = 100
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	p := &Pool{
		queue:   make(chan Job, queueSize),
		handler: handler,
	}

	for i := range workerCount {
		p.wg.Add(1)
		go p.runWorker(i)
	}
	return p
}

// Submit enqueues a job. Returns ErrPoolClosed if the pool has shut down or
// ErrPoolFull if the queue is full.
func (p *Pool) Submit(job Job) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.mu.Unlock()

	select {
	case p.queue <- job:
		return nil
	default:
		return ErrPoolFull
	}
}

// Shutdown stops accepting new work and waits for in-flight jobs to drain. If ctx
// is cancelled before workers exit, Shutdown returns the ctx error.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.queue)
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pool) runWorker(id int) {
	defer p.wg.Done()
	for job := range p.queue {
		p.handle(id, job)
	}
}

func (p *Pool) handle(id int, job Job) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("worker %d recovered from panic processing PR #%d: %v", id, job.PR.ID, r)
		}
	}()

	ctx := context.Background()
	if err := p.handler(ctx, job); err != nil {
		logger.Errorf("worker %d failed to process PR #%d: %v", id, job.PR.ID, err)
	}
}
