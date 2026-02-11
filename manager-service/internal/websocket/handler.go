package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	v1 "k8s.io/api/core/v1"
	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/connection"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
	"github.com/sandbox/manager/internal/storage"
)

// Handler manages WebSocket connections and sandbox sessions
type Handler struct {
	sandboxManager    *sandbox.Manager
	bufferManager     *buffer.Manager
	k8sClient         *k8s.Client
	storageClient     *storage.Client
	connectionManager *connection.Manager
	podNamespace      string
	logger            observability.Logger
	cfg               *config.Config
	upgrader          *websocket.Upgrader
}

// NewHandler creates a new WebSocket handler
func NewHandler(
	sandboxManager *sandbox.Manager,
	bufferManager *buffer.Manager,
	k8sClient *k8s.Client,
	storageClient *storage.Client,
	connectionManager *connection.Manager,
	podNamespace string,
	cfg *config.Config,
) *Handler {
	// Build WebSocket config from app config
	wsCfg := &Config{
		ReadBufferSize:          cfg.WebSocket.ReadBufferSize,
		WriteBufferSize:         cfg.WebSocket.WriteBufferSize,
		AllowedOrigins:          cfg.WebSocket.AllowedOrigins,
		AllowNonBrowserRequests: cfg.WebSocket.AllowNonBrowserRequests,
	}
	if cfg.WebSocket.HandshakeTimeout != "" {
		if d, err := time.ParseDuration(cfg.WebSocket.HandshakeTimeout); err == nil {
			wsCfg.HandshakeTimeout = d
		}
	}

	return &Handler{
		sandboxManager:    sandboxManager,
		bufferManager:     bufferManager,
		k8sClient:         k8sClient,
		storageClient:     storageClient,
		connectionManager: connectionManager,
		podNamespace:      podNamespace,
		logger:            observability.GetLogger(),
		cfg:               cfg,
		upgrader:          wsCfg.Upgrader(),
	}
}

// ServeHTTP handles WebSocket upgrade and connection
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket with configured upgrader
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("WebSocket upgrade failed from %s: %w", r.RemoteAddr, err)
		http.Error(w, "websocket upgrade failed", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	ctx := r.Context()
	h.logger.Info("WebSocket connection established from %s", r.RemoteAddr)

	// Handle connection
	h.handleConnection(ctx, conn)
}

// handleConnection manages the WebSocket connection lifecycle
func (h *Handler) handleConnection(ctx context.Context, conn *websocket.Conn) {
	var sandboxID string
	var sess *sandbox.Sandbox
	var isNewSession bool    // Track if we created a new session
	var cleanupMu sync.Mutex // Mutex to protect cleanupDone flag
	cleanupDone := false     // Flag to prevent double cleanup

	// Defer cleanup: only clean up new sessions on connection close
	// Existing sessions are preserved for reconnection
	defer func() {
		cleanupMu.Lock()
		alreadyCleaned := cleanupDone
		cleanupDone = true
		cleanupMu.Unlock()

		if sandboxID != "" && isNewSession && !alreadyCleaned {
			h.logger.Debug("Cleaning up new session %s", sandboxID)
			h.sandboxManager.Delete(sandboxID)
			h.bufferManager.Delete(sandboxID)
		}
	}()

	// Set read deadline for initial message
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Wait for create message
	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				h.logger.Debug("WebSocket closed normally during initial read: %v", err)
			} else if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				h.logger.Warn("WebSocket closed unexpectedly during initial read: %w", err)
			} else {
				h.logger.Warn("Failed to read initial WebSocket message: %w", err)
			}
			return
		}

		switch msg.Type {
		case TypeCreate:
			payload, err := h.parseCreate(msg.Data)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Invalid create payload: %v", err))
				h.logger.Warn("Failed to parse create payload: %w", err)
				return
			}
			sandboxID = payload.SandboxID

			sess, isNewSession, err = h.handleCreate(ctx, payload, conn)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Create failed: %v", err))
				if isContextCanceled(err) {
					h.logger.Debug("Create canceled for session %s: %w", payload.SandboxID, err)
				} else {
					h.logger.Error("Failed to handle create for session %s: %w", payload.SandboxID, err)
				}
				return
			}
			break

		default:
			h.sendError(conn, "Expected create message first")
			h.logger.Warn("Received non-create message first: %s", msg.Type)
			return
		}

		if sess != nil {
			break
		}
	}

	// Clear read deadline after successful create
	conn.SetReadDeadline(time.Time{})

	// Attach to existing session
	if err := h.attachSession(ctx, sandboxID, conn); err != nil {
		h.sendError(conn, fmt.Sprintf("Attach failed: %v", err))
		if isContextCanceled(err) {
			h.logger.Debug("Attach canceled for session %s: %w", sandboxID, err)
		} else {
			h.logger.Error("Failed to attach to session %s: %w", sandboxID, err)
		}
	}
}

