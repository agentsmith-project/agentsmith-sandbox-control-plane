package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	t.Run("creates client with defaults", func(t *testing.T) {
		c := NewClient("ws://localhost:8080", "test-key")
		require.NotNil(t, c)
		assert.Equal(t, "ws://localhost:8080", c.baseURL)
		assert.Equal(t, "test-key", c.serviceKey)
		assert.NotNil(t, c.httpClient)
		assert.Equal(t, 30*time.Second, c.httpClient.Timeout)
		assert.NotNil(t, c.ctx)
		assert.NotNil(t, c.cancel)
	})

	t.Run("creates client with wss url", func(t *testing.T) {
		c := NewClient("wss://example.com", "secure-key")
		assert.Equal(t, "wss://example.com", c.baseURL)
	})
}

func TestSandboxClient_Connect(t *testing.T) {
	t.Run("connects successfully to valid websocket server", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Keep connection alive until closed
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()

		err := client.Connect(ctx)
		require.NoError(t, err)
		assert.NotNil(t, client.wsConn)

		defer client.Disconnect()
	})

	t.Run("rejects invalid base URL", func(t *testing.T) {
		client := NewClient("://invalid-url", "test-key")
		ctx := context.Background()

		err := client.Connect(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse base URL")
	})

	t.Run("handles connection rejection", func(t *testing.T) {
		// Server that rejects connections without proper auth
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Service-Key") != "correct-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Accept connection with correct key
			websocket.Accept(w, r, nil)
		}))
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "wrong-key")
		ctx := context.Background()

		err := client.Connect(ctx)
		assert.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "401")
	})
}

func TestSandboxClient_Disconnect(t *testing.T) {
	t.Run("disconnects successfully", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Wait until client closes
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()

		err := client.Connect(ctx)
		require.NoError(t, err)

		err = client.Disconnect()
		assert.NoError(t, err)
		assert.Nil(t, client.wsConn)
	})

	t.Run("disconnect when not connected", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")

		err := client.Disconnect()
		assert.NoError(t, err)
	})

	t.Run("disconnect cancels context", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")
		defer client.Disconnect()

		select {
		case <-client.ctx.Done():
			t.Fatal("Context should not be cancelled before Disconnect")
		default:
		}

		client.Disconnect()

		select {
		case <-client.ctx.Done():
			// Expected
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Context should be cancelled after Disconnect")
		}
	})
}

func TestSandboxClient_CreateSession(t *testing.T) {
	t.Run("sends create session message", func(t *testing.T) {
		receivedMessage := make(chan []byte, 1)
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			typ, data, err := c.Read(context.Background())
			require.NoError(t, err)
			require.Equal(t, websocket.MessageText, typ)
			receivedMessage <- data

			// Send response
			response := map[string]interface{}{
				"type": "status",
				"data": map[string]interface{}{
					"state":   "ready",
					"message": "session-123",
				},
			}
			respData, _ := json.Marshal(response)
			c.Write(context.Background(), websocket.MessageText, respData)
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		req := &CreateSessionRequest{
			AgentThreadID: "at_123",
			Image:         "test-image",
			Command:       []string{"/bin/bash"},
			Config: SecurityConfig{
				IdleTimeout: "300s",
			},
		}

		resp, err := client.CreateSession(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "status", resp.Type)
		assert.Equal(t, "ready", resp.Data.State)

		// Verify sent message
		var sentMsg map[string]interface{}
		err = json.Unmarshal(<-receivedMessage, &sentMsg)
		require.NoError(t, err)
		assert.Equal(t, "create", sentMsg["type"])
	})

	t.Run("returns error when not connected", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")
		ctx := context.Background()

		req := &CreateSessionRequest{
			AgentThreadID: "at_123",
			Image:         "test-image",
		}

		resp, err := client.CreateSession(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "not connected")
	})
}

