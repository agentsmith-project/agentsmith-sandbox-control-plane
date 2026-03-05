//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Workspace directory tests
//
// These tests validate the workspace lifecycle managed by the sandbox manager:
//   - Manager creates {workspacePath}/{wsID}/{wlID} on the host at pod creation time
//   - The pod mounts the PVC with subPath {wsID}/{wlID} at /workspace
//   - Files written persist across pod restarts iff the PVC is persistent (JuiceFS)
// ---------------------------------------------------------------------------

// TestWorkspace_DirectoryCreatedOnCreate verifies that creating a workload causes
// the manager to create the workspace directory on the host filesystem.
func TestWorkspace_DirectoryCreatedOnCreate(t *testing.T) {
	wlID := uniqueID("ws-mkdir")

	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	expectedDir := filepath.Join(suite.WorkspacePath, testWS, wlID)
	info, err := os.Stat(expectedDir)
	require.NoError(t, err, "workspace directory must exist at %s after create", expectedDir)
	assert.True(t, info.IsDir(), "workspace path must be a directory")
}

// TestWorkspace_DirectoryIsolatedPerWorkload verifies that two workloads with different
// IDs in the same workspace receive distinct subdirectories.
func TestWorkspace_DirectoryIsolatedPerWorkload(t *testing.T) {
	wlID1 := uniqueID("ws-iso-1")
	wlID2 := uniqueID("ws-iso-2")
	c := newClient()

	_ = mustCreateWorkload(t, testWS, testProj, wlID1, CreateRequest{Image: suite.Image})
	_ = mustCreateWorkload(t, testWS, testProj, wlID2, CreateRequest{Image: suite.Image})
	t.Cleanup(func() {
		c.DeleteWorkload(t, testWS, testProj, wlID1)
		c.DeleteWorkload(t, testWS, testProj, wlID2)
	})

	dir1 := filepath.Join(suite.WorkspacePath, testWS, wlID1)
	dir2 := filepath.Join(suite.WorkspacePath, testWS, wlID2)
	assert.NotEqual(t, dir1, dir2, "each workload must have a distinct workspace directory")

	_, err1 := os.Stat(dir1)
	_, err2 := os.Stat(dir2)
	assert.NoError(t, err1, "workspace dir for wlID1 must exist")
	assert.NoError(t, err2, "workspace dir for wlID2 must exist")
}

// TestWorkspace_DirectoryIdempotent verifies that recreating an existing workload
// does not corrupt the existing workspace directory.
func TestWorkspace_DirectoryIdempotent(t *testing.T) {
	wlID := uniqueID("ws-idem")
	c := newClient()

	// Create – workspace dir appears.
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	wsDir := filepath.Join(suite.WorkspacePath, testWS, wlID)
	_, err := os.Stat(wsDir)
	require.NoError(t, err)

	// Write a sentinel file into the workspace dir on the host.
	sentinelPath := filepath.Join(wsDir, "sentinel.txt")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("sentinel"), 0644))

	// Create again (idempotent) – must not delete the sentinel.
	c.CreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	_, err = os.Stat(sentinelPath)
	assert.NoError(t, err, "idempotent create must not delete existing workspace files")

	c.DeleteWorkload(t, testWS, testProj, wlID)
}

// TestWorkspace_SubPathFormat verifies the subPath used for the PVC volume mount
// is {wsID}/{wlID} (not flattened or escaped).
func TestWorkspace_SubPathFormat(t *testing.T) {
	wsID := "ws-subpath"
	wlID := uniqueID("subpath")

	_ = mustCreateWorkload(t, wsID, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, wsID, testProj, wlID) })

	// Validate workspace dir is at {basePath}/{wsID}/{wlID}
	expected := filepath.Join(suite.WorkspacePath, wsID, wlID)
	_, err := os.Stat(expected)
	require.NoError(t, err, "workspace dir must be at %s", expected)

	// Also verify via the K8s pod spec.
	podName := "workload-" + wlID
	waitPodPhase(t, suite.Namespace, podName, "Running", 3*time.Minute)

	pod, err := k8sCli.CoreV1().Pods(suite.Namespace).Get(
		context.Background(), podName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, pod.Spec.Containers, 1)
	require.Len(t, pod.Spec.Containers[0].VolumeMounts, 1)
	vm := pod.Spec.Containers[0].VolumeMounts[0]
	assert.Equal(t, "/workspace", vm.MountPath)
	assert.Equal(t, fmt.Sprintf("%s/%s", wsID, wlID), vm.SubPath,
		"pod volume subPath must be {wsID}/{wlID}")
}

// ---------------------------------------------------------------------------
// JuiceFS persistence tests (only run when E2E_JUICEFS=true)
//
// These tests verify that files written to /workspace in a pod persist across
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

	const testFile = "/workspace/persist-test.txt"
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
// in one workload's /workspace are not visible in another workload's /workspace.
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
		[]string{"sh", "-c", "echo wl1-secret > /workspace/secret.txt"}, 10)
	require.Equal(t, 200, writeResp.StatusCode)

	// The file must NOT be visible in wl2.
	readResp := c.Exec(t, testWS, testProj, wlID2,
		[]string{"sh", "-c", "test -f /workspace/secret.txt && echo found || echo absent"}, 10)
	require.Equal(t, 200, readResp.StatusCode)

	var er ExecResponse
	require.NoError(t, readResp.DecodeJSON(&er))
	assert.Contains(t, er.Stdout, "absent",
		"/workspace in wl2 must not see files written in wl1")
}