// parseCreate parses the create message payload
func (h *Handler) parseCreate(data json.RawMessage) (CreatePayload, error) {
	var payload CreatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return CreatePayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	if payload.SandboxID == "" {
		return CreatePayload{}, fmt.Errorf("sandbox_id is required")
	}
	return payload, nil
}

// handleCreate processes the create message and creates/attaches to a session
// Returns the session, whether it's a new session (vs. existing), and any error
func (h *Handler) handleCreate(ctx context.Context, payload CreatePayload, conn *websocket.Conn) (*sandbox.Sandbox, bool, error) {
	// Check if session exists
	if sess, ok := h.sandboxManager.Get(payload.SandboxID); ok {
		// Existing session, just attach
		h.logger.Info("Attaching to existing session %s", payload.SandboxID)
		h.sendStatus(conn, StatusPayload{
			State:    "ready",
			Message:  "Attached to existing session",
			Progress: 1.0,
		})
		return sess, false, nil // false = not a new session
	}

	h.logger.Info("Creating new session %s", payload.SandboxID)

	// Parse duration strings
	idleTimeout, _ := time.ParseDuration(payload.Config.IdleTimeout)
	maxLifetime, _ := time.ParseDuration(payload.Config.MaxLifetime)
	if idleTimeout == 0 {
		idleTimeout = 30 * time.Minute
	}
	if maxLifetime == 0 {
		maxLifetime = sandbox.DefaultMaxLifetime
	}

	// Create session
	sess, err := h.sandboxManager.Create(ctx, sandbox.CreateRequest{
		SandboxID:    payload.SandboxID,
		Image:        payload.Image,
		Command:      payload.Command,
		Env:          payload.Env,
		PodNamespace: h.podNamespace,
		Config: sandbox.SecurityConfig{
			AllowNetworkAccess:  payload.Config.AllowNetworkAccess,
			ReadonlyFilesystem:  payload.Config.ReadonlyFilesystem,
			CPULimit:            payload.Config.CPULimit,
			MemoryLimit:         payload.Config.MemoryLimit,
			IdleTimeout:         idleTimeout,
			MaxLifetime:         maxLifetime,
			DropAllCapabilities: payload.Config.DropAllCapabilities,
			AllowPrivileged:     payload.Config.AllowPrivileged,
		},
	})
	if err != nil {
		return nil, true, fmt.Errorf("session manager create failed for %s: %w", payload.SandboxID, err)
	}

	// Send creating status
	h.sendStatus(conn, StatusPayload{
		State:    "creating",
		Message:  "Creating pod...",
		Progress: 0.1,
	})

	// Build pod spec for create
	ttlSeconds := int(maxLifetime.Seconds())
	podSpec := &k8s.PodSpec{
		SessionID:       payload.SandboxID,
		Image:           payload.Image,
		ImagePullPolicy: "IfNotPresent",
		TTLSeconds:      ttlSeconds,
		CPULimit:        payload.Config.CPULimit,
		MemoryLimit:     payload.Config.MemoryLimit,
		ContainerName:   "sandbox",
		Workdir:         "/workspace",
		ShellType:       "bash", // Enable shell-bridge
		AgentThreadID:   payload.SandboxID,
		ResourceLimits: k8s.ResourceLimits{
			CPU:              payload.Config.CPULimit,
			Memory:           payload.Config.MemoryLimit,
			EphemeralStorage: "2Gi",
		},
		SecurityContext: &k8s.PodSecurityConfig{
			NonRoot:             true,
			RunAsUser:           1000,
			DropAllCapabilities: payload.Config.DropAllCapabilities,
			ReadOnlyRoot:        payload.Config.ReadonlyFilesystem,
			Privileged:          payload.Config.AllowPrivileged,
		},
		Env: payload.Env,
	}

	// Create pod
	result, err := h.k8sClient.CreatePod(ctx, podSpec)
	if err != nil {
		return nil, true, fmt.Errorf("k8s pod creation failed for session %s: %w", payload.SandboxID, err)
	}

	sess.PodName = result.PodName
	h.sandboxManager.SetPodInfo(payload.SandboxID, result.PodName)
	h.logger.Info("Created pod %s for session %s", result.PodName, payload.SandboxID)

	// Wait for pod ready
	h.sendStatus(conn, StatusPayload{
		State:    "creating",
		Message:  "Waiting for pod to be ready...",
		Progress: 0.3,
	})

	ready, err := h.k8sClient.WaitForPodReady(ctx, result.PodName, 5*time.Minute, 2*time.Second)
	if err != nil {
		return nil, true, fmt.Errorf("pod readiness check failed for %s: %w", result.PodName, err)
	}
	if !ready {
		return nil, true, fmt.Errorf("pod %s did not become ready within timeout", result.PodName)
	}

	// Get pod IP for shell-bridge connection
	pod, err := h.k8sClient.GetPod(ctx, result.PodName)
	if err != nil {
		return nil, true, fmt.Errorf("failed to get pod info for %s: %w", result.PodName, err)
	}
	if pod.Status.PodIP == "" {
		return nil, true, fmt.Errorf("pod %s has no IP address yet", result.PodName)
	}
	sess.PodIP = pod.Status.PodIP
	h.sandboxManager.SetPodIP(payload.SandboxID, pod.Status.PodIP)
	h.logger.Info("Pod %s has IP %s", result.PodName, pod.Status.PodIP)

	// Check for snapshot
	h.sendStatus(conn, StatusPayload{
		State:    "restoring",
		Message:  "Checking for previous workspace...",
		Progress: 0.5,
	})

	snapshotKey := h.storageClient.GenerateSnapshotKey("ws_default", "proj_default", payload.SandboxID)
	exists, err := h.storageClient.SnapshotExists(ctx, snapshotKey)
	if err != nil {
		h.logger.Warn("Failed to check snapshot existence for %s: %w (continuing without snapshot)", payload.SandboxID, err)
	}

	if exists {
		h.logger.Info("Found snapshot for session %s, restoring...", payload.SandboxID)
		h.sendStatus(conn, StatusPayload{
			State:    "restoring",
			Message:  "Restoring workspace...",
			Progress: 0.6,
		})

		tarData, _, err := h.storageClient.DownloadSnapshot(ctx, snapshotKey)
		if err != nil {
			h.logger.Warn("Failed to download snapshot for %s: %w (continuing without restore)", payload.SandboxID, err)
		} else {
			defer func() {
				if closeErr := tarData.Close(); closeErr != nil {
					h.logger.Warn("Failed to close snapshot data stream for %s: %w", payload.SandboxID, closeErr)
				}
			}()
			if err := h.k8sClient.RestoreWorkspace(ctx, h.podNamespace, result.PodName, tarData); err != nil {
				h.logger.Warn("Failed to restore workspace for %s: %w (continuing anyway)", payload.SandboxID, err)
			} else {
				h.logger.Info("Restored workspace for session %s", payload.SandboxID)
			}
		}
	}

	// Ready
	h.sandboxManager.UpdateState(payload.SandboxID, sandbox.StateReady)
	h.sendStatus(conn, StatusPayload{
		State:    "ready",
		Message:  "Session ready",
		Progress: 1.0,
	})

	h.logger.Info("Session %s is ready", payload.SandboxID)
	return sess, true, nil // true = new session
}

