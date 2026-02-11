package shellbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	// ErrNotConnected is returned when trying to use the client without an active connection
	ErrNotConnected = errors.New("not connected to shell-bridge")
)

const (
	HandshakeTimeout = 10 * time.Second
	WriteTimeout     = 5 * time.Second
	ReadTimeout      = 30 * time.Second
	DefaultPort      = 8080
)

// FrameHandler handles shell output frames from shell-bridge
type FrameHandler interface {
	OnStdout(data []byte)
	OnStderr(data []byte)
	OnResize(data []byte)
	OnClose() // Called when shell bridge sends EOF (0x04 frame)
}

// Client is a WebSocket client for shell-bridge
type Client struct {
	conn       *websocket.Conn
	connMu     sync.RWMutex
	connected  bool
	closed     atomic.Bool
	url        string
	httpClient *http.Client
	onClose    func()
	onCloseMu  sync.RWMutex
}

// NewClient creates a new shell-bridge client for a pod
func NewClient(podIP string, port int) *Client {
	if port == 0 {
		port = DefaultPort
	}
	return &Client{
		url:        fmt.Sprintf("ws://%s:%d/ws", podIP, port),
		httpClient: &http.Client{Timeout: HandshakeTimeout},
	}
}

// Connect establishes a WebSocket connection to shell-bridge
func (c *Client) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: HandshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to shell-bridge: %w", err)
	}
	c.connMu.Lock()
	c.conn = conn
	c.connected = true
	c.connMu.Unlock()
	return nil
}

// ExecMessage is the JSON message for executing commands (matching shell-bridge protocol)
type ExecMessage struct {
	Type    string   `json:"type"`
	Shell   string   `json:"shell"`
	Command string   `json:"command"`
	Env     []string `json:"env,omitempty"`
}

// ExecCommand sends a command to the shell
func (c *Client) ExecCommand(ctx context.Context, shell, command string, env []string) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	msg := ExecMessage{Type: "exec", Shell: shell, Command: command, Env: env}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// Output represents shell output with metadata
type Output struct {
	Type byte
	Data []byte
}

// ReceiveOutput waits for output from the shell
// Returns io.EOF when the shell closes
func (c *Client) ReceiveOutput(ctx context.Context) (*Output, error) {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return nil, ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	for {
		// Set a read deadline to prevent busy-wait loop
		err := conn.SetReadDeadline(time.Now().Add(ReadTimeout))
		if err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			// Check for timeout (net.Error with Timeout())
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				// On timeout, check if context is cancelled
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					// Continue waiting for data
					continue
				}
			}
			if err == io.EOF || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read error: %w", err)
		}

		if msgType == websocket.BinaryMessage {
			frame, err := ParseBinaryFrame(data)
			if err != nil {
				// Return error immediately instead of silently continuing
				return nil, fmt.Errorf("failed to parse binary frame: %w", err)
			}
			return &Output{Type: byte(frame.Type), Data: frame.Data}, nil
		}
		// Ignore text messages (control messages like exit, ping)
	}
}

// Close closes the WebSocket connection gracefully
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	var err error
	if c.conn != nil {
		// Send close frame
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err = c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.closed.Store(true)

	// Call onClose callback if set (always call, even if conn was nil)
	c.onCloseMu.RLock()
	if c.onClose != nil {
		c.onClose()
	}
	c.onCloseMu.RUnlock()

	return err
}

// OnClose registers a callback function to be called when the connection is closed
func (c *Client) OnClose(fn func()) {
	c.onCloseMu.Lock()
	defer c.onCloseMu.Unlock()
	c.onClose = fn
}

// IsActive returns true if the connection is active (connected and not closed)
func (c *Client) IsActive() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.connected && !c.closed.Load()
}

// SendSignal sends a signal to the shell process
// Signal should be a string like "SIGTERM", "SIGKILL", "SIGINT", etc.
func (c *Client) SendSignal(ctx context.Context, signal string) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	signalMsg := struct {
		Type   string `json:"type"`
		Signal string `json:"signal"`
	}{
		Type:   "signal",
		Signal: signal,
	}

	data, err := json.Marshal(signalMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal signal message: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// ReceiveLoop enters a loop receiving output from the shell and calling the handler
// Returns when the shell closes or context is cancelled
func (c *Client) ReceiveLoop(ctx context.Context, handler FrameHandler) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Set a read deadline to allow context cancellation
		err := conn.SetReadDeadline(time.Now().Add(ReadTimeout))
		if err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			// Check for timeout (net.Error with Timeout())
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				// On timeout, continue waiting (check context at loop start)
				continue
			}
			if err == io.EOF || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				handler.OnClose()
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		if msgType == websocket.BinaryMessage {
			frame, err := ParseBinaryFrame(data)
			if err != nil {
				return fmt.Errorf("failed to parse binary frame: %w", err)
			}

			switch frame.Type {
			case DataTypeStdout:
				handler.OnStdout(frame.Data)
			case DataTypeStderr:
				handler.OnStderr(frame.Data)
			case DataTypeResize:
				handler.OnResize(frame.Data)
			case DataTypeClose:
				handler.OnClose()
				return nil
			}
		}
		// Ignore text messages (control messages like exit, ping)
	}
}
