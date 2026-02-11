package connection

import (
	"context"
	"fmt"
	"sync"

	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

// ShellBridgeClient defines the interface for shell bridge client operations.
// This allows for mocking in tests and flexibility in implementation.
type ShellBridgeClient interface {
	Connect(ctx context.Context) error
	ExecCommand(ctx context.Context, shell, command string, env []string) error
	ReceiveOutput(ctx context.Context) (*shellbridge.Output, error)
	SendSignal(ctx context.Context, signal string) error
	Close() error
}

// Manager manages on-demand shell bridge connections for sandboxes.
// It ensures connections are created only when needed and reused when available.
type Manager struct {
	sandboxManager *sandbox.Manager
	logger         observability.Logger
	mu             sync.RWMutex
}

// NewManager creates a new connection manager.
func NewManager(sandboxManager *sandbox.Manager) *Manager {
	return &Manager{
		sandboxManager: sandboxManager,
		logger:         observability.GetLogger(),
	}
}

// EnsureConnection ensures a shell bridge connection exists for the given sandbox.
// If a connection already exists, it returns the existing connection.
// If no connection exists, it creates a new one.
// Returns an error if the sandbox is not found or connection creation fails.
func (m *Manager) EnsureConnection(ctx context.Context, sandboxID string) (ShellBridgeClient, error) {
	// Get the sandbox
	sbox, ok := m.sandboxManager.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Check if connection already exists (with read lock first for efficiency)
	sbox.ConnectionMu.RLock()
	if conn := sbox.BridgeConnection; conn != nil {
		sbox.ConnectionMu.RUnlock()
		if shellBridgeClient, ok := conn.(ShellBridgeClient); ok {
			m.logger.Debug("Reusing existing shell bridge connection for sandbox %s", sandboxID)
			return shellBridgeClient, nil
		}
		// Connection exists but is not a valid ShellBridgeClient, clear it
		sbox.ConnectionMu.RUnlock()
		sbox.ConnectionMu.Lock()
		sbox.BridgeConnection = nil
		sbox.ConnectionMu.Unlock()
	} else {
		sbox.ConnectionMu.RUnlock()
	}

	// No valid connection exists, create a new one
	sbox.ConnectionMu.Lock()
	defer sbox.ConnectionMu.Unlock()

	// Double-check after acquiring write lock (in case another goroutine created one)
	if conn := sbox.BridgeConnection; conn != nil {
		if shellBridgeClient, ok := conn.(ShellBridgeClient); ok {
			m.logger.Debug("Reusing shell bridge connection (double-check) for sandbox %s", sandboxID)
			return shellBridgeClient, nil
		}
	}

	// Validate sandbox has a PodIP for connection
	if sbox.PodIP == "" {
		return nil, fmt.Errorf("sandbox %s has no PodIP, cannot create shell bridge connection", sandboxID)
	}

	// Create new shell bridge client
	client := shellbridge.NewClient(sbox.PodIP, shellbridge.DefaultPort)

	// Set up OnClose callback to cleanup connection when shell bridge closes
	client.OnClose(func() {
		m.HandleBridgeClose(sandboxID)
	})

	// Connect to the shell bridge
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to shell bridge for sandbox %s: %w", sandboxID, err)
	}

	// Wrap the client to satisfy our interface
	wrappedClient := &shellBridgeClientWrapper{client: client}

	// Store the connection in the sandbox
	sbox.BridgeConnection = wrappedClient

	m.logger.Info("Created new shell bridge connection for sandbox %s (PodIP: %s)", sandboxID, sbox.PodIP)

	return wrappedClient, nil
}

// HandleBridgeClose handles the closure of a shell bridge connection for a sandbox.
// This should be called when the shell bridge connection is closed (normally or abnormally).
func (m *Manager) HandleBridgeClose(sandboxID string) {
	sbox, ok := m.sandboxManager.Get(sandboxID)
	if !ok {
		m.logger.Warn("HandleBridgeClose called for non-existent sandbox: %s", sandboxID)
		return
	}

	sbox.ConnectionMu.Lock()
	defer sbox.ConnectionMu.Unlock()

	// Close the connection if it exists
	if conn := sbox.BridgeConnection; conn != nil {
		if shellBridgeClient, ok := conn.(ShellBridgeClient); ok {
			if err := shellBridgeClient.Close(); err != nil {
				m.logger.Warn("Error closing shell bridge connection for sandbox %s: %v", sandboxID, err)
			}
		}
		sbox.BridgeConnection = nil
	}

	m.logger.Info("Cleared shell bridge connection for sandbox %s", sandboxID)
}

// shellBridgeClientWrapper wraps the shellbridge.Client to satisfy ShellBridgeClient interface.
type shellBridgeClientWrapper struct {
	client *shellbridge.Client
}

// Connect forwards to the underlying client's Connect method.
func (w *shellBridgeClientWrapper) Connect(ctx context.Context) error {
	return w.client.Connect(ctx)
}

// ExecCommand forwards to the underlying client's ExecCommand method.
func (w *shellBridgeClientWrapper) ExecCommand(ctx context.Context, shell, command string, env []string) error {
	return w.client.ExecCommand(ctx, shell, command, env)
}

// ReceiveOutput forwards to the underlying client's ReceiveOutput method.
func (w *shellBridgeClientWrapper) ReceiveOutput(ctx context.Context) (*shellbridge.Output, error) {
	return w.client.ReceiveOutput(ctx)
}

// SendSignal forwards to the underlying client's SendSignal method.
func (w *shellBridgeClientWrapper) SendSignal(ctx context.Context, signal string) error {
	return w.client.SendSignal(ctx, signal)
}

// Close forwards to the underlying client's Close method.
func (w *shellBridgeClientWrapper) Close() error {
	return w.client.Close()
}