// attachSession attaches to an existing session and starts bidirectional I/O
func (h *Handler) attachSession(ctx context.Context, sandboxID string, conn *websocket.Conn) error {
	sess, ok := h.sandboxManager.Get(sandboxID)
	if !ok {
		return fmt.Errorf("session not found: %s", sandboxID)
	}

	// Mark as connected
	h.sandboxManager.MarkClientConnected(sandboxID)
	defer h.sandboxManager.MarkClientDisconnected(sandboxID)

	h.logger.Info("Client connected to session %s", sandboxID)

	// Send buffered messages
	buf := h.bufferManager.GetOrCreate(sandboxID)
	bufferedMessages := buf.ReadAll()
	if len(bufferedMessages) > 0 {
		h.logger.Info("Sending %d buffered messages for session %s", len(bufferedMessages), sandboxID)
	}
	for _, msg := range bufferedMessages {
		h.sendOutput(conn, msg.Type, msg.Data)
		if msg.Type == "exit" {
			h.sendExit(conn, msg.ExitCode)
			return nil
		}
	}

	// Start bidirectional forwarding
	return h.forwardIO(ctx, sess, conn)
}

// forwardIO handles bidirectional I/O between WebSocket and shell-bridge session
func (h *Handler) forwardIO(ctx context.Context, sess *sandbox.Sandbox, conn *websocket.Conn) error {
	// Validate PodIP is available
	if sess.PodIP == "" {
		return fmt.Errorf("session %s has no PodIP, cannot connect to shell-bridge", sess.SandboxID)
	}

	// Ensure connection via connection manager
	shellBridgeClient, err := h.connectionManager.EnsureConnection(ctx, sess.SandboxID)
	if err != nil {
		h.logger.Error("Failed to ensure connection for session %s: %v", sess.SandboxID, err)
		return fmt.Errorf("failed to ensure shell-bridge connection: %w", err)
	}
	// Note: We don't close here - connection manager manages the lifecycle
	h.logger.Info("Connected to shell-bridge for session %s at %s", sess.SandboxID, sess.PodIP)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Activity ticker for TTL management
	activityTicker := time.NewTicker(30 * time.Second)
	defer activityTicker.Stop()

	// Message channel for stdout/stderr
	outputChan := make(chan outputMessage, 100)
	errorChan := make(chan error, 1)

	// shell-bridge output reader goroutine
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			output, err := shellBridgeClient.ReceiveOutput(ctx)
			if err != nil {
				if err == io.EOF {
					h.logger.Debug("Shell-bridge closed for session %s", sess.SandboxID)
					// Send exit message
					select {
					case outputChan <- outputMessage{msgType: "exit", exitCode: 0}:
					case <-ctx.Done():
					}
					return
				}
				if !isContextCanceled(err) {
					h.logger.Error("ReceiveOutput error for session %s: %v", sess.SandboxID, err)
					select {
					case errorChan <- err:
					case <-ctx.Done():
					}
				}
				return
			}

			// Route output based on type
			var msgType string
			switch shellbridge.BinaryDataType(output.Type) {
			case shellbridge.DataTypeStdout:
				msgType = "stdout"
			case shellbridge.DataTypeStderr:
				msgType = "stderr"
			case shellbridge.DataTypeClose:
				// Shell bridge sent EOF - trigger cascade disconnection
				h.logger.Debug("Shell bridge closed for session %s", sess.SandboxID)
				select {
				case outputChan <- outputMessage{msgType: "exit", exitCode: 0}:
				case <-ctx.Done():
				}
				return
			default:
				h.logger.Debug("Unknown binary data type: %d", output.Type)
				continue
			}

			select {
			case outputChan <- outputMessage{msgType: msgType, data: output.Data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// WebSocket → shell-bridge (stdin)
	go func() {
		defer wg.Done()
		defer cancel()

		// Ping ticker to keep connection alive
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		buf := h.bufferManager.GetOrCreate(sess.SandboxID)

		for {
			select {
			case <-ctx.Done():
				h.logger.Debug("Stdin goroutine exiting: context done")
				return

			case outMsg := <-outputChan:
				// Buffer the message
				buf.Write(&buffer.Message{
					Type:     outMsg.msgType,
					Data:     outMsg.data,
					ExitCode: outMsg.exitCode,
				})

				// Send to client
				if outMsg.msgType == "exit" {
					h.sendExit(conn, outMsg.exitCode)
					return
				}
				h.sendOutput(conn, outMsg.msgType, outMsg.data)

			case err := <-errorChan:
				h.logger.Error("Exec error: %v", err)
				h.sendError(conn, err.Error())
				return

			case <-activityTicker.C:
				// Update activity
				h.sandboxManager.UpdateActivity(sess.SandboxID)

			case <-pingTicker.C:
				// Send ping
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					h.logger.Debug("Failed to send ping to %s: %w", sess.SandboxID, err)
					return
				}

			default:
				// Non-blocking read from WebSocket
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				var msg Message
				if err := conn.ReadJSON(&msg); err != nil {
					if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
						continue
					}
					if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						h.logger.Debug("WebSocket closed normally during stdin read")
					} else if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						h.logger.Warn("WebSocket closed unexpectedly during stdin read: %w", err)
					} else {
						h.logger.Warn("Failed to read stdin message from WebSocket: %w", err)
					}
					return
				}
				conn.SetReadDeadline(time.Time{})

				// Update activity on any message
				h.sandboxManager.UpdateActivity(sess.SandboxID)

				switch msg.Type {
				case TypeStdin:
					payload, err := h.parseStdin(msg.Data)
					if err != nil {
						h.logger.Error("Failed to parse stdin: %v", err)
						continue
					}
					// Validate size before decoding (base64 expands to ~4/3x)
					maxEncodedSize := (h.cfg.WebSocket.MaxMessageSize * 4) / 3
					if int64(len(payload.Data)) > maxEncodedSize {
						h.logger.Error("Stdin data too large: %d bytes (max %d)", len(payload.Data), maxEncodedSize)
						continue
					}
					data, err := base64.StdEncoding.DecodeString(payload.Data)
					if err != nil {
						h.logger.Error("Failed to decode stdin data: %v", err)
						continue
					}
					// Forward stdin to shell-bridge as a command
					// Convert bytes to string for command execution
					command := string(data)
					if err := shellBridgeClient.ExecCommand(ctx, "bash", command, nil); err != nil {
						h.logger.Error("Failed to send stdin to shell-bridge: %v", err)
					}

				case TypeSignal:
					// Handle signal messages
					if err := h.handleSignal(ctx, msg); err != nil {
						h.logger.Error("Failed to handle signal: %v", err)
						h.sendError(conn, fmt.Sprintf("Signal failed: %v", err))
					}

				case TypeCreate:
					// Reconnect attempt - handle accordingly
					h.logger.Debug("Received create message during active session")

				default:
					h.logger.Debug("Received message type: %s", msg.Type)
				}
			}
		}
	}()

	wg.Wait()
	h.logger.Info("Session %s connection closed", sess.SandboxID)
	return nil
}

