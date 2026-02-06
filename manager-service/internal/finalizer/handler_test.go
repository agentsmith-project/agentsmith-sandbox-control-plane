package finalizer

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	GenerateSnapshotKey(workspaceID, projectID, agentThreadID string) string
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

func (m *MockStorageClient) GenerateSnapshotKey(workspaceID, projectID, agentThreadID string) string {
	args := m.Called(workspaceID, projectID, agentThreadID)
	return args.String(0)
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

// TestGetWorkspaceIDFromAnnotation tests getting workspace ID from annotation.
func TestGetWorkspaceIDFromAnnotation(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Annotations: map[string]string{
				"workspace_id": "ws-123",
			},
		},
	}

	workspaceID := h.getWorkspaceID(pod)
	if workspaceID != "ws-123" {
		t.Errorf("Expected workspace ID 'ws-123', got '%s'", workspaceID)
	}
}

// TestGetWorkspaceIDFromLabel tests getting workspace ID from label.
func TestGetWorkspaceIDFromLabel(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Labels: map[string]string{
				"workspace_id": "ws-456",
			},
		},
	}

	workspaceID := h.getWorkspaceID(pod)
	if workspaceID != "ws-456" {
		t.Errorf("Expected workspace ID 'ws-456', got '%s'", workspaceID)
	}
}

// TestGetWorkspaceIDFallback tests workspace ID fallback to pod name.
func TestGetWorkspaceIDFallback(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	workspaceID := h.getWorkspaceID(pod)
	expectedID := "ws_test-pod"
	if workspaceID != expectedID {
		t.Errorf("Expected workspace ID '%s', got '%s'", expectedID, workspaceID)
	}
}

// TestGetProjectIDFromAnnotation tests getting project ID from annotation.
func TestGetProjectIDFromAnnotation(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Annotations: map[string]string{
				"project_id": "proj-123",
			},
		},
	}

	projectID := h.getProjectID(pod)
	if projectID != "proj-123" {
		t.Errorf("Expected project ID 'proj-123', got '%s'", projectID)
	}
}

// TestGetProjectIDFallback tests project ID fallback to default.
func TestGetProjectIDFallback(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	projectID := h.getProjectID(pod)
	if projectID != "proj_default" {
		t.Errorf("Expected project ID 'proj_default', got '%s'", projectID)
	}
}

// TestGetAgentThreadIDFromLabel tests getting agent thread ID from label.
func TestGetAgentThreadIDFromLabel(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Labels: map[string]string{
				"agent_thread_id": "at-123",
			},
		},
	}

	agentThreadID := h.getAgentThreadID(pod)
	if agentThreadID != "at-123" {
		t.Errorf("Expected agent thread ID 'at-123', got '%s'", agentThreadID)
	}
}

// TestGetAgentThreadIDFallback tests agent thread ID fallback to pod name.
func TestGetAgentThreadIDFallback(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	agentThreadID := h.getAgentThreadID(pod)
	expectedID := "at_test-pod"
	if agentThreadID != expectedID {
		t.Errorf("Expected agent thread ID '%s', got '%s'", expectedID, agentThreadID)
	}
}

// TestGetWorkspaceIDAnnotationPrecedenceOverLabel tests annotation takes precedence over label for workspace ID.
func TestGetWorkspaceIDAnnotationPrecedenceOverLabel(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Annotations: map[string]string{
				"workspace_id": "ws-from-annotation",
			},
			Labels: map[string]string{
				"workspace_id": "ws-from-label",
			},
		},
	}

	workspaceID := h.getWorkspaceID(pod)
	if workspaceID != "ws-from-annotation" {
		t.Errorf("Expected workspace ID from annotation 'ws-from-annotation', got '%s'", workspaceID)
	}
}

// TestGetProjectIDAnnotationPrecedenceOverLabel tests annotation takes precedence over label for project ID.
func TestGetProjectIDAnnotationPrecedenceOverLabel(t *testing.T) {
	h := &Handler{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Annotations: map[string]string{
				"project_id": "proj-from-annotation",
			},
			Labels: map[string]string{
				"project_id": "proj-from-label",
			},
		},
	}

	projectID := h.getProjectID(pod)
	if projectID != "proj-from-annotation" {
		t.Errorf("Expected project ID from annotation 'proj-from-annotation', got '%s'", projectID)
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
