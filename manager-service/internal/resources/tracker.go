package resources

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

// TrackedGoroutine represents a tracked goroutine
type TrackedGoroutine struct {
	name       string
	cancelFunc context.CancelFunc
	startTime  time.Time
}

// TrackedConnection represents a tracked connection
type TrackedConnection struct {
	id        string
	closer    io.Closer
	startTime time.Time
}

// ResourceMetrics contains metrics about tracked resources
type ResourceMetrics struct {
	Goroutines int
	Connections int
}

// ResourceTracker tracks and manages goroutines and connections
type ResourceTracker struct {
	mu            sync.RWMutex
	goroutines    map[string]*TrackedGoroutine
	connections   map[string]*TrackedConnection
	logger        observability.Logger
	shutdownOnce  sync.Once
}

// NewResourceTracker creates a new resource tracker
func NewResourceTracker(logger observability.Logger) *ResourceTracker {
	return &ResourceTracker{
		goroutines:  make(map[string]*TrackedGoroutine),
		connections: make(map[string]*TrackedConnection),
		logger:      logger,
	}
}

// TrackGoroutine tracks a goroutine with the given name and cancel function
// Returns a cleanup function that should be called when the goroutine is done
func (rt *ResourceTracker) TrackGoroutine(name string, cancel context.CancelFunc) func() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Store the goroutine
	trackedGoroutine := &TrackedGoroutine{
		name:       name,
		cancelFunc: cancel,
		startTime:  time.Now(),
	}

	// If key already exists, clean up old one first
	if existing, exists := rt.goroutines[name]; exists {
		rt.logger.Info("ResourceTracker: replacing existing goroutine %s", name)
		if existing.cancelFunc != nil {
			existing.cancelFunc()
		}
	}

	rt.goroutines[name] = trackedGoroutine
	rt.logger.Info("ResourceTracker: tracking goroutine %s (total: %d)", name, len(rt.goroutines))

	// Return cleanup function
	return func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()

		if tracked, exists := rt.goroutines[name]; exists {
			// Only remove from tracking if it's the same goroutine
			if tracked == trackedGoroutine {
				rt.logger.Info("ResourceTracker: stopped tracking goroutine %s (total: %d)", name, len(rt.goroutines))
				delete(rt.goroutines, name)
			}
		}
	}
}

// TrackConnection tracks a connection with the given ID
// Returns a closer that should be used instead of the original one
func (rt *ResourceTracker) TrackConnection(id string, conn io.Closer) (io.Closer, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Create a wrapper that cleans up when closed
	cleanupCloser := &closingCloser{
		Closer: conn,
		id:     id,
		tracker: rt,
	}

	// Store the connection
	trackedConnection := &TrackedConnection{
		id:        id,
		closer:    cleanupCloser,
		startTime: time.Now(),
	}

	// If key already exists, clean up old one first
	if existing, exists := rt.connections[id]; exists {
		rt.logger.Info("ResourceTracker: replacing existing connection %s", id)
		if existing.closer != nil {
			if err := existing.closer.Close(); err != nil {
				rt.logger.Error("ResourceTracker: error closing connection %s: %v", id, err)
			}
		}
	}

	rt.connections[id] = trackedConnection
	rt.logger.Info("ResourceTracker: tracking connection %s (total: %d)", id, len(rt.connections))

	// Return the wrapper that will clean up when closed
	return cleanupCloser, nil
}

// closingCloser wraps a closer and removes it from the tracker when closed
type closingCloser struct {
	io.Closer
	id      string
	tracker *ResourceTracker
}

func (c *closingCloser) Close() error {
	c.tracker.mu.Lock()
	defer c.tracker.mu.Unlock()

	err := c.Closer.Close()

	// Remove from tracker
	delete(c.tracker.connections, c.id)
	c.tracker.logger.Info("ResourceTracker: connection %s closed by user (remaining: %d)",
		c.id, len(c.tracker.connections))

	return err
}

// Shutdown gracefully shuts down all tracked resources
func (rt *ResourceTracker) Shutdown(ctx context.Context) error {
	var shutdownCompleted bool

	rt.shutdownOnce.Do(func() {
		rt.logger.Info("ResourceTracker: starting shutdown")

		var goroutineCount, connectionCount int

		// Cancel all goroutines
		rt.mu.Lock()
		for name, goroutine := range rt.goroutines {
			rt.logger.Info("ResourceTracker: cancelling goroutine %s", name)
			if goroutine.cancelFunc != nil {
				goroutine.cancelFunc()
			}
			delete(rt.goroutines, name)
			goroutineCount++
		}
		rt.mu.Unlock()

		// Close all connections
		rt.mu.Lock()
		for id, connection := range rt.connections {
			rt.logger.Info("ResourceTracker: closing connection %s", id)
			if connection.closer != nil {
				if closeErr := connection.closer.Close(); closeErr != nil {
					rt.logger.Error("ResourceTracker: error closing connection %s: %v", id, closeErr)
				}
			}
			delete(rt.connections, id)
			connectionCount++
		}
		rt.mu.Unlock()

		rt.logger.Info("ResourceTracker: shutdown complete (cancelled %d goroutines, closed %d connections)",
			goroutineCount, connectionCount)
		shutdownCompleted = true
	})

	if shutdownCompleted {
		return nil
	}
	return fmt.Errorf("shutdown already in progress")
}

// GetMetrics returns current resource metrics
func (rt *ResourceTracker) GetMetrics() ResourceMetrics {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	return ResourceMetrics{
		Goroutines:  len(rt.goroutines),
		Connections: len(rt.connections),
	}
}

// StartResourceCleanupTask starts a background task to clean up resources
// This can be used to periodically clean up any orphaned resources
func (rt *ResourceTracker) StartResourceCleanupTask(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			rt.logger.Info("ResourceTracker: stopping cleanup task")
			return
		case <-ticker.C:
			// In a real implementation, this could check for orphaned resources
			// For now, just log current metrics
			metrics := rt.GetMetrics()
			if metrics.Goroutines > 0 || metrics.Connections > 0 {
				rt.logger.Debug("ResourceTracker: current metrics - goroutines: %d, connections: %d",
					metrics.Goroutines, metrics.Connections)
			}
		}
	}
}