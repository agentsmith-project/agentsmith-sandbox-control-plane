package shellbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	HandshakeTimeout = 10 * time.Second
	WriteTimeout     = 5 * time.Second
	DefaultPort      = 8080
)

// Client is a WebSocket client for shell-bridge
type Client struct {
	conn       *websocket.Conn
	url        string
	httpClient *http.Client
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
	c.conn = conn
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
	msg := ExecMessage{Type: "exec", Shell: shell, Command: command, Env: env}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Output represents shell output with metadata
type Output struct {
	Type byte
	Data []byte
}

// ReceiveOutput waits for output from the shell
// Returns io.EOF when the shell closes
func (c *Client) ReceiveOutput(ctx context.Context) (*Output, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			if err == io.EOF || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read error: %w", err)
		}

		if msgType == websocket.BinaryMessage {
			frame, err := ParseBinaryFrame(data)
			if err != nil {
				continue // Skip malformed frames
			}
			return &Output{Type: byte(frame.Type), Data: frame.Data}, nil
		}
		// Ignore text messages (control messages like exit, ping)
	}
}

// Close closes the WebSocket connection gracefully
func (c *Client) Close() error {
	if c.conn != nil {
		// Send close frame
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		return c.conn.Close()
	}
	return nil
}
