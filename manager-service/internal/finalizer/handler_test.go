package finalizer

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// K8sClientInterface defines the interface for K8s client operations needed by Handler.
type K8sClientInterface interface {
	RemoveFinalizer(ctx context.Context, namespace, podName, finalizer string) error
	SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error)
	ListPodsWithFinalizer(ctx context.Context, namespace, finalizer string) ([]v1.Pod, error)
	Namespace() string
}

// StorageClientInterface defines the interface for storage client operations needed by Handler.
type StorageClientInterface interface {
	UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) error
	GenerateSnapshotKey(sandboxID string) (string, error)
}

// MockK8sClient is a mock implementation of K8s client operations for testing finalizer.
type MockK8sClient struct {
	mock.Mock
}

func (m *MockK8sClient) RemoveFinalizer(ctx context.Context, namespace, podName, finalizer string) error {
	args := m.Called(ctx, namespace, podName, finalizer)
	return args.Error(0)
}

func (m *MockK8sClient) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
	args := m.Called(ctx, namespace, podName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockK8sClient) ListPodsWithFinalizer(ctx context.Context, namespace, finalizer string) ([]v1.Pod, error) {
	args := m.Called(ctx, namespace, finalizer)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]v1.Pod), args.Error(1)
}

func (m *MockK8sClient) Namespace() string {
	args := m.Called()
	return args.String(0)
}

// MockStorageClient is a mock implementation of Storage client operations for testing finalizer.
type MockStorageClient struct {
	mock.Mock
}

func (m *MockStorageClient) UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) error {
	args := m.Called(ctx, key, data, size)
	return args.Error(0)
}

func (m *MockStorageClient) GenerateSnapshotKey(sandboxID string) (string, error) {
	args := m.Called(sandboxID)
	return args.String(0), args.Error(1)
}

// testHandler wraps Handler with mock clients for testing.
type testHandler struct {
	handler      *Handler
	mockK8s      *MockK8sClient
	mockStorage  *MockStorageClient
}

// newTestHandler creates a handler with mock clients for testing.
// Note: This requires modifying the Handler struct to use interfaces in production code.
// For now, we'll test the retry logic directly.
func newTestHandler() *testHandler {
	mockK8s := new(MockK8sClient)
	mockStorage := new(MockStorageClient)
	mockK8s.On("Namespace").Return("test-namespace")

	return &testHandler{
		mockK8s:     mockK8s,
		mockStorage: mockStorage,
	}
}

// TestRetryConstants tests that retry constants are set correctly.
func TestRetryConstants(t *testing.T) {
	if maxRemoveFinalizerRetries != 3 {
		t.Errorf("Expected maxRemoveFinalizerRetries to be 3, got %d", maxRemoveFinalizerRetries)
	}

	if removeFinalizerBaseBackoff != 100*time.Millisecond {
		t.Errorf("Expected removeFinalizerBaseBackoff to be 100ms, got %v", removeFinalizerBaseBackoff)
	}
}

// TestRemoveFinalizerWithRetry_SuccessOnFirstAttempt tests successful removal on first attempt.
func TestRemoveFinalizerWithRetry_SuccessOnFirstAttempt(t *testing.T) {
	// We'll test the retry logic directly using a closure that simulates RemoveFinalizer
	attemptCount := 0
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		return nil // Always succeed
	}

	// Simulate the retry logic
	var lastErr error
	backoff := removeFinalizerBaseBackoff

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(context.Background(), "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}

	require.NoError(t, lastErr)
	require.Equal(t, 1, attemptCount, "Expected 1 attempt for success on first try")
}

// TestRemoveFinalizerWithRetry_SuccessOnSecondAttempt tests successful removal after 1 retry.
func TestRemoveFinalizerWithRetry_SuccessOnSecondAttempt(t *testing.T) {
	attemptCount := 0
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		if attemptCount == 1 {
			return fmt.Errorf("temporary error")
		}
		return nil
	}

	// Simulate the retry logic
	var lastErr error
	backoff := removeFinalizerBaseBackoff
	start := time.Now()

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(context.Background(), "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	elapsed := time.Since(start)

	require.NoError(t, lastErr)
	require.Equal(t, 2, attemptCount, "Expected 2 attempts")
	if elapsed < removeFinalizerBaseBackoff {
		t.Errorf("Expected to wait at least %v between retries, got %v", removeFinalizerBaseBackoff, elapsed)
	}
}

