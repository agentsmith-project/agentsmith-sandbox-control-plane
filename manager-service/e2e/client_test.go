//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ASBCP HTTP client (thin wrapper around net/http)
// ---------------------------------------------------------------------------

// asbcpClient sends authenticated requests to ASBCP's internal sandbox execution service.
type asbcpClient struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

func newClient() *asbcpClient {
	return &asbcpClient{
		baseURL:    suite.ASBCPURL,
		serviceKey: suite.ServiceKey,
		http:       &http.Client{Timeout: 120 * time.Second},
	}
}

// newUnauthClient returns a client with no service key (for auth failure tests).
func newUnauthClient() *asbcpClient {
	c := newClient()
	c.serviceKey = ""
	return c
}

// newWrongKeyClient returns a client with an incorrect service key.
func newWrongKeyClient() *asbcpClient {
	c := newClient()
	c.serviceKey = "definitely-wrong-key"
	return c
}

// workloadURL builds the canonical workload URL.
func (c *asbcpClient) workloadURL(wsID, projID, wlID string) string {
	return fmt.Sprintf("%s/v1/workspaces/%s/projects/%s/workloads/%s",
		c.baseURL, wsID, projID, wlID)
}

func (c *asbcpClient) bindingURL(wsID, projID, bindingID string) string {
	return fmt.Sprintf("%s/v1/workspaces/%s/projects/%s/workspace-bindings/%s",
		c.baseURL, wsID, projID, bindingID)
}

// Response is the raw HTTP response plus its decoded body bytes.
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

func (r *Response) DecodeJSON(v interface{}) error {
	return json.Unmarshal(r.Body, v)
}

func (r *Response) BodyString() string { return string(r.Body) }

