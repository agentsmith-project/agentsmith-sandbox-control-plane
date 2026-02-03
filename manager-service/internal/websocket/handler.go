package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/session"
	"github.com/sandbox/manager/internal/storage"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Handler manages WebSocket connections and sandbox sessions
type Handler struct {
	sessionManager *session.Manager
	bufferManager  *buffer.Manager
	k8sClient      *k8s.Client
	storageClient  *storage.Client
	podNamespace   string
	logger         observability.Logger
}

// NewHandler creates a new WebSocket handler
func NewHandler(
	sessionManager *session.Manager,
	bufferManager *buffer.Manager,
	k8sClient *k8s.Client,
	storageClient *storage.Client,
	podNamespace string,
) *Handler {
	return &Handler{
		sessionManager: sessionManager,
		bufferManager:  bufferManager,
		k8sClient:      k8sClient,
		storageClient:  storageClient,
		podNamespace:   podNamespace,
		logger:         observability.GetLogger(),
	}
}

// ServeHTTP handles WebSocket upgrade and connection
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("websocket upgrade failed: %v", err), http.StatusBadRequest)
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
	var agentThreadID string
	var sess *session.Session

	// Set read deadline for initial message
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Wait for create message
	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				h.logger.Debug("WebSocket closed normally: %v", err)
			} else {
				h.logger.Error("Failed to read WebSocket message: %v", err)
			}
			return
		}

		switch msg.Type {
		case TypeCreate:
			payload, err := h.parseCreate(msg.Data)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Invalid create payload: %v", err))
				h.logger.Error("Failed to parse create payload: %v", err)
				return
			}
			agentThreadID = payload.AgentThreadID

			sess, err = h.handleCreate(ctx, payload, conn)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Create failed: %v", err))
				h.logger.Error("Failed to handle create: %v", err)
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
	if err := h.attachSession(ctx, agentThreadID, conn); err != nil {
		h.sendError(conn, fmt.Sprintf("Attach failed: %v", err))
		h.logger.Error("Failed to attach session: %v", err)
	}
}

// parseCreate parses the create message payload
func (h *Handler) parseCreate(data json.RawMessage) (CreatePayload, error) {
	var payload CreatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return CreatePayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	if payload.AgentThreadID == "" {
		return CreatePayload{}, fmt.Errorf("agent_thread_id is required")
	}
	return payload, nil
}

