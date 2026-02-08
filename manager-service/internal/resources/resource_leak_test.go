package resources_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/resources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoroutineLeakDetection tests for goroutine leaks
func TestGoroutineLeakDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping leak test in short mode")
	}

	t.Run("TrackedGoroutinesAreCleanedUp", func(t *testing.T) {
		tracker := resources.NewResourceTracker()

		initialGoroutines := tracker.GetGoroutineCount()

		// Start and stop tracked goroutines
		for i := 0; i < 10; i++ {
			cleanup := tracker.TrackGoroutine("test-goroutine", time.Minute)
			go func() {
				time.Sleep(10 * time.Millisecond)
			}()
			cleanup()
		}

		// Give time for cleanup
		time.Sleep(100 * time.Millisecond)

		// Verify no goroutine leak
		finalGoroutines := tracker.GetGoroutineCount()
		assert.Equal(t, initialGoroutines, finalGoroutines,
			"Goroutine count should return to initial value")
	})

	t.Run("UntrackedGoroutinesDoNotAffectTracker", func(t *testing.T) {
		tracker := resources.NewResourceTracker()

		initialCount := tracker.GetGoroutineCount()

		// Start untracked goroutine
		done := make(chan bool)
		go func() {
			time.Sleep(50 * time.Millisecond)
			done <- true
		}()

		// Tracker should not count this
		currentCount := tracker.GetGoroutineCount()
		assert.Equal(t, initialCount, currentCount)

		<-done
	})

	t.Run("ConcurrentGoroutineTracking", func(t *testing.T) {
		tracker := resources.NewResourceTracker()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				cleanup := tracker.TrackGoroutine("concurrent-test", time.Minute)
				time.Sleep(time.Duration(n) * time.Millisecond)
				cleanup()
			}(i % 10)
		}

		wg.Wait()
		time.Sleep(100 * time.Millisecond)

		// All goroutines should be cleaned up
		assert.Equal(t, 0, tracker.GetGoroutineCount())
	})
}

// TestConnectionLeakDetection tests for connection leaks
func TestConnectionLeakDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping leak test in short mode")
	}

	t.Run("TrackedConnectionsAreCleanedUp", func(t *testing.T) {
		tracker := resources.NewResourceTracker()

		initialConnections := tracker.GetConnectionCount()

		// Create and close tracked connections
		for i := 0; i < 5; i++ {
			conn := &mockConnection{id: "conn-" + string(rune('0'+i))}
			cleanup := tracker.TrackConnection("test-connection", conn)
			cleanup()
		}

		finalConnections := tracker.GetConnectionCount()
		assert.Equal(t, initialConnections, finalConnections)
	})

	t.Run("ConnectionAutoCleanupOnContextCancel", func(t *testing.T) {
		tracker := resources.NewResourceTracker()

		ctx, cancel := context.WithCancel(context.Background())
		conn := &mockConnection{id: "auto-conn"}

		_ = tracker.TrackConnectionWithContext(ctx, "auto-test", conn)

		// Cancel context to trigger cleanup
		cancel()

		time.Sleep(100 * time.Millisecond)

		// Connection should be cleaned up
		assert.Equal(t, 0, tracker.GetConnectionCount())
	})
}

// TestGoroutinePoolLeaks tests goroutine pool for leaks
func TestGoroutinePoolLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping leak test in short mode")
	}

	t.Run("PoolDoesNotLeakWorkers", func(t *testing.T) {
		pool, err := resources.NewGoroutinePool(10, 100, time.Minute)
		require.NoError(t, err)

		// Submit many tasks
		for i := 0; i < 50; i++ {
			err := pool.Submit(func(ctx context.Context) error {
				time.Sleep(10 * time.Millisecond)
				return nil
			})
			require.NoError(t, err)
		}

		// Wait for all tasks to complete
		time.Sleep(500 * time.Millisecond)

		// Shutdown pool
		pool.Shutdown(context.Background())

		// Verify pool is stopped
		assert.True(t, pool.IsStopped())
	})

	t.Run("PoolHandlesPanics", func(t *testing.T) {
		pool, err := resources.NewGoroutinePool(2, 10, time.Minute)
		require.NoError(t, err)

		// Submit task that panics
		err = pool.Submit(func(ctx context.Context) error {
			panic("intentional panic for testing")
		})
		require.NoError(t, err)

		// Submit normal task after panic
		resultChan := make(chan bool, 1)
		err = pool.Submit(func(ctx context.Context) error {
			resultChan <- true
			return nil
		})
		require.NoError(t, err)

		// Normal task should still work
		select {
		case <-resultChan:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Pool did not recover from panic")
		}

		pool.Shutdown(context.Background())
	})
}

// TestBufferLeaks tests buffer manager for leaks
func TestBufferLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping leak test in short mode")
	}

	t.Run("UnusedBuffersAreCleanedUp", func(t *testing.T) {
		// This would require integration with buffer manager
		// For now, we test the concept
		t.Skip("Buffer manager integration test")
	})
}

// mockConnection is a mock connection for testing
type mockConnection struct {
	id     string
	closed bool
	mu     sync.Mutex
}

func (m *mockConnection) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockConnection) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
