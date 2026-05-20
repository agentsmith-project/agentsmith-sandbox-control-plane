//go:build e2e

package e2e_test

// gc_test.go – End-to-end tests for pod expiry, idle timeout, keepalive accounting,
// and ASBCP-owned release of expired workloads.
//
// Test topology (no external state assumptions):
//   - Each test creates its own workload(s) and cleans up via t.Cleanup.
//   - patchPodExpiry (suite helper) fast-forwards the expires_at annotation so GC
//     tests don't need to wait for real idle timeouts.
//   - releaseExpiredWorkloadsViaASBCP scans expired pods and calls the ASBCP
//     workload delete API, preserving the AFSCP release/status path.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Annotation verification tests (no real K8s pod needed for the assertions –
// but we do create pods to read their annotations from the API)
// ---------------------------------------------------------------------------

// TestGC_ExpiresAtAnnotationSetOnCreate verifies that after creating a workload
// the pod carries an expires_at annotation that is in the future.
func TestGC_ExpiresAtAnnotationSetOnCreate(t *testing.T) {
	wlID := uniqueID("gc-ann-exp")
	before := time.Now().UTC()

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	// mustCreateWorkload waits for the pod to be Ready before returning,
	// so the pod already exists and all annotations are set at this point.
	podName := workloadPodName(testWS, testProj, wlID)
	waitPodExists(t, suite.Namespace, podName, 10*time.Second)

	ann := podAnnotations(t, suite.Namespace, podName)

	require.NotEmpty(t, ann["expires_at"], "pod must have expires_at annotation")
	expiresAt, err := time.Parse(time.RFC3339, ann["expires_at"])
	require.NoError(t, err, "expires_at must be valid RFC3339")

	assert.True(t, expiresAt.After(before),
		"expires_at %s must be in the future (before=%s)", ann["expires_at"], before)
	assert.True(t, expiresAt.Before(before.Add(DefaultIdleTimeout+2*time.Minute)),
		"expires_at must not be too far in the future (idle_timeout is the cap)")
}

// TestGC_MaxExpiresAtAnnotationSetOnCreate verifies the workload/maxExpiresAt
// annotation is set and respects max_lifetime_sec.
func TestGC_MaxExpiresAtAnnotationSetOnCreate(t *testing.T) {
	wlID := uniqueID("gc-ann-max")
	const maxLifetimeSec = 3600 // 1 hour
	before := time.Now().UTC()

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{
		Image:          suite.Image,
		MaxLifetimeSec: maxLifetimeSec,
	})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitPodExists(t, suite.Namespace, podName, 10*time.Second)

	ann := podAnnotations(t, suite.Namespace, podName)

	require.NotEmpty(t, ann["workload/maxExpiresAt"], "pod must have workload/maxExpiresAt annotation")
	maxExpires, err := time.Parse(time.RFC3339, ann["workload/maxExpiresAt"])
	require.NoError(t, err)

	expected := before.Add(time.Duration(maxLifetimeSec) * time.Second)
	assert.WithinDuration(t, expected, maxExpires, 30*time.Second,
		"workload/maxExpiresAt should be ~now+%ds", maxLifetimeSec)
}

// TestGC_IdleTimeoutAnnotationReflectsRequest verifies workload/idleTimeoutSec
// annotation matches the requested idle_timeout_sec value.
func TestGC_IdleTimeoutAnnotationReflectsRequest(t *testing.T) {
	wlID := uniqueID("gc-ann-idle")
	const customIdle = 600

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{
		Image:          suite.Image,
		IdleTimeoutSec: customIdle,
	})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitPodExists(t, suite.Namespace, podName, 10*time.Second)

	ann := podAnnotations(t, suite.Namespace, podName)
	assert.Equal(t, fmt.Sprintf("%d", customIdle), ann["workload/idleTimeoutSec"],
		"idle timeout annotation must match requested value")
}

// TestGC_LastActivityAtAnnotationSetOnCreate verifies last_activity_at is set
// to approximately the time of pod creation.
func TestGC_LastActivityAtAnnotationSetOnCreate(t *testing.T) {
	wlID := uniqueID("gc-last-act")
	before := time.Now().UTC()

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitPodExists(t, suite.Namespace, podName, 10*time.Second)
	after := time.Now().UTC()

	ann := podAnnotations(t, suite.Namespace, podName)
	require.NotEmpty(t, ann["last_activity_at"])
	lastAct, err := time.Parse(time.RFC3339, ann["last_activity_at"])
	require.NoError(t, err)

	assert.True(t, !lastAct.Before(before.Add(-5*time.Second)),
		"last_activity_at must not predate pod creation by more than 5s")
	assert.True(t, !lastAct.After(after.Add(5*time.Second)),
		"last_activity_at must be within 5s of pod creation completion")
}