// sendStatus sends a status message to the client
func (h *Handler) sendStatus(conn *websocket.Conn, payload StatusPayload) error {
	return conn.WriteJSON(Message{
		Type: TypeStatus,
		Data: h.marshalJSON(payload),
	})
}

// sendOutput sends an output message (stdout/stderr) to the client
func (h *Handler) sendOutput(conn *websocket.Conn, msgType string, data []byte) error {
	return conn.WriteJSON(Message{
		Type: msgType,
		Data: h.marshalJSON(OutputPayload{
			Data: base64.StdEncoding.EncodeToString(data),
		}),
	})
}

// sendExit sends an exit message to the client
func (h *Handler) sendExit(conn *websocket.Conn, code int32) error {
	return conn.WriteJSON(Message{
		Type: TypeExit,
		Data: h.marshalJSON(ExitPayload{Code: code}),
	})
}

// sendError sends an error message to the client
func (h *Handler) sendError(conn *websocket.Conn, message string) error {
	return conn.WriteJSON(Message{
		Type: TypeError,
		Data: h.marshalJSON(ErrorPayload{Message: message}),
	})
}

// parseStdin parses the stdin message payload
func (h *Handler) parseStdin(data json.RawMessage) (StdinPayload, error) {
	var payload StdinPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return StdinPayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	return payload, nil
}

