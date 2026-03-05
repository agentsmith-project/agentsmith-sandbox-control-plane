package workload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newFakeK8sAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		status := &metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Message:  "not found",
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		}
		json.NewEncoder(w).Encode(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestK8sClient(t *testing.T, apiURL string) *k8s.Client {
	t.Helper()
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
users:
- name: test
  user:
    token: fake-token
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, apiURL)
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", path)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)
	return client
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	api := newFakeK8sAPI(t)
	client := newTestK8sClient(t, api.URL)
	executor := k8s.NewExecutor(client)
	return NewHandler(client, executor, nil, "test-pvc")
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	require.NoError(t, json.NewDecoder(rec.Body).Decode(v))
}

// ---------------------------------------------------------------------------
// PodName
// ---------------------------------------------------------------------------

func TestPodName(t *testing.T) {
	assert.Equal(t, "workload-abc-123", PodName("abc-123"))
	assert.Equal(t, "workload-", PodName(""))
	assert.Equal(t, "workload-x", PodName("x"))
	assert.Equal(t, "workload-with spaces", PodName("with spaces"))
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestConstants(t *testing.T) {
	assert.Equal(t, "managed-workload", WorkloadLabel)
	assert.Equal(t, 30*time.Minute, DefaultIdleTimeout)
	assert.Equal(t, 24*time.Hour, DefaultMaxLifetime)
	assert.Equal(t, []string{"tail", "-f", "/dev/null"}, DefaultKeepAliveCommand)
}

// ---------------------------------------------------------------------------
// NewHandler
// ---------------------------------------------------------------------------

func TestNewHandler(t *testing.T) {
	h := newTestHandler(t)
	require.NotNil(t, h)
	assert.NotNil(t, h.k8sClient)
	assert.NotNil(t, h.executor)
	assert.Equal(t, "test-pvc", h.pvcName)
	assert.Nil(t, h.storage)
}

// ---------------------------------------------------------------------------
// parseRoute
// ---------------------------------------------------------------------------

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantWsID    string
		wantProjID  string
		wantWlID    string
		wantAction  string
		wantOK      bool
	}{
		{
			name:       "full path no action",
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1",
			wantWsID:   "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "", wantOK: true,
		},
		{
			name:       "with keepalive action",
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/keepalive",
			wantWsID:   "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "keepalive", wantOK: true,
		},
		{
			name:       "with exec action",
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/exec",
			wantWsID:   "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "exec", wantOK: true,
		},
		{
			name: "wrong prefix",
			path: "/v1/workloads/ws-1/projects/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name: "too few parts",
			path: "/v1/workspaces/ws-1/projects",
			wantOK: false,
		},
		{
			name: "wrong segment - not projects",
			path: "/v1/workspaces/ws-1/other/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name: "wrong segment - not workloads",
			path: "/v1/workspaces/ws-1/projects/proj-1/pods/wl-1",
			wantOK: false,
		},
		{
			name: "empty workspace ID",
			path: "/v1/workspaces//projects/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name: "empty project ID",
			path: "/v1/workspaces/ws-1/projects//workloads/wl-1",
			wantOK: false,
		},
		{
			name: "empty workload ID",
			path: "/v1/workspaces/ws-1/projects/proj-1/workloads/",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsID, projID, wlID, action, ok := parseRoute(tt.path)
			assert.Equal(t, tt.wantOK, ok, "ok mismatch")
			if tt.wantOK {
				assert.Equal(t, tt.wantWsID, wsID)
				assert.Equal(t, tt.wantProjID, projID)
				assert.Equal(t, tt.wantWlID, wlID)
				assert.Equal(t, tt.wantAction, action)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// jsonResponse / jsonError
// ---------------------------------------------------------------------------

func TestJsonResponse(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       interface{}
		wantStatus int
	}{
		{"200 with struct", http.StatusOK, PodStatus{Phase: "Running", PodName: "p"}, http.StatusOK},
		{"201 created", http.StatusCreated, PodStatus{Phase: "Pending"}, http.StatusCreated},
		{"200 with map", http.StatusOK, map[string]string{"k": "v"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			jsonResponse(rec, tt.status, tt.body)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var raw json.RawMessage
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&raw), "body should be valid JSON")
		})
	}
}

