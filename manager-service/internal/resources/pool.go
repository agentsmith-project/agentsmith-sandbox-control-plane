package resources

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
)

// Metrics contains pool statistics
type Metrics struct {
	ActiveWorkers int32
	PendingJobs   int32
	CompletedJobs int64
}

// GoroutinePool manages a pool of worker goroutines with a job queue
type GoroutinePool struct {
	maxWorkers    int
	queueSize     int
	workers       []*worker
	jobQueue      chan func()
	wg            sync.WaitGroup
	metrics       Metrics
	shutdownChan  chan struct{}
	shutdownOnce  sync.Once
	shutdown      bool
}

// worker represents a single worker goroutine
type worker struct {
	pool *GoroutinePool
}

// NewGoroutinePool creates a new GoroutinePool with specified max workers and queue size
func NewGoroutinePool(maxWorkers, queueSize int) *GoroutinePool {
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU()
	}
	if queueSize <= 0 {
		queueSize = maxWorkers * 10
	}

	pool := &GoroutinePool{
		maxWorkers:   maxWorkers,
		queueSize:    queueSize,
		jobQueue:     make(chan func(), queueSize),
		shutdownChan: make(chan struct{}),
	}

	// Start workers
	pool.workers = make([]*worker, maxWorkers)
	for i := 0; i < maxWorkers; i++ {
		w := &worker{pool: pool}
		pool.workers[i] = w
		pool.wg.Add(1)
		go w.run()
	}

	return pool
}

// Submit submits a job to the pool for execution
func (p *GoroutinePool) Submit(ctx context.Context, fn func()) error {
	if p.shutdown {
		return context.Canceled
	}

	// Update metrics
	atomic.AddInt32(&p.metrics.PendingJobs, 1)

	select {
	case p.jobQueue <- fn:
		// Job queued successfully
		return nil
	case <-ctx.Done():
		// Context cancelled
		atomic.AddInt32(&p.metrics.PendingJobs, -1)
		return ctx.Err()
	case <-p.shutdownChan:
		// Pool is shutting down
		atomic.AddInt32(&p.metrics.PendingJobs, -1)
		return context.Canceled
	}
}

// Shutdown gracefully shuts down the pool
func (p *GoroutinePool) Shutdown(ctx context.Context) {
	p.shutdownOnce.Do(func() {
		p.shutdown = true
		close(p.shutdownChan)

		// Wait for workers to finish current jobs
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// All workers finished
		case <-ctx.Done():
			// Context cancelled, just continue
		}
	})
}

// GetMetrics returns current pool metrics
func (p *GoroutinePool) GetMetrics() Metrics {
	return Metrics{
		ActiveWorkers: atomic.LoadInt32(&p.metrics.ActiveWorkers),
		PendingJobs:   atomic.LoadInt32(&p.metrics.PendingJobs),
		CompletedJobs: atomic.LoadInt64(&p.metrics.CompletedJobs),
	}
}

// run is the main loop for a worker
func (w *worker) run() {
	defer w.pool.wg.Done()

	for {
		select {
		case job := <-w.pool.jobQueue:
			// Update metrics
			atomic.AddInt32(&w.pool.metrics.PendingJobs, -1)
			atomic.AddInt32(&w.pool.metrics.ActiveWorkers, 1)

			// Execute job with panic recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Job panicked, but continue processing
						atomic.AddInt64(&w.pool.metrics.CompletedJobs, 1)
					}
				}()
				job()
			}()

			// Update metrics
			atomic.AddInt32(&w.pool.metrics.ActiveWorkers, -1)
			atomic.AddInt64(&w.pool.metrics.CompletedJobs, 1)

		case <-w.pool.shutdownChan:
			// Pool is shutting down
			return
		}
	}
}