func TestSandboxClient_Exec(t *testing.T) {
	t.Run("sends exec message with base64 encoded command", func(t *testing.T) {
		receivedMessage := make(chan []byte, 1)
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			typ, data, err := c.Read(context.Background())
			require.NoError(t, err)
			require.Equal(t, websocket.MessageText, typ)
			receivedMessage <- data
			// Wait for close
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		cmd := "echo 'hello world'"
		err = client.Exec(ctx, cmd)
		require.NoError(t, err)

		// Verify sent message
		var sentMsg map[string]interface{}
		err = json.Unmarshal(<-receivedMessage, &sentMsg)
		require.NoError(t, err)
		assert.Equal(t, "stdin", sentMsg["type"])

		data := sentMsg["data"].(map[string]interface{})
		assert.Contains(t, data, "data")
		encodedCmd := data["data"].(string)

		// Verify it's base64 encoded with newline
		decoded, err := decodeBase64(encodedCmd)
		require.NoError(t, err)
		assert.Equal(t, cmd+"\n", decoded)
	})

	t.Run("returns error when not connected", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")
		ctx := context.Background()

		err := client.Exec(ctx, "ls")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})
}

func TestSandboxClient_Close(t *testing.T) {
	t.Run("closes connection", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Wait until client closes
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)

		err = client.Close(ctx)
		assert.NoError(t, err)
		assert.Nil(t, client.wsConn)
	})
}

func TestSandboxClient_sendMessage(t *testing.T) {
	t.Run("sends message when connected", func(t *testing.T) {
		receivedMessage := make(chan []byte, 1)
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			typ, data, err := c.Read(context.Background())
			require.NoError(t, err)
			require.Equal(t, websocket.MessageText, typ)
			receivedMessage <- data
			// Wait for close
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		msg := map[string]interface{}{
			"type": "test",
			"data": "value",
		}

		err = client.sendMessage(ctx, msg)
		require.NoError(t, err)

		// Verify message was sent
		received := <-receivedMessage
		var receivedMsg map[string]interface{}
		err = json.Unmarshal(received, &receivedMsg)
		require.NoError(t, err)
		assert.Equal(t, "test", receivedMsg["type"])
	})

	t.Run("returns error when not connected", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")
		ctx := context.Background()

		msg := map[string]interface{}{"type": "test"}
		err := client.sendMessage(ctx, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("handles json marshal error", func(t *testing.T) {
		// Create a value that can't be marshaled (channel)
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Wait for close
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		msg := map[string]interface{}{
			"type": make(chan int),
		}

		err = client.sendMessage(ctx, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "marshal message")
	})
}

func TestSandboxClient_readMessage(t *testing.T) {
	t.Run("reads text message", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Send a test message
			testMsg := map[string]interface{}{
				"type": "test",
				"data": "value",
			}
			data, _ := json.Marshal(testMsg)
			c.Write(context.Background(), websocket.MessageText, data)
			// Wait for close
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		msg, err := client.readMessage(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, msg)

		var parsed map[string]interface{}
		err = json.Unmarshal(msg, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "test", parsed["type"])
	})

	t.Run("returns error when not connected", func(t *testing.T) {
		client := NewClient("ws://localhost:8080", "test-key")
		ctx := context.Background()

		_, err := client.readMessage(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})

	t.Run("returns error for non-text message", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			// Send binary message instead of text
			c.Write(context.Background(), websocket.MessageBinary, []byte{0x01, 0x02, 0x03})
			// Wait for close
			for {
				_, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		_, err = client.readMessage(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected text message")
	})
}

func TestSandboxClient_ConcurrentAccess(t *testing.T) {
	t.Run("handles concurrent message sending", func(t *testing.T) {
		server := createTestWebSocketServer(t, func(c *websocket.Conn) {
			for i := 0; i < 10; i++ {
				typ, _, err := c.Read(context.Background())
				if err != nil {
					return
				}
				require.Equal(t, websocket.MessageText, typ)
			}
		})
		defer server.Close()

		client := NewClient(strings.Replace(server.URL, "http", "ws", 1), "test-key")
		ctx := context.Background()
		err := client.Connect(ctx)
		require.NoError(t, err)
		defer client.Disconnect()

		// Send multiple messages concurrently
		errChan := make(chan error, 10)
		for i := 0; i < 10; i++ {
			go func(idx int) {
				msg := map[string]interface{}{
					"type": "test",
					"id":   idx,
				}
				errChan <- client.sendMessage(ctx, msg)
			}(i)
		}

		// Check all sends succeeded
		for i := 0; i < 10; i++ {
			assert.NoError(t, <-errChan)
		}
	})
}

// Helper functions

func createTestWebSocketServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for service key
		if r.Header.Get("X-Service-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("failed to accept websocket: %v", err)
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")

		handler(c)
	}))
}

func decodeBase64(s string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}