func TestJsonResponse_PodStatusFields(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonResponse(rec, http.StatusOK, PodStatus{
		PodName:        "my-pod",
		Phase:          "Running",
		IP:             "10.0.0.1",
		StartedAt:      "2025-01-01T00:00:00Z",
		LastActivityAt: "2025-01-01T01:00:00Z",
		ExpiresAt:      "2025-01-02T00:00:00Z",
		Message:        "all good",
	})

	var got PodStatus
	decodeJSON(t, rec, &got)
	assert.Equal(t, "my-pod", got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "10.0.0.1", got.IP)
	assert.Equal(t, "2025-01-01T00:00:00Z", got.StartedAt)
	assert.Equal(t, "2025-01-01T01:00:00Z", got.LastActivityAt)
	assert.Equal(t, "2025-01-02T00:00:00Z", got.ExpiresAt)
	assert.Equal(t, "all good", got.Message)
}

func TestJsonResponse_OmitsEmptyFields(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonResponse(rec, http.StatusOK, PodStatus{Phase: "offline"})

	var raw map[string]interface{}
	decodeJSON(t, rec, &raw)
	assert.Equal(t, "offline", raw["phase"])
	_, hasPodName := raw["pod_name"]
	assert.False(t, hasPodName, "empty pod_name should be omitted")
	_, hasIP := raw["ip"]
	assert.False(t, hasIP, "empty ip should be omitted")
}

func TestJsonError(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		message string
	}{
		{"400 bad request", http.StatusBadRequest, "invalid input"},
		{"404 not found", http.StatusNotFound, "pod not found"},
		{"500 internal", http.StatusInternalServerError, "something broke"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			jsonError(rec, tt.status, tt.message)

			assert.Equal(t, tt.status, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var body map[string]string
			decodeJSON(t, rec, &body)
			assert.Equal(t, tt.message, body["error"])
			assert.Len(t, body, 1, "error response should only have 'error' key")
		})
	}
}

// ---------------------------------------------------------------------------
// routeRequest – routing
// ---------------------------------------------------------------------------

func TestRouteRequest_NotFound(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name string
		path string
	}{
		{"old URL format", "/v1/workloads/123/pods"},
		{"missing workloads segment", "/v1/workspaces/ws/projects/proj"},
		{"wrong segment", "/v1/workspaces/ws/other/proj/workloads/wl"},
		{"empty workspace", "/v1/workspaces//projects/proj/workloads/wl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.routeRequest(rec, req)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestRouteRequest_MethodNotAllowed(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"POST to workload", http.MethodPost, "/v1/workspaces/ws/projects/proj/workloads/wl"},
		{"PATCH to workload", http.MethodPatch, "/v1/workspaces/ws/projects/proj/workloads/wl"},
		{"HEAD to workload", http.MethodHead, "/v1/workspaces/ws/projects/proj/workloads/wl"},
		{"GET keepalive", http.MethodGet, "/v1/workspaces/ws/projects/proj/workloads/wl/keepalive"},
		{"PUT keepalive", http.MethodPut, "/v1/workspaces/ws/projects/proj/workloads/wl/keepalive"},
		{"DELETE keepalive", http.MethodDelete, "/v1/workspaces/ws/projects/proj/workloads/wl/keepalive"},
		{"GET exec", http.MethodGet, "/v1/workspaces/ws/projects/proj/workloads/wl/exec"},
		{"PUT exec", http.MethodPut, "/v1/workspaces/ws/projects/proj/workloads/wl/exec"},
		{"POST unknown", http.MethodPost, "/v1/workspaces/ws/projects/proj/workloads/wl/unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			h.routeRequest(rec, req)
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

func TestRouteRequest_ValidRoutes(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "GET workload → offline (pod not found)",
			method:     http.MethodGet,
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "DELETE workload → 404 (pod not found)",
			method:     http.MethodDelete,
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "POST keepalive → 404 (pod not found)",
			method:     http.MethodPost,
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/keepalive",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "PUT workload with empty body → 400 (decode error)",
			method:     http.MethodPut,
			path:       "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1",
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader([]byte(tt.body)))
			rec := httptest.NewRecorder()
			h.routeRequest(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestRouteRequest_WorkloadIDExtracted(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws-1/projects/proj-1/workloads/my-workload-42", nil)
	rec := httptest.NewRecorder()
	h.routeRequest(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	decodeJSON(t, rec, &got)
	assert.Equal(t, "offline", got.Phase)
}

// ---------------------------------------------------------------------------
// RegisterRoutes
// ---------------------------------------------------------------------------

func TestRegisterRoutes(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws-1/projects/proj-1/workloads/abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	decodeJSON(t, rec, &got)
	assert.Equal(t, "offline", got.Phase)
}

// ---------------------------------------------------------------------------
// handleCreatePod – validation
// ---------------------------------------------------------------------------

func TestHandleCreatePod_InvalidJSON(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Contains(t, body["error"], "invalid request body")
}

func TestHandleCreatePod_EmptyBody(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Contains(t, body["error"], "invalid request body")
}

func TestHandleCreatePod_MissingImage(t *testing.T) {
	h := &Handler{}
	payload, _ := json.Marshal(CreateRequest{})
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "image is required", body["error"])
}

// ---------------------------------------------------------------------------
// handleExec – validation
// ---------------------------------------------------------------------------

func TestHandleExec_InvalidJSON(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{bad")))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Contains(t, body["error"], "invalid request body")
}

func TestHandleExec_EmptyCmd(t *testing.T) {
	h := &Handler{}
	payload, _ := json.Marshal(ExecRequest{Cmd: []string{}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "cmd is required", body["error"])
}

func TestHandleExec_MissingCmd(t *testing.T) {
	h := &Handler{}
	payload, _ := json.Marshal(ExecRequest{})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "wl-1")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "cmd is required", body["error"])
}

// ---------------------------------------------------------------------------
// handleExec – pod not found
// ---------------------------------------------------------------------------

func TestHandleExec_PodNotFound(t *testing.T) {
	h := newTestHandler(t)
	payload, _ := json.Marshal(ExecRequest{Cmd: []string{"echo", "hello"}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "nonexistent")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "pod not found", body["error"])
}

// ---------------------------------------------------------------------------
// isValidK8sName
// ---------------------------------------------------------------------------

func TestIsValidK8sName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple", "abc", true},
		{"with hyphens", "my-workload-1", true},
		{"digits only", "123", true},
		{"single char", "x", true},
		{"max 63 chars", "a23456789012345678901234567890123456789012345678901234567890123", true},
		{"empty", "", false},
		{"too long 64 chars", "a234567890123456789012345678901234567890123456789012345678901234", false},
		{"uppercase", "MyWorkload", false},
		{"underscore", "my_workload", false},
		{"starts with hyphen", "-abc", false},
		{"ends with hyphen", "abc-", false},
		{"spaces", "with spaces", false},
		{"dots", "my.workload", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidK8sName(tt.input))
		})
	}
}