// ---------------------------------------------------------------------------
// Keepalive accounting
// ---------------------------------------------------------------------------

// TestGC_KeepaliveUpdatesExpiresAt verifies that calling keepalive pushes
// expires_at forward on the pod annotation and returns the new value in the API response.
func TestGC_KeepaliveUpdatesExpiresAt(t *testing.T) {
	wlID := uniqueID("gc-ka-update")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// Record pre-keepalive annotation.
	annBefore := podAnnotations(t, suite.Namespace, podName)
	expBefore, err := time.Parse(time.RFC3339, annBefore["expires_at"])
	require.NoError(t, err, "pre-keepalive expires_at must be parseable")

	// Wait a moment to ensure the new expiry is strictly later.
	time.Sleep(2 * time.Second)

	resp := newClient().Keepalive(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, resp.StatusCode, "keepalive: %s", resp.BodyString())

	var kr KeepaliveResponse
	require.NoError(t, resp.DecodeJSON(&kr))
	require.NotEmpty(t, kr.ExpiresAt)

	// API response must carry the new expiry.
	expAfterAPI, err := time.Parse(time.RFC3339, kr.ExpiresAt)
	require.NoError(t, err)
	assert.True(t, expAfterAPI.After(expBefore),
		"keepalive must extend expires_at beyond previous value")

	// Pod annotation must also be updated.
	annAfter := podAnnotations(t, suite.Namespace, podName)
	expAfterAnn, err := time.Parse(time.RFC3339, annAfter["expires_at"])
	require.NoError(t, err)
	assert.WithinDuration(t, expAfterAPI, expAfterAnn, 5*time.Second,
		"pod annotation expires_at must match the API response")

	// last_activity_at annotation must also have been refreshed.
	lastAct, err := time.Parse(time.RFC3339, annAfter["last_activity_at"])
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), lastAct, 10*time.Second,
		"last_activity_at must be updated to approximately now")
}

// TestGC_KeepaliveDoesNotExceedMaxLifetime verifies keepalive is capped at
// workload/maxExpiresAt and cannot extend past the pod's maximum lifetime.
func TestGC_KeepaliveDoesNotExceedMaxLifetime(t *testing.T) {
	wlID := uniqueID("gc-ka-cap")

	// Use a very short max lifetime (30s) so maxExpiresAt is in the near future.
	// The idle timeout (default 30 min) is much larger, so without the cap the
	// keepalive would set expires_at = now+30min which exceeds maxExpiresAt.
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{
		Image:          suite.Image,
		MaxLifetimeSec: 30,
	})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	ann := podAnnotations(t, suite.Namespace, podName)
	maxExpiresStr := ann["workload/maxExpiresAt"]
	require.NotEmpty(t, maxExpiresStr)
	maxExpires, err := time.Parse(time.RFC3339, maxExpiresStr)
	require.NoError(t, err)

	resp := newClient().Keepalive(t, testWS, testProj, wlID)
	// The pod may already be expired (startup > 30s); 404 is acceptable.
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("pod already expired (max_lifetime_sec=30, startup took longer than 30s)")
	}
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var kr KeepaliveResponse
	require.NoError(t, resp.DecodeJSON(&kr))
	expiresAt, err := time.Parse(time.RFC3339, kr.ExpiresAt)
	require.NoError(t, err)

	assert.True(t, !expiresAt.After(maxExpires.Add(2*time.Second)),
		"keepalive expires_at=%s must not exceed maxExpiresAt=%s", kr.ExpiresAt, maxExpiresStr)
}

// TestGC_MultipleKeepalives verifies repeated keepalives keep extending the expiry
// and that each call's response carries a valid future timestamp.
func TestGC_MultipleKeepalives(t *testing.T) {
	wlID := uniqueID("gc-multi-ka")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{
		Image:          suite.Image,
		MaxLifetimeSec: 3600,
	})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	c := newClient()
	var prev time.Time

	for i := 0; i < 3; i++ {
		time.Sleep(1 * time.Second)
		resp := c.Keepalive(t, testWS, testProj, wlID)
		require.Equal(t, http.StatusOK, resp.StatusCode, "keepalive #%d: %s", i+1, resp.BodyString())

		var kr KeepaliveResponse
		require.NoError(t, resp.DecodeJSON(&kr))

		cur, err := time.Parse(time.RFC3339, kr.ExpiresAt)
		require.NoError(t, err, "keepalive #%d: expires_at must be valid RFC3339", i+1)
		assert.True(t, cur.After(time.Now().UTC()),
			"keepalive #%d: expires_at must be in the future", i+1)

		if i > 0 {
			assert.True(t, !cur.Before(prev.Add(-time.Second)),
				"keepalive #%d: expires_at must not regress (cur=%s prev=%s)", i+1, cur, prev)
		}
		prev = cur
	}
}

// ---------------------------------------------------------------------------
// ASBCP-owned expiry release
// ---------------------------------------------------------------------------