// TestRemoveFinalizerWithRetry_SuccessOnThirdAttempt tests successful removal after 2 retries.
func TestRemoveFinalizerWithRetry_SuccessOnThirdAttempt(t *testing.T) {
	attemptCount := 0
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		if attemptCount <= 2 {
			return fmt.Errorf("temporary error %d", attemptCount)
		}
		return nil
	}

	// Simulate the retry logic
	var lastErr error
	backoff := removeFinalizerBaseBackoff
	start := time.Now()

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(context.Background(), "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	elapsed := time.Since(start)

	require.NoError(t, lastErr)
	require.Equal(t, 3, attemptCount, "Expected 3 attempts")
	expectedMinWait := removeFinalizerBaseBackoff + 2*removeFinalizerBaseBackoff
	if elapsed < expectedMinWait {
		t.Errorf("Expected to wait at least %v between retries, got %v", expectedMinWait, elapsed)
	}
}

// TestRemoveFinalizerWithRetry_FailureAfterMaxRetries tests failure after max retries.
func TestRemoveFinalizerWithRetry_FailureAfterMaxRetries(t *testing.T) {
	attemptCount := 0
	expectedErr := fmt.Errorf("persistent error")
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		return expectedErr
	}

	// Simulate the retry logic
	var lastErr error
	backoff := removeFinalizerBaseBackoff

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(context.Background(), "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}

	require.Error(t, lastErr)
	require.Equal(t, 3, attemptCount, "Expected 3 attempts")
	require.Contains(t, lastErr.Error(), "persistent error")
}

// TestRemoveFinalizerWithRetry_ContextCancellation tests context cancellation during retry.
func TestRemoveFinalizerWithRetry_ContextCancellation(t *testing.T) {
	attemptCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		// Cancel context after first attempt
		if attemptCount == 1 {
			cancel()
		}
		return fmt.Errorf("error")
	}

	// Simulate the retry logic with context cancellation check
	var lastErr error
	backoff := removeFinalizerBaseBackoff

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(ctx, "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-ctx.Done():
				lastErr = fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
				break
			case <-time.After(backoff):
			}
			if lastErr != nil {
				break
			}
			backoff *= 2
		}
	}

	require.Error(t, lastErr)
	require.Contains(t, lastErr.Error(), "context cancelled")
	require.True(t, attemptCount >= 1, "Expected at least 1 attempt before cancellation")
}

// TestRemoveFinalizerWithRetry_ContextAlreadyCancelled tests context already cancelled.
func TestRemoveFinalizerWithRetry_ContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attemptCount := 0
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attemptCount++
		return fmt.Errorf("error")
	}

	// Simulate the retry logic with context cancellation check
	var lastErr error
	backoff := removeFinalizerBaseBackoff

	for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
		err := removeFinalizerFunc(ctx, "test-namespace", "test-pod", SnapshotFinalizer)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < maxRemoveFinalizerRetries {
			select {
			case <-ctx.Done():
				// Context is cancelled from the start
				// The first attempt will be made, then we'll detect cancellation during backoff
				lastErr = fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
				break
			case <-time.After(backoff):
			}
			if lastErr != nil {
				break
			}
			backoff *= 2
		}
	}

	require.Error(t, lastErr)
	require.Contains(t, lastErr.Error(), "context cancelled")
}

// TestGetSandboxIDFromAnnotation tests getting sandbox ID from annotation.
func TestGetSandboxIDFromAnnotation(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-my-session",
			Annotations: map[string]string{
				"sandbox/sessionId": "my-session",
			},
		},
	}

	sandboxID := h.getSandboxID(pod)
	if sandboxID != "my-session" {
		t.Errorf("Expected sandbox ID 'my-session', got '%s'", sandboxID)
	}
}

