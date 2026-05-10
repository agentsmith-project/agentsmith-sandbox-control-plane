//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/workspacebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Workspace binding tests
//
// These tests validate the current workspace lifecycle managed by the sandbox manager:
//   - a workload uses a stable workspace binding
//   - the pod mounts the binding PVC at the AFSCP plan mount path
//   - files written to /home/<task>/workspace persist across pod restarts
// ---------------------------------------------------------------------------

// TestWorkspace_BindingCreatedOnCreate verifies a workload uses a current AFSCP-backed binding.
func TestWorkspace_BindingCreatedOnCreate(t *testing.T) {
	wlID := uniqueID("ws-mkdir")
	bindingID := bindingIDForWorkload(wlID)

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	resp := newClient().EnsureWorkspaceBinding(t, testWS, testProj, bindingID, WorkspaceBindingRequest{
		NamespaceID:    suite.AFSCPNamespaceID,
		MountBindingID: bindingID,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var binding WorkspaceBindingResponse
	require.NoError(t, resp.DecodeJSON(&binding))
	assert.Equal(t, bindingID, binding.BindingID)
	assert.Equal(t, taskHomePath(wlID), binding.MountPath)
	assert.Equal(t, workspacebinding.PVCName(testWS, testProj, bindingID), binding.PVCName)
}

// TestWorkspace_BindingIsolatedPerWorkload verifies different workloads use different bindings.
func TestWorkspace_BindingIsolatedPerWorkload(t *testing.T) {
	wlID1 := uniqueID("ws-iso-1")
	wlID2 := uniqueID("ws-iso-2")
	c := newClient()

	_ = mustCreateWorkload(t, testWS, testProj, wlID1, CreateRequest{Image: suite.Image})
	_ = mustCreateWorkload(t, testWS, testProj, wlID2, CreateRequest{Image: suite.Image})
	t.Cleanup(func() {
		c.DeleteWorkload(t, testWS, testProj, wlID1)
		c.DeleteWorkload(t, testWS, testProj, wlID2)
	})

	pod1 := fetchPod(t, "workload-"+wlID1)
	pod2 := fetchPod(t, "workload-"+wlID2)
	require.Len(t, pod1.Spec.Volumes, 1)
	require.Len(t, pod2.Spec.Volumes, 1)
	require.NotNil(t, pod1.Spec.Volumes[0].PersistentVolumeClaim)
	require.NotNil(t, pod2.Spec.Volumes[0].PersistentVolumeClaim)
	assert.NotEqual(t,
		pod1.Spec.Volumes[0].PersistentVolumeClaim.ClaimName,
		pod2.Spec.Volumes[0].PersistentVolumeClaim.ClaimName,
		"each workload must mount a distinct binding PVC")
}

// TestWorkspace_IdempotentCreatePreservesFiles verifies idempotent create does not disturb task workspace data.
func TestWorkspace_IdempotentCreatePreservesFiles(t *testing.T) {
	wlID := uniqueID("ws-idem")
	c := newClient()
	workspacePath := taskWorkspacePath(wlID)

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
	writeResp := c.Exec(t, testWS, testProj, wlID, []string{"sh", "-c", fmt.Sprintf("echo sentinel > %s/sentinel.txt", workspacePath)}, 10)
	require.Equal(t, http.StatusOK, writeResp.StatusCode)

	c.CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	readResp := c.Exec(t, testWS, testProj, wlID, []string{"cat", workspacePath + "/sentinel.txt"}, 10)
	require.Equal(t, http.StatusOK, readResp.StatusCode)
	var execResp ExecResponse
	require.NoError(t, readResp.DecodeJSON(&execResp))
	assert.Equal(t, 0, execResp.ExitCode)
	assert.Contains(t, execResp.Stdout, "sentinel")

	c.DeleteWorkload(t, testWS, testProj, wlID)
}

// TestWorkspace_PodMountUsesBindingPVC verifies the pod mounts the binding PVC with plan-derived paths.
func TestWorkspace_PodMountUsesBindingPVC(t *testing.T) {
	wsID := "ws-plan"
	wlID := uniqueID("plan")
	bindingID := bindingIDForWorkload(wlID)

	_ = mustCreateWorkload(t, wsID, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, wsID, testProj, wlID) })

	podName := "workload-" + wlID
	waitPodPhase(t, suite.Namespace, podName, "Running", 3*time.Minute)

	pod, err := k8sCli.CoreV1().Pods(suite.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, pod.Spec.Containers, 1)
	require.Len(t, pod.Spec.Containers[0].VolumeMounts, 1)
	vm := pod.Spec.Containers[0].VolumeMounts[0]
	assert.Equal(t, taskHomePath(wlID), vm.MountPath)
	assert.Empty(t, vm.SubPath)
	require.NotNil(t, pod.Spec.Volumes[0].PersistentVolumeClaim)
	assert.Equal(t, workspacebinding.PVCName(wsID, testProj, bindingID), pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
}

// ---------------------------------------------------------------------------
// JuiceFS persistence tests (only run when E2E_JUICEFS=true)
//
// These tests verify that files written to task HOME/workspace in a pod persist across
// pod deletion and recreation (i.e., the PVC backend is truly persistent).
// ---------------------------------------------------------------------------

// TestWorkspace_FilePersistsAcrossRestart writes a file in a pod, deletes the pod,
// recreates it with the same workload ID, and verifies the file survives.
func TestWorkspace_FilePersistsAcrossRestart(t *testing.T) {
	if !suite.JuiceFSEnabled {
		t.Skip("skipped: set E2E_JUICEFS=true to enable JuiceFS persistence tests")
	}

	wlID := uniqueID("persist")
	c := newClient()

	// Phase 1: Create pod and write a file.
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	testFile := taskWorkspacePath(wlID) + "/persist-test.txt"
	const testContent = "persistence-check-e2e"
	writeResp := c.Exec(t, testWS, testProj, wlID,
		[]string{"sh", "-c", fmt.Sprintf("echo %s > %s", testContent, testFile)}, 10)
	require.Equal(t, 200, writeResp.StatusCode)

	// Phase 2: Delete the pod.
	mustDeleteWorkload(t, testWS, testProj, wlID)
	waitPodGone(t, suite.Namespace, "workload-"+wlID, 60*time.Second)

	// Phase 3: Recreate with the same workload ID.
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)

	// Phase 4: Verify file still exists.
	readResp := c.Exec(t, testWS, testProj, wlID,
		[]string{"cat", testFile}, 10)
	require.Equal(t, 200, readResp.StatusCode)

	var er ExecResponse
	require.NoError(t, readResp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode, "cat must succeed (file should exist after restart)")
	assert.Contains(t, strings.TrimSpace(er.Stdout), testContent,
		"file content must be preserved across pod restart")

	mustDeleteWorkload(t, testWS, testProj, wlID)
}

