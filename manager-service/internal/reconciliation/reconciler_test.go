package reconciliation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/session"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MockK8sClient implements a mock K8s client for testing
type MockK8sClient struct {
	pods         []*v1.Pod
	listPodsErr  error
	deletePodErr map[string]error
}

func (m *MockK8sClient) ListSandboxPods(ctx context.Context) ([]*v1.Pod, error) {
	return m.pods, m.listPodsErr
}

func (m *MockK8sClient) DeletePod(ctx context.Context, name string, gracePeriodSeconds int64) error {
	if err, exists := m.deletePodErr[name]; exists {
		return err
	}
	return nil
}

func (m *MockK8sClient) CheckReady(ctx context.Context) error {
	return nil
}

func (m *MockK8sClient) Clientset() interface{} {
	return nil
}

func (m *MockK8sClient) Config() interface{} {
	return nil
}

func (m *MockK8sClient) Namespace() string {
	return "test-namespace"
}

func (m *MockK8sClient) CreateNamespace(ctx context.Context, name string, labels map[string]string) error {
	return nil
}

func (m *MockK8sClient) Retry(ctx context.Context, fn func() error) error {
	return fn()
}

func (m *MockK8sClient) WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}

func (m *MockK8sClient) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
	for _, pod := range m.pods {
		if pod.Name == name {
			return pod, nil
		}
	}
	return nil, nil
}

func (m *MockK8sClient) GetPodBySessionID(ctx context.Context, sessionID string) (*v1.Pod, error) {
	for _, pod := range m.pods {
		if k8s.GetSessionIDFromPod(pod) == sessionID {
			return pod, nil
		}
	}
	return nil, nil
}

func (m *MockK8sClient) PodExists(ctx context.Context, name string) (bool, error) {
	for _, pod := range m.pods {
		if pod.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (m *MockK8sClient) WaitForPodReady(ctx context.Context, name string, waitTime time.Duration, pollInterval time.Duration) (bool, error) {
	return false, nil
}

func (m *MockK8sClient) CreatePod(ctx context.Context, spec *k8s.PodSpec) (*k8s.PodResult, error) {
	return &k8s.PodResult{}, nil
}

func (m *MockK8sClient) PatchActivity(ctx context.Context, name string, ttlSeconds int) error {
	return nil
}

func (m *MockK8sClient) PatchActivityBySessionID(ctx context.Context, sessionID string, ttlSeconds int) error {
	return nil
}

func (m *MockK8sClient) DeletePodBySessionID(ctx context.Context, sessionID string, gracePeriodSeconds int64) error {
	return nil
}

func TestReconciler_cleanupOrphanedPods(t *testing.T) {
	ctx := context.Background()

	// Create orphaned pods (not associated with any session)
	orphanedPod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphaned-pod1",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "sandbox",
			},
			Annotations: map[string]string{
				"sandbox/sessionId": "orphaned-session-1",
			},
		},
	}
	orphanedPod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphaned-pod2",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "sandbox",
			},
			Annotations: map[string]string{
				"sandbox/sessionId": "orphaned-session-2",
			},
		},
	}

	// Create valid session pods
	sessionPod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "session-pod1",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "sandbox",
			},
			Annotations: map[string]string{
				"sandbox/sessionId": "valid-session-1",
			},
		},
	}
	sessionPod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "session-pod2",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"app": "sandbox",
			},
			Annotations: map[string]string{
				"sandbox/sessionId": "valid-session-2",
			},
		},
	}

	// Mock K8s client
	var deletedPods []string
	mockK8s := &MockK8sClient{
		pods: []*v1.Pod{orphanedPod1, orphanedPod2, sessionPod1, sessionPod2},
		deletePodErr: map[string]error{
			"orphaned-pod1": nil,
			"orphaned-pod2": nil,
		},
	}

	// Create a new DeletePod method that tracks deletions
	originalDeletePod := mockK8s.DeletePod
	mockK8s.DeletePod = func(ctx context.Context, name string, gracePeriodSeconds int64) error {
		deletedPods = append(deletedPods, name)
		originalDeletePod(ctx, name, gracePeriodSeconds)
		return originalDeletePod(ctx, name, gracePeriodSeconds)
	}

	// Create reconciler
	sessionMgr := session.NewManager()
	reconciler := NewReconciler(sessionMgr, mockK8s)

	// Test cleanup
	err := reconciler.cleanupOrphanedPods(ctx)
	if err != nil {
		t.Errorf("cleanupOrphanedPods failed: %v", err)
	}

	// Check that orphaned pods were deleted
	if len(deletedPods) != 2 {
		t.Errorf("Expected 2 orphaned pods to be deleted, got %d", len(deletedPods))
	}

	// Verify valid session pods were not deleted
	sessionPodNames := []string{"session-pod1", "session-pod2"}
	for _, podName := range sessionPodNames {
		found := false
		for _, deletedPod := range deletedPods {
			if deletedPod == podName {
				found = true
				break
			}
		}
		if found {
			t.Errorf("Session pod %s was incorrectly deleted", podName)
		}
	}
}

