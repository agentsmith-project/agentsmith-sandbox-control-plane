package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// SandboxClient is a WebSocket client for sandbox operations.
type SandboxClient struct {
	baseURL    string
	serviceKey string
	httpClient *http.Client
	wsConn     *websocket.Conn
	wsMu       sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewClient creates a new sandbox client.
func NewClient(baseURL, serviceKey string) *SandboxClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &SandboxClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Connect establishes a WebSocket connection to the manager.
func (c *SandboxClient) Connect(ctx context.Context) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}

	wsURL := url.URL{
		Scheme: func() string {
			if u.Scheme == "https" {
				return "wss"
			}
			return "ws"
		}(),
		Host: u.Host,
		Path: "/ws",
	}

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"X-Service-Key": []string{c.serviceKey},
		},
	}

	c.wsMu.Lock()
	defer c.wsMu.Unlock()

	conn, resp, err := websocket.Dial(ctx, wsURL.String(), opts)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket dial failed (status %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	c.wsConn = conn
	return nil
}

// Disconnect closes the WebSocket connection.
func (c *SandboxClient) Disconnect() error {
	c.cancel()

	c.wsMu.Lock()
	defer c.wsMu.Unlock()

	if c.wsConn != nil {
		err := c.wsConn.Close(websocket.StatusNormalClosure, "")
		c.wsConn = nil
		return err
	}
	return nil
}

// CreateSession creates a new sandbox session.
func (c *SandboxClient) CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error) {
	msg := map[string]interface{}{
		"type": "create",
		"data": req,
	}

	if err := c.sendMessage(ctx, msg); err != nil {
		return nil, err
	}

	resp, err := c.readMessage(ctx)
	if err != nil {
		return nil, err
	}

	var result CreateSessionResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

// Exec executes a command in the attached session.
func (c *SandboxClient) Exec(ctx context.Context, cmd string) error {
	// Base64 encode the command as per server protocol
	encodedData := base64.StdEncoding.EncodeToString([]byte(cmd + "\n"))
	msg := map[string]interface{}{
		"type": "stdin",
		"data": map[string]string{
			"data": encodedData,
		},
	}

	return c.sendMessage(ctx, msg)
}

// Close closes the current session.
// Note: The server doesn't have a separate "close" message type.
// The session is kept alive for reconnection; calling Disconnect() closes the connection.
func (c *SandboxClient) Close(ctx context.Context) error {
	// Just close the connection - the server will keep the session alive
	return c.Disconnect()
}

// sendMessage sends a message via WebSocket.
func (c *SandboxClient) sendMessage(ctx context.Context, msg interface{}) error {
	c.wsMu.RLock()
	defer c.wsMu.RUnlock()

	if c.wsConn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	return c.wsConn.Write(ctx, websocket.MessageText, data)
}

// readMessage reads a full message from WebSocket including type and data.
func (c *SandboxClient) readMessage(ctx context.Context) (json.RawMessage, error) {
	c.wsMu.RLock()
	defer c.wsMu.RUnlock()

	if c.wsConn == nil {
		return nil, fmt.Errorf("not connected")
	}

	typ, data, err := c.wsConn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	if typ != websocket.MessageText {
		return nil, fmt.Errorf("expected text message, got: %v", typ)
	}

	// Return the full message (including type field) as-is
	return data, nil
}
