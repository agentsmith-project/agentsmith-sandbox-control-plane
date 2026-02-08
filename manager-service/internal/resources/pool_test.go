package resources

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestGoroutinePool_Basic(t *testing.T) {
	pool := NewGoroutinePool(5, 10)
	defer pool.Shutdown(context.Background())

	if pool.GetMetrics().ActiveWorkers != 0 {
		t.Errorf("Expected 0 active workers, got %d", pool.GetMetrics().ActiveWorkers)
	}
	if pool.GetMetrics().PendingJobs != 0 {
		t.Errorf("Expected 0 pending jobs, got %d", pool.GetMetrics().PendingJobs)
	}
	if pool.GetMetrics().CompletedJobs != 0 {
		t.Errorf("Expected 0 completed jobs, got %d", pool.GetMetrics().CompletedJobs)
	}
}

func TestGoroutinePool_SubmitJob(t *testing.T) {
	pool := NewGoroutinePool(2, 5)
	defer pool.Shutdown(context.Background())

	var wg sync.WaitGroup
	results := make([]int, 5)
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		jobID := i
		err := pool.Submit(context.Background(), func() {
			defer wg.Done()
			mu.Lock()
			results[jobID] = jobID * 2
			mu.Unlock()
		})
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}
	}

	// Wait with timeout to prevent test hanging
	done := make(chan bool, 1)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// All jobs completed
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for jobs to complete")
	}

	// Check results
	for i := 0; i < 5; i++ {
		if results[i] != i*2 {
			t.Errorf("Expected result %d for job %d, got %d", i*2, i, results[i])
		}
	}

	// Check metrics
	metrics := pool.GetMetrics()
	if metrics.CompletedJobs != 5 {
		t.Errorf("Expected 5 completed jobs, got %d", metrics.CompletedJobs)
	}
}

func TestGoroutinePool_ContextCancellation(t *testing.T) {
	// TODO: This test needs to be rewritten to properly test context cancellation
	// For now, we'll just test basic shutdown behavior
	ctx, cancel := context.WithCancel(context.Background())

	pool := NewGoroutinePool(1, 1)

	// Submit a job
	err := pool.Submit(ctx, func() {
		time.Sleep(50 * time.Millisecond)
	})
	if err != nil {
		t.Errorf("Submit failed: %v", err)
	}

	// Cancel context
	cancel()

	// Shutdown pool
	pool.Shutdown(context.Background())

	// Pool should be shutdown
	if err := pool.Submit(context.Background(), func() {}); err == nil {
		t.Error("Expected error after shutdown")
	}
}

func TestGoroutinePool_Shutdown(t *testing.T) {
	pool := NewGoroutinePool(2, 5)

	// Submit some jobs
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		err := pool.Submit(context.Background(), func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
		})
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}
	}

	// Shutdown should not block
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan bool)
	go func() {
		pool.Shutdown(ctx)
		done <- true
	}()

	select {
	case <-done:
		// Shutdown completed successfully
	case <-ctx.Done():
		t.Error("Shutdown timed out")
	}

	// After shutdown, Submit should fail
	err := pool.Submit(context.Background(), func() {})
	if err == nil {
		t.Error("Expected error after shutdown")
	}
}

func TestGoroutinePool_Metrics(t *testing.T) {
	pool := NewGoroutinePool(1, 2)
	defer pool.Shutdown(context.Background())

	// Submit jobs
	for i := 0; i < 3; i++ {
		err := pool.Submit(context.Background(), func() {
			time.Sleep(10 * time.Millisecond)
		})
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}
	}

	// Wait for jobs to complete
	time.Sleep(50 * time.Millisecond)

	metrics := pool.GetMetrics()
	if metrics.ActiveWorkers != 0 {
		t.Errorf("Expected 0 active workers, got %d", metrics.ActiveWorkers)
	}
	if metrics.PendingJobs != 0 {
		t.Errorf("Expected 0 pending jobs, got %d", metrics.PendingJobs)
	}
	if metrics.CompletedJobs < 3 {
		t.Errorf("Expected at least 3 completed jobs, got %d", metrics.CompletedJobs)
	}
}

func TestGoroutinePool_MaxWorkersLimit(t *testing.T) {
	pool := NewGoroutinePool(2, 5)
	defer pool.Shutdown(context.Background())

	// Submit more jobs than workers
	var wg sync.WaitGroup
	workerCounts := make(chan int, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		err := pool.Submit(context.Background(), func() {
			defer wg.Done()
			workerCounts <- runtime.NumGoroutine()
			time.Sleep(50 * time.Millisecond)
		})
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}
	}

	wg.Wait()
	close(workerCounts)

	// Check that we didn't exceed max workers
	maxWorkers := 0
	for count := range workerCounts {
		if count > maxWorkers {
			maxWorkers = count
		}
	}

	// Max workers should be reasonable (base + our 2 workers)
	if maxWorkers > 10 {
		t.Errorf("Too many goroutines: %d", maxWorkers)
	}
}

func TestGoroutinePool_QueueFull(t *testing.T) {
	pool := NewGoroutinePool(1, 2)
	defer pool.Shutdown(context.Background())

	// Fill the queue
	for i := 0; i < 2; i++ {
		err := pool.Submit(context.Background(), func() {
			time.Sleep(100 * time.Millisecond)
		})
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}
	}

	// Next submission should block or error based on implementation
	start := time.Now()
	err := pool.Submit(context.Background(), func() {
		time.Sleep(10 * time.Millisecond)
	})

	duration := time.Since(start)

	if err != nil {
		// If queue is full, we expect an error
		if err.Error() != "queue is full" {
			t.Errorf("Expected queue full error, got %v", err)
		}
	} else if duration > 10*time.Millisecond {
		// If it didn't error, it should not have waited long
		t.Errorf("Submit took too long: %v", duration)
	}
}

func TestGoroutinePool_PanicHandling(t *testing.T) {
	pool := NewGoroutinePool(1, 3)
	defer pool.Shutdown(context.Background())

	// Submit a job that panics
	err := pool.Submit(context.Background(), func() {
		panic("test panic")
	})
	if err != nil {
		t.Errorf("Submit failed: %v", err)
	}

	// Pool should continue working
	err = pool.Submit(context.Background(), func() {
		// This should still work
	})
	if err != nil {
		t.Errorf("Submit after panic failed: %v", err)
	}

	// Wait for completion
	time.Sleep(50 * time.Millisecond)

	// Metrics should still be accurate
	metrics := pool.GetMetrics()
	if metrics.CompletedJobs < 2 {
		t.Errorf("Expected 2 completed jobs, got %d", metrics.CompletedJobs)
	}
}

func TestGoroutinePool_MultipleShutdowns(t *testing.T) {
	pool := NewGoroutinePool(1, 1)

	// First shutdown
	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel1()
	pool.Shutdown(ctx1)

	// Second shutdown should not panic
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	pool.Shutdown(ctx2)
}