func TestReconciler_cleanupOrphanedBuffers(t *testing.T) {
	ctx := context.Background()

	// Create sessions
	sessionMgr := session.NewManager()
	s1, _ := sessionMgr.Create(ctx, session.CreateRequest{
		AgentThreadID: "session1",
	})
	s2, _ := sessionMgr.Create(ctx, session.CreateRequest{
		AgentThreadID: "session2",
	})

	// Create buffer manager
	bufferMgr := buffer.NewManager()

	// Create buffers for existing sessions
	bufferMgr.GetOrCreate(s1.AgentThreadID)
	bufferMgr.GetOrCreate(s2.AgentThreadID)

	// Create orphaned buffers (not associated with any session)
	bufferMgr.GetOrCreate("orphaned-buffer1")
	bufferMgr.GetOrCreate("orphaned-buffer2")

	// Mock K8s client
	mockK8s := &MockK8sClient{
		pods: []*v1.Pod{},
	}

	// Create reconciler
	reconciler := NewReconciler(sessionMgr, bufferMgr, mockK8s)

	// Test cleanup
	err := reconciler.cleanupOrphanedBuffers(ctx)
	if err != nil {
		t.Errorf("cleanupOrphanedBuffers failed: %v", err)
	}

	// Verify that orphaned buffers were deleted by checking what's left
	remainingBuffers := bufferMgr.List()
	expectedBuffers := []string{"session1", "session2"}

	if len(remainingBuffers) != 2 {
		t.Errorf("Expected 2 remaining buffers, got %d", len(remainingBuffers))
	}

	for _, buf := range remainingBuffers {
		if buf != "session1" && buf != "session2" {
			t.Errorf("Unexpected buffer found: %s", buf)
		}
	}
}

func TestReconciler_Start(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create reconciler
	sessionMgr := session.NewManager()
	mockK8s := &MockK8sClient{
		pods: []*v1.Pod{},
	}

	reconciler := NewReconciler(sessionMgr, mockK8s)

	// Start reconciler
	reconciler.Start(ctx)

	// Check that it's running
	if !reconciler.GetStatus() {
		t.Error("Reconciler should be running")
	}

	// Let it run for a short time
	time.Sleep(100 * time.Millisecond)

	// Stop reconciler
	reconciler.Stop()

	// Check that it's stopped
	if reconciler.GetStatus() {
		t.Error("Reconciler should be stopped")
	}
}

func TestReconciler_CleanupWithErrors(t *testing.T) {
	ctx := context.Background()

	// Create session
	sessionMgr := session.NewManager()
	s1, _ := sessionMgr.Create(ctx, session.CreateRequest{
		AgentThreadID: "session1",
		PodNamespace:  "test-namespace",
	})
	s1.PodName = "pod1"

	// Mock K8s client that returns error
	mockK8s := &MockK8sClient{
		pods: []*v1.Pod{},
		deletePodErr: map[string]error{
			"any-pod": fmt.Errorf("delete failed"),
		},
	}

	reconciler := NewReconciler(sessionMgr, mockK8s)

	// Test cleanup with error
	err := reconciler.cleanupOrphanedPods(ctx)
	if err == nil {
		t.Error("Expected error from cleanupOrphanedPods")
	}
}

func TestReconciler_CleanupListError(t *testing.T) {
	ctx := context.Background()

	// Create session
	sessionMgr := session.NewManager()
	s1, _ := sessionMgr.Create(ctx, session.CreateRequest{
		AgentThreadID: "session1",
	})

	// Mock K8s client that returns error
	mockK8s := &MockK8sClient{
		listPodsErr: fmt.Errorf("list failed"),
	}

	reconciler := NewReconciler(sessionMgr, mockK8s)

	// Test cleanup with error
	err := reconciler.cleanupOrphanedPods(ctx)
	if err == nil {
		t.Error("Expected error from cleanupOrphanedPods")
	}
}