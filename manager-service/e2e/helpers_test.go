//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test client
// ---------------------------------------------------------------------------

// testClient wraps an *http.Client and the base URL of the manager service.
type testClient struct {
	http    *http.Client
	baseURL string
}

// newTestClient reads SBX_MANAGER_HTTP_URL (default "http://localhost:8080")
// and returns a ready-to-use testClient.
func newTestClient(t *testing.T) *testClient {
	t.Helper()

	base := os.Getenv("SBX_MANAGER_HTTP_URL")
	if base == "" {
		base = "http://localhost:8080"
	}
	base = strings.TrimRight(base, "/")

	return &testClient{
		http:    &http.Client{},
		baseURL: base,
	}
}

// ---------------------------------------------------------------------------
// Request / response helpers
// ---------------------------------------------------------------------------

// createSandboxRequest is the JSON body for PUT /v1/sandboxes/{id}.
type createSandboxRequest struct {
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
	Image      string `json:"image,omitempty"`
}

// createSandboxResponse is the JSON body returned by PUT /v1/sandboxes/{id}.
type createSandboxResponse struct {
	PodName   string `json:"podName"`
	ExpiresAt string `json:"expiresAt"`
}

// execRequest is the JSON body for POST /v1/sandboxes/{id}/exec.
type execRequest struct {
	Cmd            []string          `json:"cmd"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
}

// createSandbox sends PUT /v1/sandboxes/{sessionID}.
func (c *testClient) createSandbox(ctx context.Context, t *testing.T, sessionID string, body createSandboxRequest) *http.Response {
	t.Helper()

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/v1/sandboxes/%s", c.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// execCommand sends POST /v1/sandboxes/{sessionID}/exec and returns the raw
// response so the caller can parse the SSE stream.
func (c *testClient) execCommand(ctx context.Context, t *testing.T, sessionID string, body execRequest) *http.Response {
	t.Helper()

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/v1/sandboxes/%s/exec", c.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// touchSandbox sends POST /v1/sandboxes/{sessionID}/touch.
func (c *testClient) touchSandbox(ctx context.Context, t *testing.T, sessionID string) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/v1/sandboxes/%s/touch", c.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// deleteSandbox sends DELETE /v1/sandboxes/{sessionID}.
func (c *testClient) deleteSandbox(ctx context.Context, t *testing.T, sessionID string) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/v1/sandboxes/%s", c.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	require.NoError(t, err)

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// uploadFile sends POST /v1/sandboxes/{sessionID}/files/upload?dest={dest}
// with the provided data as the request body.
func (c *testClient) uploadFile(ctx context.Context, t *testing.T, sessionID, dest string, data io.Reader) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/v1/sandboxes/%s/files/upload?dest=%s", c.baseURL, sessionID, dest)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, data)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// downloadFile sends GET /v1/sandboxes/{sessionID}/files/download?src={src}.
func (c *testClient) downloadFile(ctx context.Context, t *testing.T, sessionID, src string) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/v1/sandboxes/%s/files/download?src=%s", c.baseURL, sessionID, src)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	return resp
}

// ---------------------------------------------------------------------------
// SSE parsing
// ---------------------------------------------------------------------------

// SSEEvent represents a single server-sent event.
type SSEEvent struct {
	Event string
	Data  string
}

// parseSSEEvents reads a text/event-stream body and returns structured events.
// It handles multi-line data fields by concatenating them with newlines.
func parseSSEEvents(reader io.Reader) ([]SSEEvent, error) {
	var events []SSEEvent
	scanner := bufio.NewScanner(reader)

	var currentEvent string
	var currentData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if currentData.Len() > 0 {
				currentData.WriteString("\n")
			}
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			// Empty line = end of event block
			if currentEvent != "" || currentData.Len() > 0 {
				events = append(events, SSEEvent{
					Event: currentEvent,
					Data:  currentData.String(),
				})
				currentEvent = ""
				currentData.Reset()
			}
		}
	}

	// Capture trailing event without a final blank line
	if currentEvent != "" || currentData.Len() > 0 {
		events = append(events, SSEEvent{
			Event: currentEvent,
			Data:  currentData.String(),
		})
	}

	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scanner error: %w", err)
	}
	return events, nil
}

// sseOutputData is the JSON payload inside stdout/stderr SSE events.
type sseOutputData struct {
	Data string `json:"data"`
}

// sseExitData is the JSON payload inside exit SSE events.
type sseExitData struct {
	ExitCode   int   `json:"exit_code"`
	DurationMs int64 `json:"duration_ms"`
}

// decodeSSEOutputData decodes the base64 "data" field from an SSE
// stdout/stderr event payload and returns the plain-text string.
func decodeSSEOutputData(rawJSON string) (string, error) {
	var out sseOutputData
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		return "", fmt.Errorf("unmarshal SSE output data: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	return string(decoded), nil
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

// randomSessionID returns a new UUID suitable for use as a sandbox session ID.
func randomSessionID() string {
	return uuid.New().String()
}