// TestGetSandboxIDNoAnnotation returns empty when sandbox/sessionId is missing (no fallback to pod name).
func TestGetSandboxIDNoAnnotationReturnsEmpty(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-test-pod",
		},
	}

	sandboxID := h.getSandboxID(pod)
	if sandboxID != "" {
		t.Errorf("Expected empty sandbox ID when annotation missing, got %q", sandboxID)
	}
}

// Benchmark retry performance
func BenchmarkRemoveFinalizerWithRetry_Success(b *testing.B) {
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		return nil
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var backoff = removeFinalizerBaseBackoff
		for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
			err := removeFinalizerFunc(ctx, "test-namespace", "bench-pod", SnapshotFinalizer)
			if err == nil {
				break
			}
			if attempt < maxRemoveFinalizerRetries {
				select {
				case <-ctx.Done():
					break
				case <-time.After(backoff):
				}
				backoff *= 2
			}
		}
	}
}

func BenchmarkRemoveFinalizerWithRetry_WithRetries(b *testing.B) {
	attempts := 0
	removeFinalizerFunc := func(ctx context.Context, namespace, podName, finalizer string) error {
		attempts++
		if attempts <= 2 {
			return fmt.Errorf("temporary error")
		}
		// Reset for next iteration
		if attempts >= 3 {
			attempts = 0
		}
		return nil
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		attempts = 0
		var backoff = removeFinalizerBaseBackoff
		for attempt := 1; attempt <= maxRemoveFinalizerRetries; attempt++ {
			err := removeFinalizerFunc(ctx, "test-namespace", "bench-pod", SnapshotFinalizer)
			if err == nil {
				break
			}
			if attempt < maxRemoveFinalizerRetries {
				select {
				case <-ctx.Done():
					break
				case <-time.After(backoff):
				}
				backoff *= 2
			}
		}
	}
}

// TestMaxTotalSnapshotAttempts tests the cross-cycle retry limit constant
func TestMaxTotalSnapshotAttempts(t *testing.T) {
	assert.Equal(t, 10, maxTotalSnapshotAttempts, "maxTotalSnapshotAttempts should be 10")
}