// handleCreate processes the create message and creates/attaches to a session
func (h *Handler) handleCreate(ctx context.Context, payload CreatePayload, conn *websocket.Conn) (*session.Session, error) {
	// Check if session exists
	if sess, ok := h.sessionManager.Get(payload.AgentThreadID); ok {
		// Existing session, just attach
		h.logger.Info("Attaching to existing session %s", payload.AgentThreadID)
		h.sendStatus(conn, StatusPayload{
			State:    "ready",
			Message:  "Attached to existing session",
			Progress: 1.0,
		})
		return sess, nil
	}

	h.logger.Info("Creating new session %s", payload.AgentThreadID)

	// Parse duration strings
	idleTimeout, _ := time.ParseDuration(payload.Config.IdleTimeout)
	maxLifetime, _ := time.ParseDuration(payload.Config.MaxLifetime)
	if idleTimeout == 0 {
		idleTimeout = 30 * time.Minute
	}
	if maxLifetime == 0 {
		maxLifetime = 24 * time.Hour
	}

	// Create session
	sess, err := h.sessionManager.Create(ctx, session.CreateRequest{
		AgentThreadID: payload.AgentThreadID,
		Image:         payload.Image,
		Command:       payload.Command,
		Env:           payload.Env,
		PodNamespace:  h.podNamespace,
		Config: session.SecurityConfig{
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
		return nil, fmt.Errorf("failed to create session: %w", err)
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
		SessionID:       payload.AgentThreadID,
		Image:           payload.Image,
		ImagePullPolicy: "IfNotPresent",
		TTLSeconds:      ttlSeconds,
		CPULimit:        payload.Config.CPULimit,
		MemoryLimit:     payload.Config.MemoryLimit,
		ContainerName:   "sandbox",
		Workdir:         "/workspace",
		AgentThreadID:   payload.AgentThreadID,
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

	// Build command if provided
	if len(payload.Command) > 0 {
		cmdStr := ""
		for i, c := range payload.Command {
			if i > 0 {
				cmdStr += " "
			}
			// Simple quoting - in production use proper shell escaping
			if containsSpace(c) {
				cmdStr += fmt.Sprintf("'%s'", c)
			} else {
				cmdStr += c
			}
		}
		podSpec.Command = cmdStr
	}

	// Create pod
	result, err := h.k8sClient.CreatePod(ctx, podSpec)
	if err != nil {
		h.sessionManager.Delete(payload.AgentThreadID)
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	sess.PodName = result.PodName
	h.sessionManager.SetPodInfo(payload.AgentThreadID, result.PodName)
	h.logger.Info("Created pod %s for session %s", result.PodName, payload.AgentThreadID)

	// Wait for pod ready
	h.sendStatus(conn, StatusPayload{
		State:    "creating",
		Message:  "Waiting for pod to be ready...",
		Progress: 0.3,
	})

	ready, err := h.k8sClient.WaitForPodReady(ctx, result.PodName, 5*time.Minute, 2*time.Second)
	if err != nil || !ready {
		return nil, fmt.Errorf("pod not ready: %w", err)
	}

	// Check for snapshot
	h.sendStatus(conn, StatusPayload{
		State:    "restoring",
		Message:  "Checking for previous workspace...",
		Progress: 0.5,
	})

	snapshotKey := h.storageClient.GenerateSnapshotKey("ws_default", "proj_default", payload.AgentThreadID)
	exists, _ := h.storageClient.SnapshotExists(ctx, snapshotKey)

	if exists {
		h.logger.Info("Found snapshot for session %s, restoring...", payload.AgentThreadID)
		h.sendStatus(conn, StatusPayload{
			State:    "restoring",
			Message:  "Restoring workspace...",
			Progress: 0.6,
		})

		tarData, _, err := h.storageClient.DownloadSnapshot(ctx, snapshotKey)
		if err == nil {
			defer tarData.Close()
			if err := h.k8sClient.RestoreWorkspace(ctx, h.podNamespace, result.PodName, tarData); err != nil {
				h.logger.Warn("Failed to restore workspace: %v", err)
			} else {
				h.logger.Info("Restored workspace for session %s", payload.AgentThreadID)
			}
		}
	}

	// Ready
	h.sessionManager.UpdateState(payload.AgentThreadID, session.StateReady)
	h.sendStatus(conn, StatusPayload{
		State:    "ready",
		Message:  "Session ready",
		Progress: 1.0,
	})

	h.logger.Info("Session %s is ready", payload.AgentThreadID)
	return sess, nil
}

// attachSession attaches to an existing session and starts bidirectional I/O
func (h *Handler) attachSession(ctx context.Context, agentThreadID string, conn *websocket.Conn) error {
	sess, ok := h.sessionManager.Get(agentThreadID)
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	// Mark as connected
	h.sessionManager.MarkClientConnected(agentThreadID)
	defer h.sessionManager.MarkClientDisconnected(agentThreadID)

	h.logger.Info("Client connected to session %s", agentThreadID)

	// Send buffered messages
	buf := h.bufferManager.GetOrCreate(agentThreadID)
	bufferedMessages := buf.ReadAll()
	if len(bufferedMessages) > 0 {
		h.logger.Info("Sending %d buffered messages for session %s", len(bufferedMessages), agentThreadID)
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

// forwardIO handles bidirectional I/O between WebSocket and tmux session
func (h *Handler) forwardIO(ctx context.Context, sess *session.Session, conn *websocket.Conn) error {
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

	// WebSocket → tmux (stdin)
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				h.logger.Debug("Stdin goroutine exiting: context done")
				return
			default:
			}

			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					h.logger.Debug("WebSocket closed normally")
				} else if err != nil {
					h.logger.Error("Failed to read from WebSocket: %v", err)
				}
				return
			}

			// Update activity on any message
			h.sessionManager.UpdateActivity(sess.AgentThreadID)

			switch msg.Type {
			case TypeStdin:
				payload, err := h.parseStdin(msg.Data)
				if err != nil {
					h.logger.Error("Failed to parse stdin: %v", err)
					continue
				}
				data, err := base64.StdEncoding.DecodeString(payload.Data)
				if err != nil {
					h.logger.Error("Failed to decode stdin data: %v", err)
					continue
				}
				// Write stdin to the tmux session
				// For now, we'll buffer this - in a full implementation,
				// we'd have a persistent stdin stream
				h.logger.Debug("Received stdin data: %d bytes", len(data))

			case TypeCreate:
				// Reconnect attempt - handle accordingly
				h.logger.Debug("Received create message during active session")

			default:
				h.logger.Debug("Received message type: %s", msg.Type)
			}
		}
	}()

	// tmux → WebSocket (stdout/stderr)
	go func() {
		defer wg.Done()
		defer cancel()

		// Ping ticker to keep connection alive
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		buf := h.bufferManager.GetOrCreate(sess.AgentThreadID)

		for {
			select {
			case <-ctx.Done():
				h.logger.Debug("Output goroutine exiting: context done")
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
				h.sessionManager.UpdateActivity(sess.AgentThreadID)

			case <-pingTicker.C:
				// Send ping
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					h.logger.Error("Failed to send ping: %v", err)
					return
				}
			}
		}
	}()

	// Start exec for stdout/stderr streaming
	// In a full implementation, this would be a separate streaming exec
	// For now, we'll keep the session alive and handle I/O

	wg.Wait()
	h.logger.Info("Session %s connection closed", sess.AgentThreadID)
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
	data, _ := json.Marshal(v)
	return data
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
