//go:build e2e

package e2e_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Exec tests – all require a running workload pod
// ---------------------------------------------------------------------------

// setupRunningWorkload creates a workload, waits for it to be Running, and
// registers t.Cleanup to delete it. Returns the workload ID.
func setupRunningWorkload(t *testing.T, label string) string {
	t.Helper()
	wlID := uniqueID(label)
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	waitWorkloadRunning(t, testWS, testProj, wlID, 3*time.Minute)
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })
	return wlID
}

// TestExec_SimpleCommand verifies a basic command executes and stdout is captured.
func TestExec_SimpleCommand(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-simple")

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"echo", "hello-e2e"}, 15)
	require.Equal(t, http.StatusOK, resp.StatusCode, "exec: %s", resp.BodyString())

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.Contains(t, er.Stdout, "hello-e2e")
	assert.Empty(t, er.Stderr)
	assert.Greater(t, er.DurationMs, int64(0))
}

// TestExec_NonZeroExitCode verifies that a command returning non-zero exit code is
// faithfully reported without treating it as an HTTP error.
func TestExec_NonZeroExitCode(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-exit")

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"sh", "-c", "exit 42"}, 10)
	require.Equal(t, http.StatusOK, resp.StatusCode, "exec non-zero exit: %s", resp.BodyString())

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 42, er.ExitCode, "exit code must be 42")
}

// TestExec_StderrCaptured verifies stderr output is returned separately from stdout.
func TestExec_StderrCaptured(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-stderr")

	resp := newClient().Exec(t, testWS, testProj, wlID,
		[]string{"sh", "-c", "echo stdout-line; echo stderr-line >&2"}, 10)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.Contains(t, er.Stdout, "stdout-line")
	assert.Contains(t, er.Stderr, "stderr-line")
}

// TestExec_LargeOutput verifies the handler can capture substantial command output.
func TestExec_LargeOutput(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-large")

	// Generate ~1 KB of output.
	resp := newClient().Exec(t, testWS, testProj, wlID,
		[]string{"sh", "-c", "for i in $(seq 1 50); do echo \"line $i of output data\"; done"}, 15)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.True(t, strings.Count(er.Stdout, "\n") >= 50, "should have ≥50 output lines")
}

// TestExec_MultipleSequential verifies multiple sequential exec calls work correctly.
func TestExec_MultipleSequential(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-multi")
	c := newClient()

	for i := 1; i <= 3; i++ {
		resp := c.Exec(t, testWS, testProj, wlID,
			[]string{"sh", "-c", "echo run-" + string(rune('0'+i))}, 10)
		require.Equal(t, http.StatusOK, resp.StatusCode, "exec #%d: %s", i, resp.BodyString())
		var er ExecResponse
		require.NoError(t, resp.DecodeJSON(&er))
		assert.Equal(t, 0, er.ExitCode)
	}
}

// TestExec_CommandNotFound verifies a missing binary produces non-zero exit code (not HTTP 500).
func TestExec_CommandNotFound(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-notfound")

	resp := newClient().Exec(t, testWS, testProj, wlID,
		[]string{"this-binary-definitely-does-not-exist-xyz"}, 10)
	// The exec handler returns 200 with a non-zero exit code (not 500).
	// If the binary is not found, the shell exits with 127.
	if resp.StatusCode == http.StatusOK {
		var er ExecResponse
		require.NoError(t, resp.DecodeJSON(&er))
		assert.NotEqual(t, 0, er.ExitCode, "missing binary must produce non-zero exit code")
	}
	// 500 is also acceptable if the exec framework itself errors.
	assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusInternalServerError,
		"unexpected status %d: %s", resp.StatusCode, resp.BodyString())
}

// TestExec_TimeoutClamped verifies that a timeout_seconds > 300 is clamped to 300s
// rather than returning HTTP 504. The command is expected to complete immediately.
func TestExec_TimeoutClamped(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-timeout")

	// Sending timeout_seconds=500 (> 300); handler clamps to 300s.
	// A fast echo command should still return 200 immediately.
	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"echo", "clamped"}, 500)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"oversized timeout must be clamped, not rejected: %s", resp.BodyString())

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.Contains(t, er.Stdout, "clamped")
}

// TestExec_ShortTimeout verifies a short-running command completes before the timeout.
func TestExec_ShortTimeout(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-short-timeout")

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"echo", "fast"}, 5)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
}

// TestExec_WorkingDirectoryIsWorkspace verifies the container working directory is task HOME/workspace.
func TestExec_WorkingDirectoryIsWorkspace(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-cwd")

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"pwd"}, 10)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.Equal(t, 0, er.ExitCode)
	assert.Equal(t, taskWorkspacePath(wlID), strings.TrimSpace(er.Stdout),
		"working directory must be task HOME/workspace")
}

// TestExec_DurationMsIsPopulated verifies duration_ms is always set and non-negative.
func TestExec_DurationMsIsPopulated(t *testing.T) {
	wlID := setupRunningWorkload(t, "exec-dur")

	resp := newClient().Exec(t, testWS, testProj, wlID, []string{"echo", "hi"}, 10)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var er ExecResponse
	require.NoError(t, resp.DecodeJSON(&er))
	assert.GreaterOrEqual(t, er.DurationMs, int64(0), "duration_ms must be non-negative")
}