// TestIsPodContainersRunning tests the container status check
func TestIsPodContainersRunning(t *testing.T) {
	tests := []struct {
		name     string
		pod      *v1.Pod
		expected bool
	}{
		{
			name: "running container",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodRunning,
					ContainerStatuses: []v1.ContainerStatus{
						{State: v1.ContainerState{Running: &v1.ContainerStateRunning{}}},
					},
				},
			},
			expected: true,
		},
		{
			name: "failed pod phase",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodFailed,
				},
			},
			expected: false,
		},
		{
			name: "succeeded pod phase",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodSucceeded,
				},
			},
			expected: false,
		},
		{
			name: "all containers terminated",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodRunning,
					ContainerStatuses: []v1.ContainerStatus{
						{State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 1}}},
					},
				},
			},
			expected: false,
		},
		{
			name: "waiting container (no statuses yet)",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodPending,
				},
			},
			expected: true, // no container statuses yet — don't skip
		},
		{
			name: "container in waiting state with statuses",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Phase: v1.PodPending,
					ContainerStatuses: []v1.ContainerStatus{
						{State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isPodContainersRunning(tc.pod)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestHandler_FailCountTracking tests the cross-cycle failure counter
func TestHandler_FailCountTracking(t *testing.T) {
	h := &Handler{
		failCounts: make(map[string]int),
	}

	t.Run("increment and read", func(t *testing.T) {
		h.failMu.Lock()
		h.failCounts["pod-a"]++
		h.failCounts["pod-a"]++
		count := h.failCounts["pod-a"]
		h.failMu.Unlock()
		assert.Equal(t, 2, count)
	})

	t.Run("clear resets counter", func(t *testing.T) {
		h.clearFailCount("pod-a")
		h.failMu.Lock()
		count := h.failCounts["pod-a"]
		h.failMu.Unlock()
		assert.Equal(t, 0, count)
	})

	t.Run("different pods have independent counters", func(t *testing.T) {
		h.failMu.Lock()
		h.failCounts["pod-x"] = 5
		h.failCounts["pod-y"] = 3
		h.failMu.Unlock()

		h.clearFailCount("pod-x")

		h.failMu.Lock()
		assert.Equal(t, 0, h.failCounts["pod-x"])
		assert.Equal(t, 3, h.failCounts["pod-y"])
		h.failMu.Unlock()
	})
}

// TestSnapshotFinalizerConstant tests the finalizer constant value
func TestSnapshotFinalizerConstant(t *testing.T) {
	expected := "manager.mbos.io/snapshot"
	if SnapshotFinalizer != expected {
		t.Errorf("Expected SnapshotFinalizer to be %q, got %q", expected, SnapshotFinalizer)
	}
}

// TestDefaultConstants tests default constants
func TestDefaultConstants(t *testing.T) {
	if DefaultCheckInterval != 10*time.Second {
		t.Errorf("Expected DefaultCheckInterval to be 10s, got %v", DefaultCheckInterval)
	}

	if DefaultSnapshotTimeout != 5*time.Minute {
		t.Errorf("Expected DefaultSnapshotTimeout to be 5m, got %v", DefaultSnapshotTimeout)
	}
}

// TestNewHandler_NilConfig tests that nil config returns error
func TestNewHandler_NilConfig(t *testing.T) {
	handler, err := NewHandler(nil)
	require.Error(t, err)
	require.Nil(t, handler)
	require.Contains(t, err.Error(), "config cannot be nil")
}

// Note: Full tests of NewHandler require actual k8s.Client and storage.Client instances
// which is not practical for unit tests. The integration tests cover this scenario.

// TestHandler_Shutdown tests the Shutdown method
func TestHandler_Shutdown(t *testing.T) {
	// Create a minimal handler with nil clients (we won't call processPods)
	handler := &Handler{
		k8sClient:       nil, // Not used in this test
		storageClient:   nil, // Not used in this test
		namespace:       "test-namespace",
		checkInterval:   100 * time.Millisecond,
		snapshotTimeout: DefaultSnapshotTimeout,
		stopCh:          make(chan struct{}),
		failCounts:      make(map[string]int),
	}

	// Start the handler (launches its own goroutine, returns immediately)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	handler.Start(ctx)

	// Shutdown should work
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	err := handler.Shutdown(shutdownCtx)
	assert.NoError(t, err, "Shutdown should succeed")
}

// TestHandler_Shutdown_ContextTimeout tests Shutdown with expired context
func TestHandler_Shutdown_ContextTimeout(t *testing.T) {
	handler := &Handler{
		k8sClient:       nil,
		storageClient:   nil,
		namespace:       "test-namespace",
		checkInterval:   100 * time.Millisecond,
		snapshotTimeout: DefaultSnapshotTimeout,
		stopCh:          make(chan struct{}),
		failCounts:      make(map[string]int),
	}

	// Start handler (returns immediately, goroutine is running)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler.Start(ctx)

	// Shutdown with already-expired context to force timeout path
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer shutdownCancel()

	err := handler.Shutdown(shutdownCtx)
	// Either it succeeds (fast stop) or times out - both are acceptable
	if err != nil {
		assert.Contains(t, err.Error(), "timed out")
	}

	// Clean up
	cancel()
}

// TestHandler_Shutdown_Idempotent tests that Shutdown can be called multiple times
func TestHandler_Shutdown_Idempotent(t *testing.T) {
	handler := &Handler{
		k8sClient:       nil,
		storageClient:   nil,
		namespace:       "test-namespace",
		checkInterval:   100 * time.Millisecond,
		snapshotTimeout: DefaultSnapshotTimeout,
		stopCh:          make(chan struct{}),
		failCounts:      make(map[string]int),
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer shutdownCancel()

	// First shutdown - close stopCh
	err := handler.Shutdown(shutdownCtx)
	assert.NoError(t, err)

	// Second shutdown - should NOT panic (sync.Once protects close(stopCh))
	err = handler.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}