func TestRouteRequest_InvalidWorkloadID(t *testing.T) {
	h := &Handler{}
	tests := []struct {
		name string
		path string
	}{
		{"uppercase", "/v1/workspaces/ws/projects/proj/workloads/MyWorkload"},
		{"underscore", "/v1/workspaces/ws/projects/proj/workloads/my_workload"},
		{"spaces", "/v1/workspaces/ws/projects/proj/workloads/my%20workload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.routeRequest(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// handleGetPod – pod not found
// ---------------------------------------------------------------------------

func TestHandleGetPod_NotFound(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "nonexistent")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got PodStatus
	decodeJSON(t, rec, &got)
	assert.Equal(t, "offline", got.Phase)
	assert.Empty(t, got.PodName)
	assert.Empty(t, got.IP)
	assert.Empty(t, got.StartedAt)
}

// ---------------------------------------------------------------------------
// handleDeletePod – pod not found
// ---------------------------------------------------------------------------

func TestHandleDeletePod_NotFound(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "nonexistent")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "pod not found", body["error"])
}

// ---------------------------------------------------------------------------
// handleKeepalive – pod not found
// ---------------------------------------------------------------------------

func TestHandleKeepalive_NotFound(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "nonexistent")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]string
	decodeJSON(t, rec, &body)
	assert.Equal(t, "pod not found", body["error"])
}

// ---------------------------------------------------------------------------
// buildPod
// ---------------------------------------------------------------------------

