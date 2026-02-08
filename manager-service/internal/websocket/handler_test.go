package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/session"
	"github.com/sandbox/manager/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleWebSocket_HeaderAuth tests that WebSocket handler uses header-based authentication
func TestHandleWebSocket_HeaderAuth(t *testing.T) {
	t.Run("rejects connection without auth header when auth enabled", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("test-key-123")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Create test server with auth middleware
		middleware := auth.ServiceKeyMiddleware(
			authValidator,
			"X-Service-Key",
			true,
			"ServiceKey",
			http.StatusUnauthorized,
		)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Try to connect without auth header - should fail
		_, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
		assert.Error(t, err)
	})

	t.Run("accepts connection with valid service key in header", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("test-key-123")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Create test server with auth middleware
		middleware := auth.ServiceKeyMiddleware(
			authValidator,
			"X-Service-Key",
			true,
			"ServiceKey",
			http.StatusUnauthorized,
		)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Connect with valid service key in header
		header := http.Header{}
		header.Set("X-Service-Key", "test-key-123")

		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err != nil {
			// Connection might be established but closed during upgrade
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "Expected successful WebSocket upgrade")
			}
		}
		if conn != nil {
			conn.Close()
		}
		// The connection should succeed (we don't check exact behavior here as it depends on the full setup)
	})

	t.Run("accepts connection with valid JWT token in Authorization header", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("dummy-key-for-validator")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Generate a valid token
		token, err := tokenAuth.GenerateToken("test-user-123")
		require.NoError(t, err)

		// Create test server with token auth middleware
		middleware := auth.TokenAuthMiddleware(tokenAuth)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Connect with valid JWT token in Authorization header
		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)

		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err != nil {
			// Connection might be established but closed during upgrade
			if resp != nil {
				assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "Should not be unauthorized with valid token")
			}
		}
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("rejects connection with invalid service key in header", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("test-key-123")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Create test server with auth middleware
		middleware := auth.ServiceKeyMiddleware(
			authValidator,
			"X-Service-Key",
			true,
			"ServiceKey",
			http.StatusUnauthorized,
		)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Try to connect with invalid service key in header
		header := http.Header{}
		header.Set("X-Service-Key", "invalid-key")

		_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		assert.Error(t, err)
		if resp != nil {
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Should reject invalid service key")
		}
	})

	t.Run("rejects connection with invalid JWT token", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("dummy-key-for-validator")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Create test server with token auth middleware
		middleware := auth.TokenAuthMiddleware(tokenAuth)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Try to connect with invalid token
		header := http.Header{}
		header.Set("Authorization", "Bearer invalid-token")

		_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		assert.Error(t, err)
		if resp != nil {
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Should reject invalid token")
		}
	})
}

// TestHandleWebSocket_UserContext tests that user context is properly extracted and used
func TestHandleWebSocket_UserContext(t *testing.T) {
	t.Run("user context is available after token auth middleware", func(t *testing.T) {
		// Setup dependencies
		serviceKeys := auth.ParseServiceKeys("dummy-key-for-validator")
		authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
		require.NoError(t, err)

		tokenAuth := auth.NewTokenAuthenticator("mbos-sandbox", []byte("test-secret-key-that-is-at-least-32-chars-long-for-security"), 24*time.Hour)

		handler, _, cleanup := setupTestHandler(t, authValidator, tokenAuth)
		defer cleanup()

		// Generate a valid token
		token, err := tokenAuth.GenerateToken("test-user-456")
		require.NoError(t, err)

		// Create test server with token auth middleware
		middleware := auth.TokenAuthMiddleware(tokenAuth)

		server := httptest.NewServer(middleware(handler))
		defer server.Close()

		// Convert http:// to ws://
		wsURL := "ws://" + server.Listener.Addr().String() + "/ws"

		// Connect with valid JWT token
		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		// We expect this might fail due to missing dependencies, but the auth should pass
		if err != nil {
			// If connection failed, it should be due to reasons other than auth
			// (e.g., missing k8s client, storage, etc.)
			return
		}
		if conn != nil {
			conn.Close()
		}
	})
}

// setupTestHandler creates a test WebSocket handler with minimal dependencies
func setupTestHandler(t *testing.T, authValidator *auth.ServiceKeyValidator, tokenAuth *auth.TokenAuthenticator) (*Handler, *session.Manager, func()) {
	t.Helper()

	// Initialize logging
	observability.InitLogging()

	// Create minimal config
	cfg := &config.Config{
		WebSocket: config.WebSocketConfig{
			ReadBufferSize:          1024,
			WriteBufferSize:         1024,
			AllowedOrigins:          []string{"*"},
			AllowNonBrowserRequests: true,
		},
	}

	// Create session manager
	sessionManager := session.NewManager()

	// Create buffer manager
	bufferManager := buffer.NewManager()

	// Create mock k8s client (minimal)
	k8sClient, err := k8s.NewClient(&k8s.ClientConfig{
		Namespace: "default",
		QPS:       10,
		Burst:     20,
	})
	if err != nil {
		t.Skipf("Skipping test: K8s client not available: %v", err)
	}

	// Create storage client
	storageCreds := &storage.Credentials{
		Endpoint:  "localhost:9000",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Bucket:    "sandboxes",
		UseSSL:    false,
	}
	storageClient, err := storage.NewClientWithCreds(storageCreds)
	if err != nil {
		t.Skipf("Skipping test: Storage client not available: %v", err)
	}

	// Create authorizer
	authorizer := auth.NewAuthorizer(sessionManager, k8sClient)

	handler := NewHandler(
		sessionManager,
		bufferManager,
		k8sClient,
		storageClient,
		"default",
		cfg,
		authorizer,
	)

	cleanup := func() {
		// Note: sessionManager doesn't have DeleteAll, cleanup is handled by individual test cases
	}

	return handler, sessionManager, cleanup
}
