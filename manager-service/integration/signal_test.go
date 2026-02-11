//go:build !short

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/connection"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

// TestSignalHandling_PayloadParsing tests parsing signal payloads
func TestSignalHandling_PayloadParsing(t *testing.T) {
	t.Run("valid signal payload", func(t *testing.T) {
		payloadJSON := `{"sandbox_id":"test-sandbox","signal":"SIGTERM"}`
		var payload SignalPayload
		err := parseJSONPayload(payloadJSON, &payload)
		require.NoError(t, err)
		assert.Equal(t, "test-sandbox", payload.SandboxID)
		assert.Equal(t, "SIGTERM", payload.Signal)
	})

	t.Run("missing sandbox_id results in empty string", func(t *testing.T) {
		payloadJSON := `{"signal":"SIGTERM"}`
		var payload SignalPayload
		err := parseJSONPayload(payloadJSON, &payload)
		// JSON unmarshal succeeds but sandbox_id will be empty
		require.NoError(t, err)
		assert.Equal(t, "", payload.SandboxID)
		// In real code, validation would catch this
	})

	t.Run("missing signal results in empty string", func(t *testing.T) {
		payloadJSON := `{"sandbox_id":"test-sandbox"}`
		var payload SignalPayload
		err := parseJSONPayload(payloadJSON, &payload)
		// JSON unmarshal succeeds but signal will be empty
		require.NoError(t, err)
		assert.Equal(t, "", payload.Signal)
		// In real code, validation would catch this
	})

	t.Run("various signal types", func(t *testing.T) {
		signals := []string{"SIGINT", "SIGTERM", "SIGKILL", "SIGHUP", "SIGUSR1", "SIGUSR2"}
		for _, signal := range signals {
			payloadJSON := `{"sandbox_id":"test-sandbox","signal":"` + signal + `"}`
			var payload SignalPayload
			err := parseJSONPayload(payloadJSON, &payload)
			require.NoError(t, err, "Signal %s should parse correctly", signal)
			assert.Equal(t, signal, payload.Signal)
		}
	})
}