func TestBuildPod_BasicFields(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(DefaultIdleTimeout)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "ws-1/wl-1/main",
		map[string]string{"WORKSPACE_PATH": "/workspace"},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "ubuntu:22.04"},
		now, expiresAt,
	)
	require.NoError(t, err)

	assert.Equal(t, "workload-wl-1", pod.Name)
	assert.Equal(t, "test-ns", pod.Namespace)

	assert.Equal(t, WorkloadLabel, pod.Labels["app"])
	assert.Equal(t, "wl-1", pod.Labels["workload_id"])
	assert.Equal(t, "ws-1", pod.Labels["workspace_id"])
	assert.Equal(t, "proj-1", pod.Labels["project_id"])

	assert.Equal(t, v1.RestartPolicyNever, pod.Spec.RestartPolicy)
	require.NotNil(t, pod.Spec.TerminationGracePeriodSeconds)
	assert.Equal(t, int64(30), *pod.Spec.TerminationGracePeriodSeconds)
	require.NotNil(t, pod.Spec.AutomountServiceAccountToken)
	assert.False(t, *pod.Spec.AutomountServiceAccountToken)

	require.NotNil(t, pod.Spec.SecurityContext)
	require.NotNil(t, pod.Spec.SecurityContext.RunAsNonRoot)
	assert.True(t, *pod.Spec.SecurityContext.RunAsNonRoot)
	require.NotNil(t, pod.Spec.SecurityContext.RunAsUser)
	assert.Equal(t, int64(1000), *pod.Spec.SecurityContext.RunAsUser)

	require.Len(t, pod.Spec.Containers, 1)
	c := pod.Spec.Containers[0]
	assert.Equal(t, "main", c.Name)
	assert.Equal(t, "ubuntu:22.04", c.Image)
	assert.Equal(t, "/workspace", c.WorkingDir)
	assert.Equal(t, DefaultKeepAliveCommand, c.Command)

	require.Len(t, c.VolumeMounts, 1)
	vm := c.VolumeMounts[0]
	assert.Equal(t, "workspace", vm.Name)
	assert.Equal(t, "/workspace", vm.MountPath)
	assert.Equal(t, "ws-1/wl-1/main", vm.SubPath)

	require.Len(t, pod.Spec.Volumes, 1)
	vol := pod.Spec.Volumes[0]
	assert.Equal(t, "workspace", vol.Name)
	require.NotNil(t, vol.PersistentVolumeClaim)
	assert.Equal(t, "test-pvc", vol.PersistentVolumeClaim.ClaimName)
}

func TestBuildPod_CustomCommand(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	customCmd := []string{"python", "-m", "http.server", "8080"}
	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		customCmd,
		CreateRequest{Image: "python:3.12"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	assert.Equal(t, customCmd, pod.Spec.Containers[0].Command)
}

func TestBuildPod_DefaultTimeouts(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(DefaultIdleTimeout)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, expiresAt,
	)
	require.NoError(t, err)

	a := pod.Annotations
	assert.Equal(t, strconv.Itoa(int(DefaultIdleTimeout.Seconds())), a["workload/idleTimeoutSec"])
	assert.Equal(t, strconv.Itoa(int(DefaultMaxLifetime.Seconds())), a["workload/maxLifetimeSec"])
	assert.Equal(t, now.Format(time.RFC3339), a["last_activity_at"])
	assert.Equal(t, expiresAt.Format(time.RFC3339), a["expires_at"])
	_, hasDeadAnnotation := a["workload/lastActiveAt"]
	assert.False(t, hasDeadAnnotation, "workload/lastActiveAt should not exist")
	_, hasDeadAnnotation2 := a["workload/expiresAt"]
	assert.False(t, hasDeadAnnotation2, "workload/expiresAt should not exist")

	expectedMaxExpires := now.Add(DefaultMaxLifetime)
	assert.Equal(t, expectedMaxExpires.Format(time.RFC3339), a["workload/maxExpiresAt"])
}

func TestBuildPod_CustomTimeouts(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	customIdle := 600
	customMax := 7200
	expiresAt := now.Add(time.Duration(customIdle) * time.Second)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{
			Image:          "img",
			IdleTimeoutSec: customIdle, MaxLifetimeSec: customMax,
		},
		now, expiresAt,
	)
	require.NoError(t, err)

	a := pod.Annotations
	assert.Equal(t, "600", a["workload/idleTimeoutSec"])
	assert.Equal(t, "7200", a["workload/maxLifetimeSec"])
	assert.Equal(t, expiresAt.Format(time.RFC3339), a["expires_at"])

	expectedMaxExpires := now.Add(time.Duration(customMax) * time.Second)
	assert.Equal(t, expectedMaxExpires.Format(time.RFC3339), a["workload/maxExpiresAt"])
}

func TestBuildPod_EnvVars(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	env := map[string]string{
		"WORKSPACE_PATH": "/workspace",
		"API_KEY":        "secret123",
		"DB_URL":         "postgres://localhost",
	}

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path", env,
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	c := pod.Spec.Containers[0]
	envMap := make(map[string]string, len(c.Env))
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}

	assert.Len(t, c.Env, 3)
	assert.Equal(t, "/workspace", envMap["WORKSPACE_PATH"])
	assert.Equal(t, "secret123", envMap["API_KEY"])
	assert.Equal(t, "postgres://localhost", envMap["DB_URL"])
}

func TestBuildPod_EmptyEnv(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	assert.Empty(t, pod.Spec.Containers[0].Env)
}

