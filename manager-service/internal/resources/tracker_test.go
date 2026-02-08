package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

// MockCloser implements io.Closer for testing
type MockCloser struct {
	closeFunc func() error
}

func (m *MockCloser) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// Test ResourceTracker initialization
func TestNewResourceTracker(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	// Verify initial state
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines, got %d", metrics.Goroutines)
	}
	if metrics.Connections != 0 {
		t.Errorf("Expected 0 connections, got %d", metrics.Connections)
	}
}

// Test TrackGoroutine functionality
func TestTrackGoroutine(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track a goroutine
	stopFunc := tracker.TrackGoroutine("test-goroutine", cancel)

	// Verify metrics
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 1 {
		t.Errorf("Expected 1 goroutine, got %d", metrics.Goroutines)
	}

	// Call stop function to cleanup
	stopFunc()

	// Verify metrics after cleanup
	metrics = tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines after cleanup, got %d", metrics.Goroutines)
	}
}

// Test TrackGoroutine with context cancellation
func TestTrackGoroutineContextCancellation(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track a goroutine
	stopFunc := tracker.TrackGoroutine("test-goroutine", cancel)

	// Cancel the context
	cancel()

	// Call stopFunc to cleanup (in real usage, the goroutine would call this when done)
	stopFunc()

	// Verify metrics
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines after cleanup, got %d", metrics.Goroutines)
	}
}

// Test TrackConnection functionality
func TestTrackConnection(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	mockCloser := &MockCloser{}

	// Track a connection
	returnedCloser, err := tracker.TrackConnection("test-connection", mockCloser)
	if err != nil {
		t.Errorf("TrackConnection failed: %v", err)
	}

	// Verify metrics
	metrics := tracker.GetMetrics()
	if metrics.Connections != 1 {
		t.Errorf("Expected 1 connection, got %d", metrics.Connections)
	}

	// Close the returned connection to trigger cleanup
	err = returnedCloser.Close()
	if err != nil {
		t.Errorf("ReturnedCloser.Close failed: %v", err)
	}

	// Verify metrics after cleanup
	metrics = tracker.GetMetrics()
	if metrics.Connections != 0 {
		t.Errorf("Expected 0 connections after cleanup, got %d", metrics.Connections)
	}
}

// Test TrackConnection error on Close
func TestTrackConnectionWithError(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	mockCloser := &MockCloser{
		closeFunc: func() error {
			return errors.New("close error")
		},
	}

	// Track a connection
	returnedCloser, err := tracker.TrackConnection("test-connection", mockCloser)
	if err != nil {
		t.Errorf("TrackConnection failed: %v", err)
	}

	// Verify metrics
	metrics := tracker.GetMetrics()
	if metrics.Connections != 1 {
		t.Errorf("Expected 1 connection, got %d", metrics.Connections)
	}

	// Close the returned connection (should log error but not panic)
	returnedCloser.Close()

	// Verify metrics after cleanup
	metrics = tracker.GetMetrics()
	if metrics.Connections != 0 {
		t.Errorf("Expected 0 connections after cleanup, got %d", metrics.Connections)
	}
}

// Test Shutdown functionality
func TestShutdown(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track a goroutine
	tracker.TrackGoroutine("test-goroutine", cancel)

	// Track a connection
	mockCloser := &MockCloser{}
	_, err := tracker.TrackConnection("test-connection", mockCloser)
	if err != nil {
		t.Errorf("TrackConnection failed: %v", err)
	}

	// Verify metrics before shutdown
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 1 {
		t.Errorf("Expected 1 goroutine before shutdown, got %d", metrics.Goroutines)
	}
	if metrics.Connections != 1 {
		t.Errorf("Expected 1 connection before shutdown, got %d", metrics.Connections)
	}

	// Shutdown tracker
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	err = tracker.Shutdown(shutdownCtx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Verify metrics after shutdown
	metrics = tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines after shutdown, got %d", metrics.Goroutines)
	}
	if metrics.Connections != 0 {
		t.Errorf("Expected 0 connections after shutdown, got %d", metrics.Connections)
	}
}

// Test concurrent access
func TestConcurrentAccess(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create multiple goroutines that track resources
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			// Track a goroutine
			stopFunc := tracker.TrackGoroutine("goroutine-"+string(rune(id)), cancel)

			// Track a connection
			mockCloser := &MockCloser{}
			returnedCloser, err := tracker.TrackConnection("connection-"+string(rune(id)), mockCloser)
			if err != nil {
				t.Errorf("TrackConnection failed: %v", err)
			}

			done <- true

			// Cleanup
			stopFunc()
			returnedCloser.Close()
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify metrics
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines after concurrent access, got %d", metrics.Goroutines)
	}
	if metrics.Connections != 0 {
		t.Errorf("Expected 0 connections after concurrent access, got %d", metrics.Connections)
	}
}

// Test duplicate resource tracking
func TestDuplicateTracking(t *testing.T) {
	logger := observability.NewDefaultLogger(true)
	tracker := NewResourceTracker(logger)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track the same goroutine multiple times
	stopFunc1 := tracker.TrackGoroutine("duplicate-goroutine", cancel)
	stopFunc2 := tracker.TrackGoroutine("duplicate-goroutine", cancel)

	// Verify metrics (should still be 1)
	metrics := tracker.GetMetrics()
	if metrics.Goroutines != 1 {
		t.Errorf("Expected 1 goroutine for duplicate tracking, got %d", metrics.Goroutines)
	}

	// Cleanup both
	stopFunc1()
	stopFunc2()

	// Verify metrics after cleanup
	metrics = tracker.GetMetrics()
	if metrics.Goroutines != 0 {
		t.Errorf("Expected 0 goroutines after duplicate cleanup, got %d", metrics.Goroutines)
	}
}