// marshalJSON marshals a value to JSON
func (h *Handler) marshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		h.logger.Error("Failed to marshal JSON: %w", err)
		// Return error message in JSON format instead of empty object
		// This ensures the client receives actionable error information
		errorMsg := fmt.Sprintf(`{"error":"Failed to marshal message: %s"}`, err.Error())
		return json.RawMessage(errorMsg)
	}
	return data
}

// validatePodIP checks that the pod has a valid IP address
func (h *Handler) validatePodIP(pod *v1.Pod) error {
	if pod.Status.PodIP == "" {
		return fmt.Errorf("pod IP not ready")
	}

	ip := net.ParseIP(pod.Status.PodIP)
	if ip == nil {
		return fmt.Errorf("invalid pod IP format: %s", pod.Status.PodIP)
	}

	return nil
}

// outputMessage represents an output message from the exec
type outputMessage struct {
	msgType  string
	data     []byte
	exitCode int32
}

// containsSpace checks if a string contains whitespace
func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// isContextCanceled checks if an error is due to context cancellation
func isContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	// Check for context.Canceled
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	// Check if error message contains context cancellation indicators
	errMsg := err.Error()
	return contains(errMsg, "context canceled") ||
		contains(errMsg, "operation was canceled") ||
		contains(errMsg, "deadline exceeded")
}