func TestBuildPod_ResourceRequestsOnly(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{
			Image:         "img",
			CPURequest:    "250m",
			MemoryRequest: "512Mi",
		},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	require.NotNil(t, res.Requests)
	assert.True(t, res.Requests[v1.ResourceCPU].Equal(resource.MustParse("250m")))
	assert.True(t, res.Requests[v1.ResourceMemory].Equal(resource.MustParse("512Mi")))
	assert.Nil(t, res.Limits)
}

func TestBuildPod_ResourceLimitsOnly(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{
			Image:       "img",
			CPULimit:    "2",
			MemoryLimit: "4Gi",
		},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	assert.Nil(t, res.Requests)
	require.NotNil(t, res.Limits)
	assert.True(t, res.Limits[v1.ResourceCPU].Equal(resource.MustParse("2")))
	assert.True(t, res.Limits[v1.ResourceMemory].Equal(resource.MustParse("4Gi")))
}

func TestBuildPod_ResourceRequestsAndLimits(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{
			Image:         "img",
			CPURequest:    "100m",
			CPULimit:      "500m",
			MemoryRequest: "256Mi",
			MemoryLimit:   "1Gi",
		},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	require.NotNil(t, res.Requests)
	require.NotNil(t, res.Limits)
	assert.True(t, res.Requests[v1.ResourceCPU].Equal(resource.MustParse("100m")))
	assert.True(t, res.Requests[v1.ResourceMemory].Equal(resource.MustParse("256Mi")))
	assert.True(t, res.Limits[v1.ResourceCPU].Equal(resource.MustParse("500m")))
	assert.True(t, res.Limits[v1.ResourceMemory].Equal(resource.MustParse("1Gi")))
}

func TestBuildPod_NoResources(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	assert.Nil(t, res.Requests)
	assert.Nil(t, res.Limits)
}

func TestBuildPod_PartialCPURequest(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img", CPURequest: "500m"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	require.NotNil(t, res.Requests)
	assert.True(t, res.Requests[v1.ResourceCPU].Equal(resource.MustParse("500m")))
	_, hasMemory := res.Requests[v1.ResourceMemory]
	assert.False(t, hasMemory)
}

func TestBuildPod_PartialMemoryLimit(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img", MemoryLimit: "2Gi"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	res := pod.Spec.Containers[0].Resources
	require.NotNil(t, res.Limits)
	assert.True(t, res.Limits[v1.ResourceMemory].Equal(resource.MustParse("2Gi")))
	_, hasCPU := res.Limits[v1.ResourceCPU]
	assert.False(t, hasCPU)
}

func TestBuildPod_PVCName(t *testing.T) {
	api := newFakeK8sAPI(t)
	client := newTestK8sClient(t, api.URL)
	executor := k8s.NewExecutor(client)
	h := NewHandler(client, executor, nil, "my-juicefs-pvc")

	now := time.Now().UTC()
	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	require.Len(t, pod.Spec.Volumes, 1)
	assert.Equal(t, "my-juicefs-pvc", pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
}

func TestBuildPod_AnnotationTimestamps(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 3, 1, 12, 30, 0, 0, time.UTC)
	expiresAt := time.Date(2025, 3, 2, 12, 30, 0, 0, time.UTC)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img"},
		now, expiresAt,
	)
	require.NoError(t, err)

	a := pod.Annotations
	assert.Equal(t, "2025-03-01T12:30:00Z", a["last_activity_at"])
	assert.Equal(t, "2025-03-02T12:30:00Z", a["expires_at"])

	assert.Equal(t, 5, len(a), "should have exactly 5 annotations (last_activity_at, expires_at, idleTimeoutSec, maxLifetimeSec, maxExpiresAt)")
}

func TestParseResourceRequirements_Invalid(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
	}{
		{"invalid cpu_request", CreateRequest{Image: "img", CPURequest: "not-a-number"}},
		{"invalid memory_request", CreateRequest{Image: "img", MemoryRequest: "xyz"}},
		{"invalid cpu_limit", CreateRequest{Image: "img", CPULimit: "1.2.3"}},
		{"invalid memory_limit", CreateRequest{Image: "img", MemoryLimit: "not-a-quantity"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseResourceRequirements(tt.req)
			require.Error(t, err)
		})
	}
}

func TestBuildPod_InvalidResourceReturnsError(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	_, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", "sub/path",
		map[string]string{},
		DefaultKeepAliveCommand,
		CreateRequest{Image: "img", CPURequest: "invalid"},
		now, now.Add(time.Hour),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cpu_request")
}
