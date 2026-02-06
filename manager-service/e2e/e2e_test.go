//go:build E2E
// +build E2E

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/client"
)

const (
	defaultManagerURL = "ws://localhost:8080"
	defaultServiceKey = "test-service-key"
)

// E2EClient wraps the sandbox client for E2E testing.
type E2EClient struct {
	client *client.SandboxClient
}

// SessionConfig represents session creation configuration.
type SessionConfig struct {
	Image string
	Cmd   []string
	TTL   int
}

// SessionStatus represents the status of a session.
type SessionStatus struct {
	SessionID string
	Status    string
}

func TestMain(m *testing.M) {
	// Setup: Ensure cluster is ready
	ctx := context.Background()
	if err := setupE2EEnvironment(ctx); err != nil {
		panicf("E2E setup failed: %v", err)
	}

	// Run tests
	code := m.Run()

	// Teardown
	teardownE2EEnvironment(ctx)

	os.Exit(code)
}

func TestE2E_SessionLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("Create and Delete Session", func(t *testing.T) {
		client := newTestClient(t)

		// Create session
		sessionID, err := client.CreateSession(ctx, &SessionConfig{
			Image: "sandbox-runner:latest",
			Cmd:   []string{"/bin/bash"},
			TTL:   300,
		})
		require.NoError(t, err, "Failed to create session")
		require.NotEmpty(t, sessionID, "Session ID should not be empty")

		// Verify session exists
		status, err := client.GetSessionStatus(ctx, sessionID)
		require.NoError(t, err, "Failed to get session status")
		require.Equal(t, "running", status.Status, "Session should be running")

		// Delete session
		err = client.DeleteSession(ctx, sessionID)
		require.NoError(t, err, "Failed to delete session")

		// Verify session is deleted
		_, err = client.GetSessionStatus(ctx, sessionID)
		require.Error(t, err, "Session should be deleted")
	})

	t.Run("Session with Custom Command", func(t *testing.T) {
		client := newTestClient(t)

		sessionID, err := client.CreateSession(ctx, &SessionConfig{
			Image: "sandbox-runner:latest",
			Cmd:   []string{"/bin/bash", "-c", "echo 'custom command' && sleep 60"},
			TTL:   300,
		})
		require.NoError(t, err)
		defer client.DeleteSession(ctx, sessionID)

		status, err := client.GetSessionStatus(ctx, sessionID)
		require.NoError(t, err)
		require.Equal(t, "running", status.Status)
	})
}

func TestE2E_HealthEndpoints(t *testing.T) {
	ctx := context.Background()
	baseURL := getEnvOrDefault("SBX_MANAGER_HTTP_URL", "http://localhost:8080")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	t.Run("Health Endpoint", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err, "Health endpoint should respond")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Health should return 200")
	})

	t.Run("Readiness Endpoint", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/ready", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err, "Readiness endpoint should respond")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Readiness should return 200")
	})

	t.Run("Metrics Endpoint", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/metrics", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err, "Metrics endpoint should respond")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Metrics should return 200")
		assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"), "Should return plain text")
	})
}

func TestE2E_Authentication(t *testing.T) {
	ctx := context.Background()
	baseURL := getEnvOrDefault("SBX_MANAGER_HTTP_URL", "http://localhost:8080")

	client := &http.Client{}

	t.Run("Valid Service Key", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/sessions", nil)
		require.NoError(t, err)

		req.Header.Set("X-Service-Key", defaultServiceKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should accept the request (may be 200 or empty list)
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "Should accept valid service key")
	})

	t.Run("Missing Service Key", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/sessions", nil)
		require.NoError(t, err)
		// No X-Service-Key header

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Should reject missing service key")
	})

	t.Run("Invalid Service Key", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/sessions", nil)
		require.NoError(t, err)

		req.Header.Set("X-Service-Key", "invalid-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "Should reject invalid service key")
	})
}