// contains checks if a string contains a substring (case-insensitive for error matching)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsMiddle(s, substr)))
}

// containsMiddle checks if substr is in the middle of s
func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// parseSignal parses the signal message payload
func (h *Handler) parseSignal(data json.RawMessage) (SignalPayload, error) {
	var payload SignalPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return SignalPayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	if payload.SandboxID == "" {
		return SignalPayload{}, fmt.Errorf("sandbox_id is required")
	}
	if payload.Signal == "" {
		return SignalPayload{}, fmt.Errorf("signal is required")
	}
	return payload, nil
}

// handleSignal processes the signal message and forwards it to the shell bridge
func (h *Handler) handleSignal(ctx context.Context, msg Message) error {
	payload, err := h.parseSignal(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to parse signal payload: %w", err)
	}

	h.logger.Debug("Received signal %s for sandbox %s", payload.Signal, payload.SandboxID)

	// Ensure we have a shell bridge connection
	client, err := h.connectionManager.EnsureConnection(ctx, payload.SandboxID)
	if err != nil {
		return fmt.Errorf("failed to ensure connection for sandbox %s: %w", payload.SandboxID, err)
	}

	// Check if client supports SendSignal (shellbridge.Client does)
	type SignalSender interface {
		SendSignal(ctx context.Context, signal string) error
	}

	signalSender, ok := client.(SignalSender)
	if !ok {
		return fmt.Errorf("client does not support SendSignal")
	}

	// Send the signal to the shell
	if err := signalSender.SendSignal(ctx, payload.Signal); err != nil {
		return fmt.Errorf("failed to send signal %s to sandbox %s: %w", payload.Signal, payload.SandboxID, err)
	}

	h.logger.Info("Sent signal %s to sandbox %s", payload.Signal, payload.SandboxID)
	return nil
}
