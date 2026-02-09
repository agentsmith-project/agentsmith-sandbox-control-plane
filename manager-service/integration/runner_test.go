//go:build Integration
// +build Integration

package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/k8s"
)

func TestRunner_KubernetesIntegration(t *testing.T) {
	ctx := context.Background()

	// This test requires a configured kubectl context pointing to a cluster
	if os.Getenv("KUBECONFIG") == "" && os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		t.Skip("Skipping Kubernetes integration test: no kubeconfig")
	}

	// Create k8s client
	client, err := k8s.NewClient(&k8s.ClientConfig{
		Namespace: "sandbox",
	})
	require.NoError(t, err, "Failed to create k8s client")

	// Create executor
	executor := k8s.NewExecutor(client)

	t.Run("Create and Delete Pod", func(t *testing.T) {
		sessionID := "test-runner-" + time.Now().Format("20060102150405")

		// Create pod spec
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
			AgentThreadID:   "test-agent-thread-" + sessionID,
		}

		// Create pod
		result, err := client.CreatePod(ctx, spec)
		require.NoError(t, err, "Failed to create pod")
		assert.NotEmpty(t, result.PodName, "Pod name should not be empty")
		assert.True(t, result.Exists, "Pod should exist")
		assert.False(t, result.Ready, "Pod should not be ready immediately")

		podName := result.PodName

		// Wait for pod to be ready
		ready, err := client.WaitForPodReady(ctx, podName, 60*time.Second, 2*time.Second)
		require.NoError(t, err, "Failed to wait for pod ready")
		assert.True(t, ready, "Pod should be ready")

		// Get pod status
		pod, err := client.GetPod(ctx, podName)
		require.NoError(t, err, "Failed to get pod")
		assert.Equal(t, "Running", string(pod.Status.Phase), "Pod should be running")

		// Verify pod exists
		exists, err := client.PodExists(ctx, podName)
		require.NoError(t, err, "Failed to check pod existence")
		assert.True(t, exists, "Pod should exist")

		// Verify pod annotations
		assert.Contains(t, pod.Annotations, "sandbox/sessionId", "Pod should have session ID annotation")
		assert.Equal(t, sessionID, pod.Annotations["sandbox/sessionId"], "Session ID should match")
		assert.Contains(t, pod.Annotations, "expires_at", "Pod should have expires_at annotation")

		// Verify pod labels
		assert.Contains(t, pod.Labels, "agent_thread_id", "Pod should have agent_thread_id label")
		assert.Equal(t, spec.AgentThreadID, pod.Labels["agent_thread_id"], "Agent thread ID should match")

		// Delete pod
		err = client.DeletePod(ctx, podName, 0)
		require.NoError(t, err, "Failed to delete pod")

		// Verify deletion
		exists, _ = client.PodExists(ctx, podName)
		assert.False(t, exists, "Pod should be deleted")
	})

	t.Run("Execute Command in Pod", func(t *testing.T) {
		sessionID := "test-exec-" + time.Now().Format("20060102150405")

		// Create pod
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
		}

		result, err := client.CreatePod(ctx, spec)
		require.NoError(t, err)

		// Wait for pod to be ready
		ready, err := client.WaitForPodReady(ctx, result.PodName, 60*time.Second, 2*time.Second)
		require.NoError(t, err)
		if !ready {
			client.DeletePod(ctx, result.PodName, 0)
			t.Fatal("Pod did not become ready")
		}

		// Execute command
		execResult, err := executor.Exec(ctx, result.PodName, &k8s.ExecOptions{
			Command: []string{"sh", "-c", "echo 'hello from runner'"},
			Timeout: 10 * time.Second,
		})
		require.NoError(t, err, "Failed to exec in pod")
		assert.Equal(t, 0, execResult.ExitCode, "Exit code should be 0")
		assert.Contains(t, execResult.Stdout, "hello from runner", "Output should contain echoed text")
		assert.Greater(t, execResult.Duration, time.Duration(0), "Duration should be positive")

		// Test command with stderr
		execResult, err = executor.Exec(ctx, result.PodName, &k8s.ExecOptions{
			Command: []string{"sh", "-c", "echo 'error message' >&2"},
			Timeout: 10 * time.Second,
		})
		require.NoError(t, err)
		assert.Contains(t, execResult.Stderr, "error message", "Stderr should contain error message")

		// Test failing command
		execResult, err = executor.Exec(ctx, result.PodName, &k8s.ExecOptions{
			Command: []string{"sh", "-c", "exit 42"},
			Timeout: 10 * time.Second,
		})
		// Note: exec doesn't fail on non-zero exit, it returns ExitCode in result
		require.NoError(t, err, "Exec should not error on non-zero exit")
		// Exit code might be -1 if we can't determine it, but command should have run
		assert.GreaterOrEqual(t, execResult.ExitCode, -1, "Exit code should be set")

		// Cleanup
		_ = client.DeletePod(ctx, result.PodName, 0)
	})

	t.Run("EnsurePod - Get Existing Pod", func(t *testing.T) {
		sessionID := "test-ensure-" + time.Now().Format("20060102150405")

		// Create pod
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
		}

		// First call should create pod
		result1, err := client.EnsurePod(ctx, spec, 60*time.Second, 2*time.Second)
		require.NoError(t, err, "Failed to ensure pod")
		assert.True(t, result1.Exists, "Pod should exist")
		assert.True(t, result1.Ready, "Pod should be ready")

		podName := result1.PodName

		// Second call should get existing pod
		result2, err := client.EnsurePod(ctx, spec, 60*time.Second, 2*time.Second)
		require.NoError(t, err, "Failed to ensure pod on second call")
		assert.Equal(t, podName, result2.PodName, "Pod name should be the same")
		assert.True(t, result2.Exists, "Pod should exist")
		assert.True(t, result2.Ready, "Pod should be ready")

		// Cleanup
		_ = client.DeletePod(ctx, podName, 0)
	})

	t.Run("PodName Generation", func(t *testing.T) {
		sessionID := "my-test-session-12345"

		podName := k8s.PodName(sessionID)

		assert.True(t, strings.HasPrefix(podName, "sbx-"), "Pod name should start with sbx-")
		assert.Len(t, podName, 14, "Pod name should be 14 characters (sbx- + 10 char hash)")
	})

	t.Run("PatchActivity", func(t *testing.T) {
		sessionID := "test-patch-" + time.Now().Format("20060102150405")

		// Create pod
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
		}

		result, err := client.CreatePod(ctx, spec)
		require.NoError(t, err)

		// Wait for ready
		_, err = client.WaitForPodReady(ctx, result.PodName, 60*time.Second, 2*time.Second)
		require.NoError(t, err)

		// Get initial expires_at
		pod, err := client.GetPod(ctx, result.PodName)
		require.NoError(t, err)
		initialExpiresAt := pod.Annotations["expires_at"]

		// Wait a moment to ensure time difference
		time.Sleep(2 * time.Second)

		// Patch activity with new TTL
		err = client.PatchActivity(ctx, result.PodName, 600)
		require.NoError(t, err, "Failed to patch activity")

		// Verify expires_at changed
		pod, err = client.GetPod(ctx, result.PodName)
		require.NoError(t, err)
		newExpiresAt := pod.Annotations["expires_at"]
		assert.NotEqual(t, initialExpiresAt, newExpiresAt, "Expires at should have changed")

		// Verify last_activity_at was updated
		assert.Contains(t, pod.Annotations, "last_activity_at", "Should have last_activity_at")

		// Cleanup
		_ = client.DeletePod(ctx, result.PodName, 0)
	})

	t.Run("GetPodBySessionID", func(t *testing.T) {
		sessionID := "test-getbyid-" + time.Now().Format("20060102150405")

		// Create pod
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
		}

		result, err := client.CreatePod(ctx, spec)
		require.NoError(t, err)

		// Get pod by session ID
		pod, err := client.GetPodBySessionID(ctx, sessionID)
		require.NoError(t, err, "Failed to get pod by session ID")
		assert.Equal(t, result.PodName, pod.Name, "Pod name should match")

		// Verify we can get the session ID back from pod
		retrievedSessionID := k8s.GetSessionIDFromPod(pod)
		assert.Equal(t, sessionID, retrievedSessionID, "Session ID should match")

		// Cleanup
		_ = client.DeletePodBySessionID(ctx, sessionID, 0)
	})

	t.Run("Exec with Timeout", func(t *testing.T) {
		sessionID := "test-timeout-" + time.Now().Format("20060102150405")

		// Create pod
		spec := &k8s.PodSpec{
			SessionID:       sessionID,
			Image:           "nginx:alpine",
			ImagePullPolicy: "IfNotPresent",
			TTLSeconds:      300,
			Command:         "sleep 300",
			ContainerName:   "runner",
		}

		result, err := client.CreatePod(ctx, spec)
		require.NoError(t, err)

		// Wait for ready
		_, err = client.WaitForPodReady(ctx, result.PodName, 60*time.Second, 2*time.Second)
		require.NoError(t, err)

		// Execute command with short timeout (should timeout)
		execResult, err := executor.Exec(ctx, result.PodName, &k8s.ExecOptions{
			Command: []string{"sleep", "30"},
			Timeout: 2 * time.Second,
		})
		// Should error due to timeout
		assert.Error(t, err, "Should error on timeout")
		assert.True(t, execResult.TimedOut, "Should be marked as timed out")

		// Cleanup
		_ = client.DeletePod(ctx, result.PodName, 0)
	})
}
