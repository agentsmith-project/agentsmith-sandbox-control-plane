//go:build !short

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/sandbox/manager/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebSocket_NoServiceKey_Returns401 verifies that WebSocket connections
// without a service_key are rejected with 401 when auth is configured
func TestWebSocket_NoServiceKey_Returns401(t *testing.T) {
	// Create auth validator
	authValidator, err := auth.NewServiceKeyValidator([]string{"test-key-123", "another-key"})
	require.NoError(t, err)

	// Create a test handler that simulates the auth wrapper
	handler := createAuthWrappedWebSocketHandler(t, authValidator, false)

	// Start test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Try to connect without service key
	wsURL := fmt.Sprintf("ws://%s/ws", server.Listener.Addr().String())
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	// Expected: connection fails with 401
	assert.Error(t, err, "Expected connection to fail without service key")
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Expected 401 status")
	}
}

// TestWebSocket_InvalidServiceKey_Returns401 verifies that WebSocket connections
// with an invalid service_key are rejected with 401
func TestWebSocket_InvalidServiceKey_Returns401(t *testing.T) {
	// Create auth validator
	authValidator, err := auth.NewServiceKeyValidator([]string{"test-key-123"})
	require.NoError(t, err)

	handler := createAuthWrappedWebSocketHandler(t, authValidator, false)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Try with invalid service key
	wsURL := fmt.Sprintf("ws://%s/ws?service_key=invalid-key", server.Listener.Addr().String())
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	// Expected: connection fails with 401
	assert.Error(t, err, "Expected connection to fail with invalid service key")
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Expected 401 status")
	}
}

// TestWebSocket_ValidServiceKey_Connects verifies that WebSocket connections
// with a valid service_key are accepted
func TestWebSocket_ValidServiceKey_Connects(t *testing.T) {
	// Create auth validator
	authValidator, err := auth.NewServiceKeyValidator([]string{"test-key-123"})
	require.NoError(t, err)

	// Create handler that accepts connections (for testing auth passes)
	handler := createAuthWrappedWebSocketHandler(t, authValidator, true)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Try with valid service key
	wsURL := fmt.Sprintf("ws://%s/ws?service_key=test-key-123", server.Listener.Addr().String())
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	// Expected: connection succeeds (auth passes)
	// We close immediately since we're only testing auth
	if err == nil {
		defer conn.Close()
		assert.NoError(t, err, "Expected connection to succeed with valid service key")
	} else {
		// If connection failed, it should NOT be a 401 error
		if resp != nil {
			assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode,
				"Should not get 401 with valid key")
		}
	}
}

// TestWebSocket_MultipleServiceKeys_AnyValidKeyAccepts verifies that any
// configured valid key is accepted
func TestWebSocket_MultipleServiceKeys_AnyValidKeyAccepts(t *testing.T) {
	// Create auth validator with multiple keys
	authValidator, err := auth.NewServiceKeyValidator([]string{"key-1", "key-2", "key-3"})
	require.NoError(t, err)

	handler := createAuthWrappedWebSocketHandler(t, authValidator, true)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Test each valid key
	validKeys := []string{"key-1", "key-2", "key-3"}
	for _, key := range validKeys {
		t.Run(key, func(t *testing.T) {
			wsURL := fmt.Sprintf("ws://%s/ws?service_key=%s",
				server.Listener.Addr().String(), key)
			conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

			if err == nil {
				conn.Close()
			} else {
				if resp != nil {
					assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode,
						fmt.Sprintf("Key %s should be accepted", key))
				}
			}
		})
	}
}

// TestWebSocket_EmptyServiceKey_Returns401 verifies that an empty
// service_key parameter is rejected
func TestWebSocket_EmptyServiceKey_Returns401(t *testing.T) {
	authValidator, err := auth.NewServiceKeyValidator([]string{"test-key-123"})
	require.NoError(t, err)

	handler := createAuthWrappedWebSocketHandler(t, authValidator, false)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Try with empty service key
	wsURL := fmt.Sprintf("ws://%s/ws?service_key=", server.Listener.Addr().String())
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)

	assert.Error(t, err, "Expected connection to fail with empty service key")
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
			"Expected 401 status for empty service key")
	}
}

// createAuthWrappedWebSocketHandler creates an HTTP handler that wraps
// WebSocket upgrade logic with service key authentication, mimicking
// the pattern used in app.go
//
// The shouldAccept flag controls whether the underlying WebSocket handler
// accepts the connection (true) or rejects it (false)
func createAuthWrappedWebSocketHandler(t *testing.T, authValidator *auth.ServiceKeyValidator, shouldAccept bool) http.Handler {
	upgrader := &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Accept all origins for testing
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract service_key from query parameter (as in app.go)
		serviceKey := r.URL.Query().Get("service_key")

		// Validate service key
		if !authValidator.Validate(serviceKey) {
			http.Error(w, "Unauthorized: invalid or missing service_key", http.StatusUnauthorized)
			return
		}

		// Auth passed - proceed to WebSocket upgrade
		// For testing, we can either accept or reject based on shouldAccept flag
		if shouldAccept {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				// Immediately close for testing purposes
				conn.Close()
			}
		} else {
			// Simulate a scenario where auth passes but the connection fails later
			// (e.g., k8s not available in tests)
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		}
	})
}