func TestGC_ASBCPRelease_DeletesExpiredPod(t *testing.T) {
	wlID := uniqueID("gc-inproc-del")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	podName := workloadPodName(testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// Force-expire the pod.
	patchPodExpiry(t, suite.Namespace, podName, time.Now().UTC().Add(-1*time.Hour))

	deleted := releaseExpiredWorkloadsViaASBCP(t, suite.Namespace)
	assert.GreaterOrEqual(t, deleted, 1, "at least one expired pod must be deleted")

	waitPodGone(t, suite.Namespace, podName, 60*time.Second)

	// After sweep, GET should return offline.
	resp := newClient().GetWorkload(t, testWS, testProj, wlID)
	var ps PodStatus
	require.NoError(t, resp.DecodeJSON(&ps))
	assert.Equal(t, "offline", ps.Phase, "deleted pod must appear as offline")
}

// TestGC_ASBCPRelease_PreservesActivePod verifies the ASBCP release helper does NOT delete a pod
// whose expires_at is in the future.
func TestGC_ASBCPRelease_PreservesActivePod(t *testing.T) {
	wlID := uniqueID("gc-inproc-keep")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// Pod expires_at is already in the future from creation (default 30 min).
	deleted := releaseExpiredWorkloadsViaASBCP(t, suite.Namespace)

	// The active pod must NOT have been deleted.
	resp := newClient().GetWorkload(t, testWS, testProj, wlID)
	var ps PodStatus
	require.NoError(t, resp.DecodeJSON(&ps))
	assert.NotEqual(t, "offline", ps.Phase,
		"active pod (expires_at in future) must not be swept; deleted=%d", deleted)
}

// TestGC_ASBCPRelease_MultipleExpiredPods verifies batch release deletes all expired
// pods in one pass without touching active ones.
func TestGC_ASBCPRelease_MultipleExpiredPods(t *testing.T) {
	const n = 3
	wlIDs := make([]string, n)
	for i := 0; i < n; i++ {
		wlIDs[i] = uniqueID(fmt.Sprintf("gc-batch-%d", i))
		_ = mustCreateWorkload(t, testWS, testProj, wlIDs[i], CreateRequest{Image: suite.Image})
		t.Cleanup(func(id string) func() {
			return func() { newClient().DeleteWorkload(t, testWS, testProj, id) }
		}(wlIDs[i]))
	}
	// Create one active pod that must survive.
	activeWlID := uniqueID("gc-batch-active")
	_ = mustCreateWorkload(t, testWS, testProj, activeWlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, activeWlID) })

	// Wait for all pods to be Running (mustCreateWorkload already waits for
	// readiness, but we want all pods to exist before we patch any of them).
	for _, id := range wlIDs {
		waitWorkloadRunning(t, testWS, testProj, id, 3*time.Minute)
	}
	waitWorkloadRunning(t, testWS, testProj, activeWlID, 3*time.Minute)

	// Force-expire the n batch pods.
	pastTime := time.Now().UTC().Add(-1 * time.Hour)
	for _, id := range wlIDs {
		patchPodExpiry(t, suite.Namespace, workloadPodName(testWS, testProj, id), pastTime)
	}

	deleted := releaseExpiredWorkloadsViaASBCP(t, suite.Namespace)
	assert.GreaterOrEqual(t, deleted, n, "must delete all %d expired pods", n)

	// All n expired pods must be gone.
	for _, id := range wlIDs {
		waitPodGone(t, suite.Namespace, workloadPodName(testWS, testProj, id), 60*time.Second)
	}

	// The active pod must still be alive.
	activeResp := newClient().GetWorkload(t, testWS, testProj, activeWlID)
	var activePS PodStatus
	require.NoError(t, activeResp.DecodeJSON(&activePS))
	assert.NotEqual(t, "offline", activePS.Phase,
		"active pod must survive batch GC sweep")
}

// GET reflects offline after deletion
// ---------------------------------------------------------------------------

// TestGC_GetAfterDeletion verifies that after a pod is deleted through the ASBCP API,
// GET returns phase "offline" rather than the previous phase.
func TestGC_GetAfterDeletion(t *testing.T) {
	wlID := uniqueID("gc-get-after")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	mustDeleteWorkload(t, testWS, testProj, wlID)
	waitPodGone(t, suite.Namespace, workloadPodName(testWS, testProj, wlID), 60*time.Second)

	resp := newClient().GetWorkload(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ps PodStatus
	require.NoError(t, resp.DecodeJSON(&ps))
	assert.Equal(t, "offline", ps.Phase,
		"GET after pod deletion must return phase=offline")
}

// DefaultIdleTimeout is the module-level constant from types.go, referenced here
// to avoid a hard-coded magic number in assertions.
const DefaultIdleTimeout = 30 * time.Minute
