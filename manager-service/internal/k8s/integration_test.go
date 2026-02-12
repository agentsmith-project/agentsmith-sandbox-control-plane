//go:build integration

package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testNamespace returns the Kubernetes namespace used for integration tests.
// It defaults to "default" but can be overridden via the TEST_K8S_NAMESPACE env var.
func testNamespace() string {
	if ns := os.Getenv("TEST_K8S_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

// testK8sClient creates a *Client configured for integration testing.
// It fails the test immediately if the client cannot be constructed.
func testK8sClient(t *testing.T) *Client {
	t.Helper()

	cfg := &ClientConfig{
		Namespace:        testNamespace(),
		DefaultContainer: "sandbox",
		QPS:              50,
		Burst:            100,
		RequestTimeout:   30 * time.Second,
	}

	client, err := NewClient(cfg)
	require.NoError(t, err, "failed to create k8s client")

	return client
}

// testPodSpec returns a minimal PodSpec suitable for integration tests.
// It uses busybox:latest running "sleep infinity" so the pod stays alive.
func testPodSpec(sessionID string) *PodSpec {
	return &PodSpec{
		SessionID:       sessionID,
		Image:           "busybox:latest",
		ImagePullPolicy: "IfNotPresent",
		TTLSeconds:      300,
		CPULimit:        "100m",
		MemoryLimit:     "64Mi",
		ContainerName:   "sandbox",
		Workdir:         "/tmp",
		Env:             map[string]string{"TEST": "true"},
		Labels: map[string]string{
			"app":                    "integration-test",
			"manager.mbos.io/session": sessionID,
		},
		Annotations: map[string]string{
			"manager.mbos.io/session-id": sessionID,
		},
	}
}

// uniqueSessionID produces a unique session ID for each test to avoid collisions.
func uniqueSessionID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

// cleanupPod registers a t.Cleanup that deletes the given pod (best-effort).
func cleanupPod(t *testing.T, client *Client, podName string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = client.DeletePod(ctx, podName, 0)
	})
}

// --------------------------------------------------------------------------
// Integration Tests
// --------------------------------------------------------------------------

func TestIntegration_CheckReady(t *testing.T) {
	client := testK8sClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.CheckReady(ctx)
	require.NoError(t, err, "k8s cluster should be reachable")
}