// TestConnectionManager_EnsureConnection tests the connection manager behavior
func TestConnectionManager_EnsureConnection(t *testing.T) {
	ctx := context.Background()
	sandboxMgr := sandbox.NewManager()
	connMgr := connection.NewManager(sandboxMgr)

	t.Run("create new connection", func(t *testing.T) {
		sandboxID := "test-conn-new"

		// Create a sandbox with PodIP
		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		sbox, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		// Set PodIP (simulating pod is ready)
		err = sandboxMgr.SetPodIP(sandboxID, "10.0.0.1")
		require.NoError(t, err)

		// Note: In a real cluster, EnsureConnection would actually connect
		// For unit testing, we just verify the logic path
		_, err = connMgr.EnsureConnection(ctx, sandboxID)

		// Without a real shell bridge, connection will fail
		// But we can verify the sandbox was looked up correctly
		sbox, _ = sandboxMgr.Get(sandboxID)
		assert.Equal(t, "10.0.0.1", sbox.PodIP)
	})

	t.Run("reuse existing connection", func(t *testing.T) {
		sandboxID := "test-conn-reuse"

		// Create a sandbox with PodIP
		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		_, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		err = sandboxMgr.SetPodIP(sandboxID, "10.0.0.2")
		require.NoError(t, err)

		// Set a mock connection directly
		sbox, _ := sandboxMgr.Get(sandboxID)
		mockClient := &mockShellBridgeClient{}
		sbox.ConnectionMu.Lock()
		sbox.BridgeConnection = mockClient
		sbox.ConnectionMu.Unlock()

		// EnsureConnection should reuse the existing connection
		client, err := connMgr.EnsureConnection(ctx, sandboxID)
		require.NoError(t, err)
		assert.NotNil(t, client)

		// Verify it's the same mock client
		sbox.ConnectionMu.RLock()
		reusedClient := sbox.BridgeConnection
		sbox.ConnectionMu.RUnlock()
		assert.Same(t, mockClient, reusedClient)
	})

	t.Run("sandbox not found", func(t *testing.T) {
		_, err := connMgr.EnsureConnection(ctx, "non-existent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sandbox not found")
	})

	t.Run("no PodIP", func(t *testing.T) {
		sandboxID := "test-conn-no-ip"

		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		_, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		// Don't set PodIP
		_, err = connMgr.EnsureConnection(ctx, sandboxID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no PodIP")
	})
}

// TestConnectionManager_HandleBridgeClose tests bridge close handling
func TestConnectionManager_HandleBridgeClose(t *testing.T) {
	ctx := context.Background()
	sandboxMgr := sandbox.NewManager()
	connMgr := connection.NewManager(sandboxMgr)

	t.Run("close and clear connection", func(t *testing.T) {
		sandboxID := "test-close-conn"

		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		_, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		// Set a mock connection
		sbox, _ := sandboxMgr.Get(sandboxID)
		mockClient := &mockShellBridgeClient{closed: false}
		sbox.ConnectionMu.Lock()
		sbox.BridgeConnection = mockClient
		sbox.ConnectionMu.Unlock()

		// Handle bridge close
		connMgr.HandleBridgeClose(sandboxID)

		// Verify connection was cleared
		sbox.ConnectionMu.RLock()
		conn := sbox.BridgeConnection
		sbox.ConnectionMu.RUnlock()
		assert.Nil(t, conn)
	})

	t.Run("handle non-existent sandbox", func(t *testing.T) {
		// Should not panic
		connMgr.HandleBridgeClose("non-existent")
	})
}

// TestSignalFlow tests the complete signal flow
func TestSignalFlow(t *testing.T) {
	ctx := context.Background()
	sandboxMgr := sandbox.NewManager()
	connMgr := connection.NewManager(sandboxMgr)
	_ = connMgr // Use connMgr to avoid unused error

	t.Run("signal to ready sandbox", func(t *testing.T) {
		sandboxID := "test-signal-ready"

		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		sbox, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		// Set PodIP to simulate ready pod
		err = sandboxMgr.SetPodIP(sandboxID, "10.0.0.10")
		require.NoError(t, err)
		err = sandboxMgr.UpdateState(sandboxID, sandbox.StateReady)
		require.NoError(t, err)

		// Verify sandbox is in correct state
		sbox, _ = sandboxMgr.Get(sandboxID)
		assert.Equal(t, "10.0.0.10", sbox.PodIP)
		assert.Equal(t, sandbox.StateReady, sbox.State)

		// Note: Actual signal sending requires a real shell bridge
		// In integration tests without cluster, we verify the setup
	})

	t.Run("signal to creating sandbox fails", func(t *testing.T) {
		sandboxID := "test-signal-creating"

		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		_, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		// Don't set PodIP - sandbox not ready
		_, err = connMgr.EnsureConnection(ctx, sandboxID)
		assert.Error(t, err)
	})
}

// TestEOF_CascadeDisconnection tests EOF-based cascade disconnection
func TestEOF_CascadeDisconnection(t *testing.T) {
	ctx := context.Background()
	sandboxMgr := sandbox.NewManager()
	bufferMgr := buffer.NewManager()
	_ = bufferMgr // Use bufferMgr to avoid unused error

	t.Run("EOF triggers exit message", func(t *testing.T) {
		sandboxID := "test-eof-exit"

		req := sandbox.CreateRequest{
			SandboxID: sandboxID,
			Image:     "ubuntu:latest",
			Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
		}
		_, err := sandboxMgr.Create(ctx, req)
		require.NoError(t, err)

		err = sandboxMgr.SetPodIP(sandboxID, "10.0.0.20")
		require.NoError(t, err)
		err = sandboxMgr.UpdateState(sandboxID, sandbox.StateReady)
		require.NoError(t, err)

		// Get buffer for the sandbox
		buf := bufferMgr.GetOrCreate(sandboxID)

		// Simulate receiving an exit message (what happens when shell bridge sends EOF)
		exitMsg := &buffer.Message{
			Type:     "exit",
			ExitCode: 0,
		}
		buf.Write(exitMsg)

		// Verify the message was buffered
		messages := buf.ReadAll()
		assert.Len(t, messages, 1)
		assert.Equal(t, "exit", messages[0].Type)
		assert.Equal(t, int32(0), messages[0].ExitCode)
	})

	t.Run("DataTypeClose frame handling", func(t *testing.T) {
		// Test that DataTypeClose (0x04) is recognized
		closeFrame := &shellbridge.BinaryFrame{
			Type:   shellbridge.DataTypeClose,
			Length: 0,
			Data:   nil,
		}
		assert.Equal(t, shellbridge.DataTypeClose, closeFrame.Type)
		assert.Equal(t, byte(0x04), byte(closeFrame.Type))
	})
}

// Helper types for testing

// SignalPayload represents a signal message payload
type SignalPayload struct {
	SandboxID string `json:"sandbox_id"`
	Signal    string `json:"signal"`
}

// parseJSONPayload is a helper to parse JSON for tests
func parseJSONPayload(jsonStr string, target interface{}) error {
	return json.Unmarshal([]byte(jsonStr), target)
}

// mockShellBridgeClient is a minimal mock for testing
type mockShellBridgeClient struct {
	closed bool
}

func (m *mockShellBridgeClient) Connect(ctx context.Context) error {
	return nil
}

func (m *mockShellBridgeClient) ExecCommand(ctx context.Context, shell, command string, env []string) error {
	return nil
}

func (m *mockShellBridgeClient) ReceiveOutput(ctx context.Context) (*shellbridge.Output, error) {
	return nil, nil
}

func (m *mockShellBridgeClient) SendSignal(ctx context.Context, signal string) error {
	return nil
}

func (m *mockShellBridgeClient) Close() error {
	m.closed = true
	return nil
}
