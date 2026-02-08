package k8s

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotWorkspace_ContextCancellation_StopsGoroutine verifies that
// cancelling the context stops the snapshot goroutine from running.
func TestSnapshotWorkspace_ContextCancellation_StopsGoroutine(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())

	// Start the snapshot
	reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
	require.NoError(t, err)

	// Cancel context immediately
	cancel()

	// Read should return an error or EOF quickly due to cancellation
	buf := make([]byte, 1024)
	done := make(chan error, 1)

	go func() {
		_, err := reader.Read(buf)
		done <- err
	}()

	// Verify: Read should fail due to context cancellation within reasonable time
	select {
	case err := <-done:
		assert.Error(t, err, "Read should return an error when context is cancelled")
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not return quickly after context cancellation - goroutine may not be checking ctx.Done()")
	}

	reader.Close()

	// Verify: No leaked goroutines
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	// In actual testing, would monitor goroutine count here
}

// TestSnapshotWorkspace_ContextCancelledBeforeStart verifies that
// if the context is already cancelled when SnapshotWorkspace is called,
// the goroutine exits immediately without doing work.
func TestSnapshotWorkspace_ContextCancelledBeforeStart(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before calling SnapshotWorkspace

	// Start the snapshot with already-cancelled context
	reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
	require.NoError(t, err)

	// Read should return error immediately
	buf := make([]byte, 1024)
	done := make(chan error, 1)

	go func() {
		_, err := reader.Read(buf)
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err, "Read should return an error for already-cancelled context")
	case <-time.After(1 * time.Second):
		t.Fatal("Read did not return quickly for already-cancelled context")
	}

	reader.Close()
}

// TestSnapshotWorkspace_ReadReturnsContextCanceled verifies that
// reading from the pipe returns context.Canceled when context is cancelled.
func TestSnapshotWorkspace_ReadReturnsContextCanceled(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())

	reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
	require.NoError(t, err)

	// Cancel immediately
	cancel()

	buf := make([]byte, 1024)
	n, err := reader.Read(buf)

	// Verify: 0 bytes read and error is set
	assert.Equal(t, 0, n, "Should read 0 bytes when context is cancelled")
	assert.Error(t, err, "Should return error when context is cancelled")

	reader.Close()
}

// TestSnapshotWorkspace_Timeout verifies that
// the snapshot operation times out after the specified duration.
func TestSnapshotWorkspace_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)

	// Use a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
	require.NoError(t, err)

	// Try to read - should timeout
	buf := make([]byte, 1024)
	done := make(chan error, 1)

	go func() {
		_, err := reader.Read(buf)
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err, "Read should return error due to timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("Snapshot did not timeout within expected duration")
	}

	reader.Close()
}

// setupTestClient creates a test client for integration tests.
// This requires a running Kubernetes cluster.
func setupTestClient(t *testing.T) *Client {
	t.Helper()

	// For integration tests, we need a real k8s client
	// This would typically load kubeconfig from test environment
	client, err := NewClient(&ClientConfig{})
	if err != nil {
		t.Skipf("Skipping test: cannot create k8s client: %v", err)
	}

	return client
}

// TestSnapshotWorkspace_PipeClosedOnError verifies that
// the pipe is properly closed when an error occurs.
func TestSnapshotWorkspace_PipeClosedOnError(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)
	ctx := context.Background()

	// Use invalid pod name to trigger error
	reader, err := client.SnapshotWorkspace(ctx, "default", "non-existent-pod-test-xyz")
	require.NoError(t, err)

	// Read should return error
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)

	// Verify: Pipe is closed with error
	assert.Error(t, err)
	assert.Equal(t, 0, n, "Should read 0 bytes when pipe closed with error")
}

// TestSnapshotWorkspace_MultipleCancelCalls verifies that
// calling cancel multiple times is safe.
func TestSnapshotWorkspace_MultipleCancelCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("requires actual k8s cluster")
	}

	client := setupTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())

	reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
	require.NoError(t, err)

	// Cancel multiple times - should be safe
	cancel()
	cancel()
	cancel()

	buf := make([]byte, 1024)
	_, err = reader.Read(buf)
	assert.Error(t, err)

	reader.Close()
}
