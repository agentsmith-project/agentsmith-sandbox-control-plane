//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_CreateSandbox verifies that a sandbox can be created and returns
// the expected podName and expiresAt fields.
func TestE2E_CreateSandbox(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	resp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{
		TTLSeconds: 300,
	})
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 on sandbox creation")

	var body createSandboxResponse
	err := json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	assert.NotEmpty(t, body.PodName, "podName should not be empty")
	assert.NotEmpty(t, body.ExpiresAt, "expiresAt should not be empty")

	// Cleanup
	cleanResp := c.deleteSandbox(ctx, t, sessionID)
	cleanResp.Body.Close()
}

// TestE2E_ExecSimpleCommand creates a sandbox, runs `echo hello`, and verifies
// the SSE stream contains the expected stdout output and a zero exit code.
func TestE2E_ExecSimpleCommand(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	// Create sandbox
	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	// Execute command
	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd:            []string{"echo", "hello"},
		TimeoutSeconds: 30,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)
	assert.Contains(t, execResp.Header.Get("Content-Type"), "text/event-stream")

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)
	require.NotEmpty(t, events, "expected at least one SSE event")

	// Collect stdout output
	var stdout string
	for _, ev := range events {
		if ev.Event == "stdout" {
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdout += decoded
		}
	}
	assert.Contains(t, stdout, "hello", "stdout should contain 'hello'")

	// Verify exit event
	var foundExit bool
	for _, ev := range events {
		if ev.Event == "exit" {
			foundExit = true
			var exitData sseExitData
			err := json.Unmarshal([]byte(ev.Data), &exitData)
			require.NoError(t, err)
			assert.Equal(t, 0, exitData.ExitCode, "exit code should be 0")
			assert.Greater(t, exitData.DurationMs, int64(0), "duration should be positive")
		}
	}
	assert.True(t, foundExit, "expected an exit event in the SSE stream")
}

// TestE2E_ExecCommandFails executes a command that should fail and verifies
// a non-zero exit code or the presence of stderr events.
func TestE2E_ExecCommandFails(t *testing.T) {
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
		Cmd:            []string{"ls", "/nonexistent_path_e2e_test"},
		TimeoutSeconds: 30,
	})
	defer execResp.Body.Close()

	require.Equal(t, http.StatusOK, execResp.StatusCode)

	events, err := parseSSEEvents(execResp.Body)
	require.NoError(t, err)

	var hasNonZeroExit bool
	var hasStderr bool
	for _, ev := range events {
		switch ev.Event {
		case "exit":
			var exitData sseExitData
			err := json.Unmarshal([]byte(ev.Data), &exitData)
			require.NoError(t, err)
			if exitData.ExitCode != 0 {
				hasNonZeroExit = true
			}
		case "stderr":
			hasStderr = true
		}
	}

	assert.True(t, hasNonZeroExit || hasStderr,
		"expected non-zero exit code or stderr event for a failing command")
}

// TestE2E_TouchSandbox creates a sandbox, touches it, and verifies a 200 OK.
func TestE2E_TouchSandbox(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	touchResp := c.touchSandbox(ctx, t, sessionID)
	defer touchResp.Body.Close()

	assert.Equal(t, http.StatusOK, touchResp.StatusCode, "touch should return 200")
}

// TestE2E_DeleteSandbox creates a sandbox and immediately deletes it.
func TestE2E_DeleteSandbox(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)

	deleteResp := c.deleteSandbox(ctx, t, sessionID)
	defer deleteResp.Body.Close()

	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode, "delete should return 204")
}

// TestE2E_FullLifecycle exercises the complete sandbox lifecycle:
// create → exec → touch → delete.
func TestE2E_FullLifecycle(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	// --- Create ---
	t.Log("step: create sandbox")
	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createBody, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode, "create failed: %s", string(createBody))

	var createData createSandboxResponse
	require.NoError(t, json.Unmarshal(createBody, &createData))
	assert.NotEmpty(t, createData.PodName)
	assert.NotEmpty(t, createData.ExpiresAt)

	// --- Exec ---
	t.Log("step: exec command")
	execResp := c.execCommand(ctx, t, sessionID, execRequest{
		Cmd:            []string{"echo", "lifecycle-test"},
		TimeoutSeconds: 30,
	})
	events, err := parseSSEEvents(execResp.Body)
	execResp.Body.Close()
	require.NoError(t, err)

	var stdout string
	for _, ev := range events {
		if ev.Event == "stdout" {
			decoded, err := decodeSSEOutputData(ev.Data)
			require.NoError(t, err)
			stdout += decoded
		}
	}
	assert.Contains(t, stdout, "lifecycle-test")

	// --- Touch ---
	t.Log("step: touch sandbox")
	touchResp := c.touchSandbox(ctx, t, sessionID)
	touchResp.Body.Close()
	assert.Equal(t, http.StatusOK, touchResp.StatusCode)

	// --- Delete ---
	t.Log("step: delete sandbox")
	deleteResp := c.deleteSandbox(ctx, t, sessionID)
	deleteResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode)
}
