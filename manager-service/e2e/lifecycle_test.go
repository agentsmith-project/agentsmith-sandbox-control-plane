//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Validation – no real K8s pod needed
// ---------------------------------------------------------------------------

// TestCreate_MissingImage verifies the manager rejects a create request without an image.
func TestCreate_MissingImage(t *testing.T) {
	resp := newClient().CreateWorkload(t, testWS, testProj, uniqueID("val-no-img"),
		CreateRequest{Env: map[string]string{"KEY": "value"}})

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "image")
}

// TestCreate_InvalidWorkloadID verifies IDs that violate DNS-label rules are rejected.
func TestCreate_InvalidWorkloadID(t *testing.T) {
	invalidIDs := []string{
		"Invalid_ID",       // uppercase + underscore
		"has space",        // space
		"-starts-with",     // leading hyphen
		"ends-with-",       // trailing hyphen
		"a234567890123456789012345678901234567890123456789012345678901234", // 64 chars
	}
	for _, id := range invalidIDs {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			resp := newClient().CreateWorkload(t, testWS, testProj, id,
				CreateRequest{Image: suite.Image})
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
				"invalid workload ID %q must be rejected", id)
			assert.Contains(t, resp.BodyString(), "invalid")
		})
	}
}

// TestCreate_InvalidResourceQuantity verifies bad resource values are rejected.
func TestCreate_InvalidResourceQuantity(t *testing.T) {
	cases := []struct {
		name string
		req  CreateRequest
	}{
		{"bad cpu_request", CreateRequest{Image: suite.Image, CPURequest: "not-a-number"}},
		{"bad memory_limit", CreateRequest{Image: suite.Image, MemoryLimit: "xyz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := newClient().CreateWorkload(t, testWS, testProj, uniqueID("val-res"), tc.req)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

// TestGet_OfflineWhenPodDoesNotExist verifies GET returns phase=offline for non-existent pods.
func TestGet_OfflineWhenPodDoesNotExist(t *testing.T) {
	wlID := uniqueID("get-offline")
	resp := newClient().GetWorkload(t, testWS, testProj, wlID)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ps PodStatus
	require.NoError(t, resp.DecodeJSON(&ps))
	assert.Equal(t, "offline", ps.Phase)
	assert.Empty(t, ps.PodName)
}

// TestDelete_NotFound verifies DELETE on a non-existent workload returns 404.
func TestDelete_NotFound(t *testing.T) {
	resp := newClient().DeleteWorkload(t, testWS, testProj, uniqueID("del-404"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestKeepalive_NotFound verifies keepalive on a non-existent workload returns 404.
func TestKeepalive_NotFound(t *testing.T) {
	resp := newClient().Keepalive(t, testWS, testProj, uniqueID("ka-404"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestExec_MissingCmd verifies exec without cmd returns 400.
func TestExec_MissingCmd(t *testing.T) {
	resp := newClient().do(t, http.MethodPost,
		newClient().workloadURL(testWS, testProj, uniqueID("exec-no-cmd"))+"/exec",
		jsonBody(map[string]interface{}{"timeout_seconds": 10}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "cmd")
}

// TestExec_PodNotFound verifies exec on a non-existent pod returns 404.
func TestExec_PodNotFound(t *testing.T) {
	resp := newClient().Exec(t, testWS, testProj, uniqueID("exec-404"),
		[]string{"echo", "hello"}, 10)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Full pod lifecycle – requires a working K8s cluster and pullable image
// ---------------------------------------------------------------------------

// TestFullLifecycle_CreateGetDeleteGet is the canonical end-to-end pod lifecycle test:
//
//	Create → pod appears in K8s (201)
//	Get    → phase is Running (after waiting)
//	Delete → 200 pod deleted
//	Get    → phase is offline
func TestFullLifecycle_CreateGetDeleteGet(t *testing.T) {
	wlID := uniqueID("lifecycle")
	c := newClient()

	t.Logf("creating workload %s", wlID)
	createResp := c.CreateWorkload(t, testWS, testProj, wlID,
		CreateRequest{Image: suite.Image, IdleTimeoutSec: 900})
	require.Equal(t, http.StatusCreated, createResp.StatusCode,
		"create: %s", createResp.BodyString())

	var created PodStatus
	require.NoError(t, createResp.DecodeJSON(&created))
	assert.Equal(t, "workload-"+wlID, created.PodName)
	assert.NotEmpty(t, created.ExpiresAt)

	// Wait for Running via the manager GET endpoint.
	t.Logf("waiting for workload %s to reach Running", wlID)
	running := waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
	assert.Equal(t, "Running", running.Phase)
	assert.NotEmpty(t, running.IP)
	assert.NotEmpty(t, running.ExpiresAt)
	assert.NotEmpty(t, running.LastActivityAt)

	// Verify the pod exists in K8s with correct labels.
	podName := "workload-" + wlID
	pod, err := k8sCli.CoreV1().Pods(suite.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	require.NoError(t, err, "pod must exist in K8s")
	assert.Equal(t, "managed-workload", pod.Labels["app"])
	assert.Equal(t, wlID, pod.Labels["workload_id"])

	// Delete the workload.
	t.Logf("deleting workload %s", wlID)
	delResp := c.DeleteWorkload(t, testWS, testProj, wlID)
	assert.Equal(t, http.StatusOK, delResp.StatusCode,
		"delete: %s", delResp.BodyString())
	var dr DeleteResponse
	require.NoError(t, delResp.DecodeJSON(&dr))
	assert.Equal(t, "pod deleted", dr.Message)

	// After deletion, K8s pod should be gone.
	waitPodGone(t, suite.Namespace, podName, 30*time.Second)

	// Manager GET should now return offline.
	getResp := c.GetWorkload(t, testWS, testProj, wlID)
	var ps PodStatus
	require.NoError(t, getResp.DecodeJSON(&ps))
	assert.Equal(t, "offline", ps.Phase)
}

// TestCreate_Idempotent verifies that creating the same workload twice returns
// 200 with "pod already exists" on the second call.
func TestCreate_Idempotent(t *testing.T) {
	wlID := uniqueID("idem")
	c := newClient()

	first := c.CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	require.Equal(t, http.StatusCreated, first.StatusCode, "first create: %s", first.BodyString())

	// Wait briefly so the pod is likely registered.
	time.Sleep(2 * time.Second)

	second := c.CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	require.Equal(t, http.StatusOK, second.StatusCode,
		"second create (idempotent) should return 200, got %d: %s",
		second.StatusCode, second.BodyString())

	var ps PodStatus
	require.NoError(t, second.DecodeJSON(&ps))
	assert.Equal(t, "pod already exists", ps.Message)
	assert.Equal(t, "workload-"+wlID, ps.PodName)

	// Cleanup
	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestKeepalive_UpdatesExpiresAt verifies that keepalive extends the expires_at annotation.
func TestKeepalive_UpdatesExpiresAt(t *testing.T) {
	wlID := uniqueID("ka")
	c := newClient()

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// Record the current expires_at from the pod annotation.
	podName := "workload-" + wlID
	before := podAnnotations(t, suite.Namespace, podName)["expires_at"]

	// Wait a second so time.Now() clearly advances.
	time.Sleep(2 * time.Second)

	resp := c.Keepalive(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, resp.StatusCode, "keepalive: %s", resp.BodyString())

	var kr KeepaliveResponse
	require.NoError(t, resp.DecodeJSON(&kr))
	require.NotEmpty(t, kr.ExpiresAt)

	// The new expires_at must be strictly later than the original.
	tBefore, _ := time.Parse(time.RFC3339, before)
	tAfter, _ := time.Parse(time.RFC3339, kr.ExpiresAt)
	assert.True(t, tAfter.After(tBefore),
		"keepalive must extend expires_at: before=%s after=%s", before, kr.ExpiresAt)

	// K8s annotation should also reflect the update.
	after := podAnnotations(t, suite.Namespace, podName)["expires_at"]
	assert.Equal(t, kr.ExpiresAt, after, "K8s annotation must match keepalive response")

	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestKeepalive_MaxExpiresAtCapped verifies that keepalive cannot push expires_at
// beyond workload/maxExpiresAt.
func TestKeepalive_MaxExpiresAtCapped(t *testing.T) {
	wlID := uniqueID("ka-cap")

	// Create with a very short max lifetime (30s) and a 15-min idle timeout.
	// The idle timeout exceeds the max lifetime, so keepalive must be capped.
	c := newClient()
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{
		Image:          suite.Image,
		IdleTimeoutSec: 900, // 15 min
		MaxLifetimeSec: 30,  // 30 s max → maxExpiresAt = now+30s
	})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// At this point maxExpiresAt ≈ createdAt + 30s.
	// A keepalive after 3+ minutes of pod startup means maxExpiresAt is already in the past or close.
	// The keepalive should return an expires_at ≤ maxExpiresAt.

	podName := "workload-" + wlID
	maxExpiresStr := podAnnotations(t, suite.Namespace, podName)["workload/maxExpiresAt"]
	require.NotEmpty(t, maxExpiresStr)
	maxExpires, err := time.Parse(time.RFC3339, maxExpiresStr)
	require.NoError(t, err)

	resp := c.Keepalive(t, testWS, testProj, wlID)
	// The pod might already be expired; 404 is acceptable.
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("pod already expired (maxLifetimeSec=30, pod startup took longer)")
	}
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var kr KeepaliveResponse
	require.NoError(t, resp.DecodeJSON(&kr))
	tExpires, err := time.Parse(time.RFC3339, kr.ExpiresAt)
	require.NoError(t, err)

	assert.True(t, !tExpires.After(maxExpires.Add(time.Second)),
		"keepalive must not set expires_at=%s beyond maxExpiresAt=%s", kr.ExpiresAt, maxExpiresStr)

	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestCreate_WorkspaceDirCreated verifies the manager creates the workspace
// directory at {workspacePath}/{wsID}/{wlID} when a workload is created.
func TestCreate_WorkspaceDirCreated(t *testing.T) {
	wlID := uniqueID("wsdir")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})

	expectedDir := fmt.Sprintf("%s/%s/%s", suite.WorkspacePath, testWS, wlID)
	info, err := os.Stat(expectedDir)
	require.NoError(t, err, "workspace dir should be created at %s", expectedDir)
	assert.True(t, info.IsDir())

	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestCreate_MaxValidWorkloadIDLength verifies a workload ID of 63 chars (max DNS label) is accepted.
func TestCreate_MaxValidWorkloadIDLength(t *testing.T) {
	// 63 chars: valid DNS label
	wlID := "a23456789012345678901234567890123456789012345678901234567890123"
	resp := newClient().CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create 63-char workload ID: expected 201/200, got %d – %s", resp.StatusCode, resp.BodyString())
	}
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestLifecycle_ExecAfterDelete_Returns404 verifies exec on a deleted workload returns 404.
func TestLifecycle_ExecAfterDelete_Returns404(t *testing.T) {
	wlID := uniqueID("exec-after-del")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
	mustDeleteWorkload(t, testWS, testProj, wlID)
	waitPodGone(t, suite.Namespace, "workload-"+wlID, 60*time.Second)

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"echo", "x"}, 5)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "exec after delete must return 404")
}

// ---------------------------------------------------------------------------
// Additional real-world scenarios
// ---------------------------------------------------------------------------

// TestGet_RunningPod_ReturnsStatusFields verifies GET returns StartedAt, ExpiresAt, LastActivityAt when pod is running.
func TestGet_RunningPod_ReturnsStatusFields(t *testing.T) {
	wlID := uniqueID("get-fields")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	defer mustDeleteWorkload(t, testWS, testProj, wlID)
	ps := waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	assert.NotEmpty(t, ps.PodName, "pod_name must be set")
	assert.NotEmpty(t, ps.StartedAt, "started_at must be set")
	assert.NotEmpty(t, ps.ExpiresAt, "expires_at must be set")
	assert.NotEmpty(t, ps.LastActivityAt, "last_activity_at must be set")
	assert.NotEmpty(t, ps.IP, "ip must be set for running pod")
}

// TestCreate_WithEnvVars verifies create with env vars and that they are applied (we only check 201 and pod runs).
func TestCreate_WithEnvVars(t *testing.T) {
	wlID := uniqueID("create-env")
	req := CreateRequest{
		Image: suite.Image,
		Env:   map[string]string{"FOO": "bar", "BAZ": "qux"},
	}
	resp := newClient().CreateWorkload(t, testWS, testProj, wlID, req)
	require.True(t, resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK,
		"create with env: %d – %s", resp.StatusCode, resp.BodyString())
	defer mustDeleteWorkload(t, testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
}

// TestCreate_WithCustomIdleAndMaxRuntime verifies create accepts idle_timeout_sec and max_runtime_sec.
func TestCreate_WithCustomIdleAndMaxRuntime(t *testing.T) {
	wlID := uniqueID("create-timeouts")
	req := CreateRequest{
		Image:          suite.Image,
		IdleTimeoutSec: 600,
		MaxLifetimeSec: 3600,
	}
	resp := newClient().CreateWorkload(t, testWS, testProj, wlID, req)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create: %s", resp.BodyString())
	defer mustDeleteWorkload(t, testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
}

// TestExec_Success_ReturnsExitCodeAndOutput verifies exec returns exit_code, stdout, stderr.
func TestExec_Success_ReturnsExitCodeAndOutput(t *testing.T) {
	wlID := uniqueID("exec-ok")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	defer mustDeleteWorkload(t, testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"sh", "-c", "echo out; echo err >&2; exit 0"}, 10)
	require.Equal(t, http.StatusOK, resp.StatusCode, "exec: %s", resp.BodyString())
	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.Contains(t, er.Stdout, "out")
	assert.Contains(t, er.Stderr, "err")
}

// TestKeepalive_WrongMethod_Returns405 verifies GET on keepalive URL returns 405.
func TestKeepalive_WrongMethod_Returns405(t *testing.T) {
	wlID := uniqueID("ka-method")
	resp := newClient().do(t, http.MethodGet,
		newClient().workloadURL(testWS, testProj, wlID)+"/keepalive", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TestDelete_WrongMethod_Returns405 verifies POST to workload URL (no action) is not allowed.
func TestDelete_WrongMethod_Returns405(t *testing.T) {
	wlID := uniqueID("del-method")
	resp := newClient().do(t, http.MethodPost, newClient().workloadURL(testWS, testProj, wlID), nil)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TestCreate_ThenImmediateGet_ReturnsPendingOrRunning verifies GET right after create returns a valid phase.
func TestCreate_ThenImmediateGet_ReturnsPendingOrRunning(t *testing.T) {
	wlID := uniqueID("immediate-get")
	resp := newClient().CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	require.True(t, resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK,
		"create: %s", resp.BodyString())
	defer mustDeleteWorkload(t, testWS, testProj, wlID)

	getResp := newClient().GetWorkload(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var ps PodStatus
	require.NoError(t, getResp.DecodeJSON(&ps))
	assert.Contains(t, []string{"Pending", "Running"}, ps.Phase,
		"phase after create should be Pending or Running, got %s", ps.Phase)
}

// TestLifecycle_KeepaliveThenGet_ExpiresAtUpdated verifies that after keepalive, GET shows updated expires_at.
func TestLifecycle_KeepaliveThenGet_ExpiresAtUpdated(t *testing.T) {
	wlID := uniqueID("ka-then-get")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	defer mustDeleteWorkload(t, testWS, testProj, wlID)
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	get1 := newClient().GetWorkload(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, get1.StatusCode)
	var ps1 PodStatus
	require.NoError(t, get1.DecodeJSON(&ps1))
	require.NotEmpty(t, ps1.ExpiresAt)

	time.Sleep(2 * time.Second)
	kaResp := newClient().Keepalive(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, kaResp.StatusCode)

	get2 := newClient().GetWorkload(t, testWS, testProj, wlID)
	require.Equal(t, http.StatusOK, get2.StatusCode)
	var ps2 PodStatus
	require.NoError(t, get2.DecodeJSON(&ps2))
	assert.NotEmpty(t, ps2.ExpiresAt)
	// expires_at after keepalive should be at least as late as before (or later)
	assert.True(t, ps2.ExpiresAt >= ps1.ExpiresAt,
		"expires_at after keepalive should not decrease: before=%s after=%s", ps1.ExpiresAt, ps2.ExpiresAt)
}

// ---------------------------------------------------------------------------
// Request validation – malformed path and body
// ---------------------------------------------------------------------------

// TestV1Path_TooShort_Returns404 verifies that a path with insufficient segments returns 404.
// e.g. GET /v1/workspaces/ws/projects/proj (missing /workloads/{wlId})
func TestV1Path_TooShort_Returns404(t *testing.T) {
	c := newClient()
	shortPath := fmt.Sprintf("%s/v1/workspaces/%s/projects/%s", suite.ManagerURL, testWS, testProj)
	resp := c.do(t, http.MethodGet, shortPath, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"path without workload ID must return 404")
}

// TestCreate_InvalidJSON_Returns400 verifies PUT with invalid JSON body returns 400.
func TestCreate_InvalidJSON_Returns400(t *testing.T) {
	c := newClient()
	wlID := uniqueID("create-bad-json")
	url := c.workloadURL(testWS, testProj, wlID)
	resp := c.do(t, http.MethodPut, url, strings.NewReader(`{invalid json}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "invalid")
}

// TestCreate_EmptyBody_Returns400 verifies PUT with empty body still requires image; decoder may fail or image missing.
func TestCreate_EmptyBody_Returns400(t *testing.T) {
	c := newClient()
	wlID := uniqueID("create-empty-body")
	url := c.workloadURL(testWS, testProj, wlID)
	resp := c.do(t, http.MethodPut, url, strings.NewReader("{}"))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "image")
}

// TestExec_InvalidBody_Returns400 verifies POST .../exec with invalid JSON returns 400.
func TestExec_InvalidBody_Returns400(t *testing.T) {
	c := newClient()
	wlID := uniqueID("exec-bad-json")
	url := c.workloadURL(testWS, testProj, wlID) + "/exec"
	resp := c.do(t, http.MethodPost, url, strings.NewReader(`{invalid json}`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"exec with invalid JSON must return 400: %s", resp.BodyString())
	assert.Contains(t, resp.BodyString(), "invalid")
}