// do sends an HTTP request and returns the Response.
func (c *asbcpClient) do(t *testing.T, method, url string, body io.Reader) *Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.serviceKey != "" {
		req.Header.Set("X-Service-Key", c.serviceKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return &Response{StatusCode: resp.StatusCode, Body: b, Header: resp.Header}
}

// ---------------------------------------------------------------------------
// Typed API methods
// ---------------------------------------------------------------------------

// CreateRequest is a subset of the workload.CreateRequest struct.
type CreateRequest struct {
	Image              string            `json:"image"`
	Command            []string          `json:"command,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	CPURequest         string            `json:"cpu_request,omitempty"`
	CPULimit           string            `json:"cpu_limit,omitempty"`
	MemoryRequest      string            `json:"memory_request,omitempty"`
	MemoryLimit        string            `json:"memory_limit,omitempty"`
	IdleTimeoutSec     int               `json:"idle_timeout_sec,omitempty"`
	MaxLifetimeSec     int               `json:"max_lifetime_sec,omitempty"`
	WorkspaceBindingID string            `json:"workspace_binding_id,omitempty"`
}

type WorkspaceBindingRequest struct {
	NamespaceID    string `json:"namespace_id"`
	MountBindingID string `json:"mount_binding_id,omitempty"`
}

type WorkspaceBindingResponse struct {
	BindingID        string `json:"binding_id"`
	WorkspaceID      string `json:"workspace_id"`
	ProjectID        string `json:"project_id"`
	Status           string `json:"status"`
	Namespace        string `json:"namespace"`
	PVName           string `json:"pv_name"`
	PVCName          string `json:"pvc_name"`
	VolumeHandle     string `json:"volume_handle"`
	NamespaceID      string `json:"namespace_id"`
	MountBindingID   string `json:"mount_binding_id"`
	VolumeID         string `json:"volume_id"`
	MountPath        string `json:"mount_path"`
	ReadOnly         bool   `json:"read_only"`
	StorageClassName string `json:"storage_class_name"`
}

// PodStatus mirrors workload.PodStatus.
type PodStatus struct {
	PodName        string `json:"pod_name"`
	Phase          string `json:"phase"`
	IP             string `json:"ip"`
	StartedAt      string `json:"started_at"`
	LastActivityAt string `json:"last_activity_at"`
	ExpiresAt      string `json:"expires_at"`
	Message        string `json:"message"`
}

// KeepaliveResponse mirrors workload.KeepaliveResponse.
type KeepaliveResponse struct {
	ExpiresAt string `json:"expires_at"`
}

// DeleteResponse mirrors workload.DeleteResponse.
type DeleteResponse struct {
	Message string `json:"message"`
}

// ExecResponse mirrors workload.ExecResponse.
type ExecResponse struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
}

// CreateWorkload sends PUT /v1/workspaces/{wsID}/projects/{projID}/workloads/{wlID}.
func (c *asbcpClient) CreateWorkload(t *testing.T, wsID, projID, wlID string, req CreateRequest) *Response {
	t.Helper()
	if c.serviceKey == suite.ServiceKey {
		ensureWorkspaceBindingForWorkload(t, wsID, projID, wlID, &req)
	}
	return c.do(t, http.MethodPut, c.workloadURL(wsID, projID, wlID), jsonBody(req))
}

func (c *asbcpClient) EnsureWorkspaceBinding(t *testing.T, wsID, projID, bindingID string, req WorkspaceBindingRequest) *Response {
	t.Helper()
	return c.do(t, http.MethodPut, c.bindingURL(wsID, projID, bindingID), jsonBody(req))
}

// GetWorkload sends GET /v1/workspaces/{wsID}/projects/{projID}/workloads/{wlID}.
func (c *asbcpClient) GetWorkload(t *testing.T, wsID, projID, wlID string) *Response {
	t.Helper()
	return c.do(t, http.MethodGet, c.workloadURL(wsID, projID, wlID), nil)
}

// DeleteWorkload sends DELETE /v1/workspaces/{wsID}/projects/{projID}/workloads/{wlID}.
func (c *asbcpClient) DeleteWorkload(t *testing.T, wsID, projID, wlID string) *Response {
	t.Helper()
	return c.do(t, http.MethodDelete, c.workloadURL(wsID, projID, wlID), nil)
}

// Keepalive sends POST .../keepalive.
func (c *asbcpClient) Keepalive(t *testing.T, wsID, projID, wlID string) *Response {
	t.Helper()
	return c.do(t, http.MethodPost, c.workloadURL(wsID, projID, wlID)+"/keepalive", nil)
}

// Exec sends POST .../exec.
func (c *asbcpClient) Exec(t *testing.T, wsID, projID, wlID string, cmd []string, timeoutSec int) *Response {
	t.Helper()
	payload := map[string]interface{}{"cmd": cmd, "timeout_seconds": timeoutSec}
	return c.do(t, http.MethodPost, c.workloadURL(wsID, projID, wlID)+"/exec",
		bytes.NewReader(mustMarshal(payload)))
}

// Healthz sends GET /healthz (no auth required).
func (c *asbcpClient) Healthz(t *testing.T) *Response {
	t.Helper()
	return c.do(t, http.MethodGet, c.baseURL+"/healthz", nil)
}

// Readyz sends GET /readyz (no auth required).
func (c *asbcpClient) Readyz(t *testing.T) *Response {
	t.Helper()
	return c.do(t, http.MethodGet, c.baseURL+"/readyz", nil)
}

// Metrics sends GET /metrics (no auth required).
func (c *asbcpClient) Metrics(t *testing.T) *Response {
	t.Helper()
	return c.do(t, http.MethodGet, c.baseURL+"/metrics", nil)
}

// ---------------------------------------------------------------------------
// Test helpers built on the client
// ---------------------------------------------------------------------------

// mustCreateWorkload creates a workload, fails the test if response isn't 201/200.
// Returns the decoded PodStatus.
func mustCreateWorkload(t *testing.T, wsID, projID, wlID string, req CreateRequest) PodStatus {
	t.Helper()
	resp := newClient().CreateWorkload(t, wsID, projID, wlID, req)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create workload %s: expected 201/200, got %d – %s",
			wlID, resp.StatusCode, resp.BodyString())
	}
	var ps PodStatus
	if err := resp.DecodeJSON(&ps); err != nil {
		t.Fatalf("decode PodStatus: %v – body: %s", err, resp.BodyString())
	}
	return ps
}

func ensureWorkspaceBindingForWorkload(t *testing.T, wsID, projID, wlID string, req *CreateRequest) {
	t.Helper()
	if req.Image == "" {
		return
	}
	if req.WorkspaceBindingID == "" {
		req.WorkspaceBindingID = bindingIDForWorkload(wlID)
	}
	resp := newClient().EnsureWorkspaceBinding(t, wsID, projID, req.WorkspaceBindingID, WorkspaceBindingRequest{
		NamespaceID:    suite.AFSCPNamespaceID,
		MountBindingID: req.WorkspaceBindingID,
	})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("ensure binding %s: expected 201/200, got %d – %s",
			req.WorkspaceBindingID, resp.StatusCode, resp.BodyString())
	}
}

func taskHomePath(wlID string) string {
	return "/home/" + wlID
}

func taskWorkspacePath(wlID string) string {
	return taskHomePath(wlID) + "/workspace"
}

func bindingIDForWorkload(wlID string) string {
	return "wmb_" + wlID
}

// mustDeleteWorkload deletes a workload and fails the test if response isn't 200.
func mustDeleteWorkload(t *testing.T, wsID, projID, wlID string) {
	t.Helper()
	resp := newClient().DeleteWorkload(t, wsID, projID, wlID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete workload %s: expected 200, got %d – %s",
			wlID, resp.StatusCode, resp.BodyString())
	}
}

// waitWorkloadRunning polls the GET endpoint until phase == "Running" or fails.
func waitWorkloadRunning(t *testing.T, wsID, projID, wlID string, timeout time.Duration) PodStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := newClient().GetWorkload(t, wsID, projID, wlID)
		var ps PodStatus
		if err := resp.DecodeJSON(&ps); err == nil && ps.Phase == "Running" {
			return ps
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("workload %s did not reach Running within %s", wlID, timeout)
	return PodStatus{}
}

// mustMarshal marshals v and panics on error (only used in test helpers).
func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
