//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_ExecWithEnvVars verifies that custom environment variables are
// propagated into the sandbox execution environment.
func TestE2E_ExecWithEnvVars(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd: []string{"sh", "-c", "echo $MY_TEST_VAR"},
		Env: map[string]string{
			"MY_TEST_VAR": "e2e_env_value",
		},
		TimeoutSeconds: 30,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)

	var stdout string
	for _, ev := range events {
		if ev.Event == "stdout" {
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdout += decoded
		}
	}

	assert.Contains(t, stdout, "e2e_env_value",
		"stdout should contain the value of MY_TEST_VAR")
}

// TestE2E_ExecWithWorkdir verifies that the workdir option is respected
// by running `pwd` in a custom directory.
func TestE2E_ExecWithWorkdir(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd:            []string{"pwd"},
		Workdir:        "/tmp",
		TimeoutSeconds: 30,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)

	var stdout string
	for _, ev := range events {
		if ev.Event == "stdout" {
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdout += decoded
		}
	}

	assert.Contains(t, strings.TrimSpace(stdout), "/tmp",
		"pwd output should reflect the custom workdir /tmp")
}

// TestE2E_ExecStdoutAndStderr runs a command that writes to both stdout and
// stderr and verifies that both event types appear in the SSE stream.
func TestE2E_ExecStdoutAndStderr(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd:            []string{"sh", "-c", "echo out_msg && echo err_msg >&2"},
		TimeoutSeconds: 30,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)

	var hasStdout, hasStderr, hasExit bool
	var stdoutText, stderrText string

	for _, ev := range events {
		switch ev.Event {
		case "stdout":
			hasStdout = true
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdoutText += decoded
		case "stderr":
			hasStderr = true
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stderrText += decoded
		case "exit":
			hasExit = true
		}
	}

	assert.True(t, hasStdout, "expected at least one stdout event")
	assert.True(t, hasStderr, "expected at least one stderr event")
	assert.True(t, hasExit, "expected an exit event")
	assert.Contains(t, stdoutText, "out_msg", "stdout should contain 'out_msg'")
	assert.Contains(t, stderrText, "err_msg", "stderr should contain 'err_msg'")
}

// TestE2E_ExecLargeOutput runs a command that generates a significant amount
// of stdout and verifies all data arrives through the SSE stream.
func TestE2E_ExecLargeOutput(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	// Generate 1000 lines of output (each line ~80 chars)
	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd:            []string{"sh", "-c", "seq 1 1000"},
		TimeoutSeconds: 60,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)

	var stdout string
	var exitCode int
	var foundExit bool

	for _, ev := range events {
		switch ev.Event {
		case "stdout":
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdout += decoded
		case "exit":
			foundExit = true
			var exitData sseExitData
			require.NoError(t, json.Unmarshal([]byte(ev.Data), &exitData))
			exitCode = exitData.ExitCode
		}
	}

	assert.True(t, foundExit, "expected an exit event")
	assert.Equal(t, 0, exitCode, "exit code should be 0")

	// Verify that the output contains the first and last numbers
	assert.Contains(t, stdout, "1\n", "stdout should contain line '1'")
	assert.Contains(t, stdout, "1000\n", "stdout should contain line '1000'")

	// Rough check: we expect at least 1000 lines worth of content
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.GreaterOrEqual(t, len(lines), 1000,
		"expected at least 1000 lines of output, got %d", len(lines))
}