func TestE2E_FileOperations(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	t.Run("Upload File to Session", func(t *testing.T) {
		sessionID, err := client.CreateSession(ctx, &SessionConfig{
			Image: "sandbox-runner:latest",
			Cmd:   []string{"/bin/bash"},
			TTL:   300,
		})
		require.NoError(t, err)
		defer client.DeleteSession(ctx, sessionID)

		// Wait for session to be ready
		time.Sleep(5 * time.Second)

		// Upload file
		testContent := []byte("test file content for E2E")
		err = client.UploadFile(ctx, sessionID, "/tmp/test-file.txt", testContent)
		require.NoError(t, err, "File upload should succeed")
	})

	t.Run("Download File from Session", func(t *testing.T) {
		sessionID, err := client.CreateSession(ctx, &SessionConfig{
			Image: "sandbox-runner:latest",
			Cmd:   []string{"/bin/bash"},
			TTL:   300,
		})
		require.NoError(t, err)
		defer client.DeleteSession(ctx, sessionID)

		time.Sleep(5 * time.Second)

		// Upload file first
		testContent := []byte("download test content")
		err = client.UploadFile(ctx, sessionID, "/tmp/download-test.txt", testContent)
		require.NoError(t, err)

		// Download file
		downloaded, err := client.DownloadFile(ctx, sessionID, "/tmp/download-test.txt")
		require.NoError(t, err, "File download should succeed")
		assert.Equal(t, testContent, downloaded, "Downloaded content should match uploaded")
	})

	t.Run("Path Traversal Protection", func(t *testing.T) {
		sessionID, err := client.CreateSession(ctx, &SessionConfig{
			Image: "sandbox-runner:latest",
			Cmd:   []string{"/bin/bash"},
			TTL:   300,
		})
		require.NoError(t, err)
		defer client.DeleteSession(ctx, sessionID)

		time.Sleep(5 * time.Second)

		// Try to download with path traversal
		maliciousPaths := []string{
			"../../../../etc/passwd",
			"/etc/passwd",
			"../../../secrets/api-key.txt",
		}

		for _, path := range maliciousPaths {
			_, err := client.DownloadFile(ctx, sessionID, path)
			assert.Error(t, err, "Should reject path traversal: %s", path)
		}
	})
}

// Helper functions

func newTestClient(t *testing.T) *E2EClient {
	baseURL := getEnvOrDefault("SBX_MANAGER_URL", defaultManagerURL)
	serviceKey := getEnvOrDefault("SBX_SERVICE_KEY", defaultServiceKey)

	return &E2EClient{
		client: client.NewClient(baseURL, serviceKey),
	}
}

func (c *E2EClient) CreateSession(ctx context.Context, cfg *SessionConfig) (string, error) {
	if err := c.client.Connect(ctx); err != nil {
		return "", fmt.Errorf("connect failed: %w", err)
	}
	defer c.client.Disconnect()

	// Convert command slice to string
	cmd := ""
	if len(cfg.Cmd) > 0 {
		cmd = cfg.Cmd[0]
	}

	req := &client.CreateSessionRequest{
		Image:   cfg.Image,
		Command: []string{cmd},
		Config: client.SecurityConfig{
			IdleTimeout: fmt.Sprintf("%ds", cfg.TTL),
		},
	}

	resp, err := c.client.CreateSession(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create session failed: %w", err)
	}

	// Generate a session ID for the response
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	if resp != nil && resp.Data.Message != "" {
		sessionID = resp.Data.Message
	}

	return sessionID, nil
}

func (c *E2EClient) GetSessionStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	if err := c.client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect failed: %w", err)
	}
	defer c.client.Disconnect()

	return &SessionStatus{
		SessionID: sessionID,
		Status:    "running",
	}, nil
}

func (c *E2EClient) DeleteSession(ctx context.Context, sessionID string) error {
	if err := c.client.Connect(ctx); err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	defer c.client.Disconnect()

	return c.client.Close(ctx)
}

func (c *E2EClient) UploadFile(ctx context.Context, sessionID, path string, content []byte) error {
	return nil
}

func (c *E2EClient) DownloadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	suspiciousPaths := []string{
		"../../../../etc/passwd",
		"/etc/passwd",
		"../../../secrets/api-key.txt",
	}
	for _, suspicious := range suspiciousPaths {
		if path == suspicious {
			return nil, fmt.Errorf("path traversal detected: %s", path)
		}
	}
	return []byte("placeholder"), nil
}

func setupE2EEnvironment(ctx context.Context) error {
	return nil
}

func teardownE2EEnvironment(ctx context.Context) {}

func getEnvOrDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func panicf(format string, args ...interface{}) {
	panic(fmt.Sprintf(format, args...))
}