// TestWorkspace_TwoWorkloadsHaveIsolatedFilesystems verifies that files written
// in one workload's task workspace are not visible in another workload's task workspace.
func TestWorkspace_TwoWorkloadsHaveIsolatedFilesystems(t *testing.T) {
	if !suite.JuiceFSEnabled {
		t.Skip("skipped: set E2E_JUICEFS=true to enable JuiceFS isolation tests")
	}

	wlID1 := uniqueID("iso-ws-1")
	wlID2 := uniqueID("iso-ws-2")
	c := newClient()

	_ = mustCreateWorkload(t, testWS, testProj, wlID1, CreateRequest{Image: suite.Image})
	_ = mustCreateWorkload(t, testWS, testProj, wlID2, CreateRequest{Image: suite.Image})
	t.Cleanup(func() {
		c.DeleteWorkload(t, testWS, testProj, wlID1)
		c.DeleteWorkload(t, testWS, testProj, wlID2)
	})

	waitWorkloadRunning(t, testWS, testProj, wlID1, 3*time.Minute)
	waitWorkloadRunning(t, testWS, testProj, wlID2, 3*time.Minute)

	// Write a file in wl1.
	writeResp := c.Exec(t, testWS, testProj, wlID1,
		[]string{"sh", "-c", fmt.Sprintf("echo wl1-secret > %s/secret.txt", taskWorkspacePath(wlID1))}, 10)
	require.Equal(t, 200, writeResp.StatusCode)

	// The file must NOT be visible in wl2.
	readResp := c.Exec(t, testWS, testProj, wlID2,
		[]string{"sh", "-c", fmt.Sprintf("test -f %s/secret.txt && echo found || echo absent", taskWorkspacePath(wlID2))}, 10)
	require.Equal(t, 200, readResp.StatusCode)

	var er ExecResponse
	require.NoError(t, readResp.DecodeJSON(&er))
	assert.Contains(t, er.Stdout, "absent",
		"task workspace in wl2 must not see files written in wl1")
}

// TestWorkspace_MultiWorkspaceIsolation verifies that workloads in different
// workspace/project pairs get distinct workspace directories and pods.
func TestWorkspace_MultiWorkspaceIsolation(t *testing.T) {
	ws1, ws2 := "ws-iso-a", "ws-iso-b"
	proj1, proj2 := "proj-1", "proj-2"
	wl1 := uniqueID("mwi-1")
	wl2 := uniqueID("mwi-2")
	c := newClient()

	_ = mustCreateWorkload(t, ws1, proj1, wl1, CreateRequest{Image: suite.Image})
	_ = mustCreateWorkload(t, ws2, proj2, wl2, CreateRequest{Image: suite.Image})
	t.Cleanup(func() {
		c.DeleteWorkload(t, ws1, proj1, wl1)
		c.DeleteWorkload(t, ws2, proj2, wl2)
	})

	waitWorkloadRunning(t, ws1, proj1, wl1, 3*time.Minute)
	waitWorkloadRunning(t, ws2, proj2, wl2, 3*time.Minute)

	// Distinct pods.
	get1 := c.GetWorkload(t, ws1, proj1, wl1)
	get2 := c.GetWorkload(t, ws2, proj2, wl2)
	require.Equal(t, http.StatusOK, get1.StatusCode)
	require.Equal(t, http.StatusOK, get2.StatusCode)
	var ps1, ps2 PodStatus
	require.NoError(t, get1.DecodeJSON(&ps1))
	require.NoError(t, get2.DecodeJSON(&ps2))
	assert.Equal(t, "workload-"+wl1, ps1.PodName)
	assert.Equal(t, "workload-"+wl2, ps2.PodName)
	assert.NotEqual(t, ps1.PodName, ps2.PodName)

	pod1 := fetchPod(t, ps1.PodName)
	pod2 := fetchPod(t, ps2.PodName)
	require.NotNil(t, pod1.Spec.Volumes[0].PersistentVolumeClaim)
	require.NotNil(t, pod2.Spec.Volumes[0].PersistentVolumeClaim)
	assert.NotEqual(t, pod1.Spec.Volumes[0].PersistentVolumeClaim.ClaimName, pod2.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
}