func TestIntegration_CreatePod_GetPod(t *testing.T) {
	client := testK8sClient(t)
	sessionID := uniqueSessionID(t)
	spec := testPodSpec(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create the pod.
	result, err := client.CreatePod(ctx, spec)
	require.NoError(t, err, "CreatePod should succeed")
	require.NotNil(t, result, "PodResult should not be nil")

	podName := PodName(sessionID)
	cleanupPod(t, client, podName)

	// Retrieve the pod via GetPod.
	pod, err := client.GetPod(ctx, podName)
	require.NoError(t, err, "GetPod should succeed for the created pod")
	require.NotNil(t, pod, "Pod should not be nil")

	// Verify basic metadata.
	assert.Equal(t, podName, pod.Name, "pod name should match")
	assert.Equal(t, testNamespace(), pod.Namespace, "pod namespace should match")

	// Verify annotations were propagated.
	assert.Equal(t, sessionID, pod.Annotations["manager.mbos.io/session-id"],
		"session-id annotation should be set")

	// Verify labels.
	assert.Equal(t, "integration-test", pod.Labels["app"],
		"app label should be set")

	// Verify helper functions against the pod object.
	assert.Equal(t, sessionID, GetSessionIDFromPod(pod),
		"GetSessionIDFromPod should return the correct session ID")
	assert.Greater(t, GetTTLFromPod(pod), 0,
		"GetTTLFromPod should return a positive TTL")
}

func TestIntegration_WaitForPodReady(t *testing.T) {
	client := testK8sClient(t)
	sessionID := uniqueSessionID(t)
	spec := testPodSpec(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := client.CreatePod(ctx, spec)
	require.NoError(t, err, "CreatePod should succeed")

	podName := PodName(sessionID)
	cleanupPod(t, client, podName)

	// Wait for the pod to become ready.
	ready, err := client.WaitForPodReady(ctx, podName, 60*time.Second, 2*time.Second)
	require.NoError(t, err, "WaitForPodReady should not return an error")
	assert.True(t, ready, "pod should become ready within the timeout")

	// Double-check via GetPod + IsPodReady.
	pod, err := client.GetPod(ctx, podName)
	require.NoError(t, err)
	assert.True(t, IsPodReady(pod), "IsPodReady should report the pod as ready")
}

func TestIntegration_EnsurePod_Idempotent(t *testing.T) {
	client := testK8sClient(t)
	sessionID := uniqueSessionID(t)
	spec := testPodSpec(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First call — creates the pod.
	result1, err := client.EnsurePod(ctx, spec, 60*time.Second, 2*time.Second)
	require.NoError(t, err, "first EnsurePod should succeed")
	require.NotNil(t, result1)

	podName := PodName(sessionID)
	cleanupPod(t, client, podName)

	// Second call with the same session ID — should return the existing pod.
	result2, err := client.EnsurePod(ctx, spec, 60*time.Second, 2*time.Second)
	require.NoError(t, err, "second EnsurePod should succeed (idempotent)")
	require.NotNil(t, result2)

	// Both results should reference the same pod (same UID).
	pod1, err := client.GetPod(ctx, podName)
	require.NoError(t, err)

	assert.Equal(t, pod1.UID, pod1.UID, "pod UID should remain the same across EnsurePod calls")
	assert.Equal(t, podName, pod1.Name, "pod name should remain stable")
}

func TestIntegration_PatchActivity(t *testing.T) {
	client := testK8sClient(t)
	sessionID := uniqueSessionID(t)
	spec := testPodSpec(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := client.CreatePod(ctx, spec)
	require.NoError(t, err, "CreatePod should succeed")

	podName := PodName(sessionID)
	cleanupPod(t, client, podName)

	// Wait until the pod is running so we can patch it.
	ready, err := client.WaitForPodReady(ctx, podName, 60*time.Second, 2*time.Second)
	require.NoError(t, err)
	require.True(t, ready, "pod must be ready before patching activity")

	// Record the initial expiry annotation.
	podBefore, err := client.GetPod(ctx, podName)
	require.NoError(t, err)
	expiryBefore := GetExpiresAtFromPod(podBefore)

	// Patch the activity with a new TTL.
	newTTL := 600
	err = client.PatchActivity(ctx, podName, newTTL)
	require.NoError(t, err, "PatchActivity should succeed")

	// Verify annotations were updated.
	podAfter, err := client.GetPod(ctx, podName)
	require.NoError(t, err)

	assert.Equal(t, newTTL, GetTTLFromPod(podAfter),
		"TTL annotation should be updated to the new value")

	expiryAfter := GetExpiresAtFromPod(podAfter)
	if expiryBefore != "" {
		assert.NotEqual(t, expiryBefore, expiryAfter,
			"expires-at annotation should change after patching activity")
	}
}

func TestIntegration_DeletePod(t *testing.T) {
	client := testK8sClient(t)
	sessionID := uniqueSessionID(t)
	spec := testPodSpec(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := client.CreatePod(ctx, spec)
	require.NoError(t, err, "CreatePod should succeed")

	podName := PodName(sessionID)
	// No cleanupPod here — we are testing deletion explicitly.

	// Wait for the pod to be scheduled.
	_, err = client.WaitForPodReady(ctx, podName, 60*time.Second, 2*time.Second)
	require.NoError(t, err, "pod should become ready before deletion test")

	// Delete the pod.
	err = client.DeletePod(ctx, podName, 0)
	require.NoError(t, err, "DeletePod should succeed")

	// Poll until GetPod returns an error (pod is gone).
	// Kubernetes may take a moment to fully remove the pod.
	require.Eventually(t, func() bool {
		_, err := client.GetPod(ctx, podName)
		return err != nil
	}, 30*time.Second, 2*time.Second, "pod should be deleted and GetPod should return an error")
}
