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
	"strings"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/afscp"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/workspacebinding"
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
	return NewHandler(client, executor)
}

func validCreateRequest(req CreateRequest) CreateRequest {
	req.WorkspaceBindingID = "wmb_demo"
	mount := validResolvedMount(req.WorkspaceBindingID)
	req.resolvedMount = &mount
	return req
}

func validResolvedMount(bindingID string) workspacebinding.ResolvedMount {
	return workspacebinding.ResolvedMount{
		PVCName:             workspacebinding.PVCName("ws-1", "proj-1", bindingID),
		NamespaceID:         "ns_demo",
		MountBindingID:      bindingID,
		VolumeID:            "vol_demo",
		MountPath:           "/home/task-plan",
		ReadOnly:            false,
		PayloadVolumeSubdir: "afscp/ns_demo/repos/repo_demo/payload",
		SecurityPolicy:      validResolvedMountSecurityPolicy(),
	}
}

func validResolvedMountSecurityPolicy() afscp.SecurityPolicy {
	return afscp.SecurityPolicy{
		RunAsNonRoot:             true,
		AllowPrivileged:          false,
		JVSControlOutsidePayload: true,
	}
}

func testBindingPVC(workspaceID, projectID, bindingID string) *v1.PersistentVolumeClaim {
	pvName := testBindingPVName(workspaceID, projectID, bindingID)
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workspacebinding.PVCName(workspaceID, projectID, bindingID),
			Namespace: "test-ns",
			Annotations: map[string]string{
				"mbos.io/afscp-namespace-id":          "ns_demo",
				"mbos.io/afscp-mount-binding-id":      bindingID,
				"mbos.io/afscp-volume-id":             "vol_demo",
				"mbos.io/payload-volume-subdir":       "afscp/ns_demo/repos/repo_demo/payload",
				"mbos.io/mount-path":                  "/home/task-plan",
				"mbos.io/read-only":                   "true",
				"mbos.io/run-as-non-root":             "true",
				"mbos.io/allow-privileged":            "false",
				"mbos.io/jvs-control-outside-payload": "true",
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			VolumeName: pvName,
		},
	}
}

func testBindingPV(workspaceID, projectID, bindingID string) *v1.PersistentVolume {
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: testBindingPVName(workspaceID, projectID, bindingID),
			Annotations: map[string]string{
				"mbos.io/afscp-namespace-id":          "ns_demo",
				"mbos.io/afscp-mount-binding-id":      bindingID,
				"mbos.io/afscp-volume-id":             "vol_demo",
				"mbos.io/payload-volume-subdir":       "afscp/ns_demo/repos/repo_demo/payload",
				"mbos.io/mount-path":                  "/home/task-plan",
				"mbos.io/read-only":                   "true",
				"mbos.io/run-as-non-root":             "true",
				"mbos.io/allow-privileged":            "false",
				"mbos.io/jvs-control-outside-payload": "true",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					VolumeAttributes: map[string]string{
						"subdir": "afscp/ns_demo/repos/repo_demo/payload",
					},
					NodePublishSecretRef: &v1.SecretReference{
						Namespace: "afscp-mounts",
						Name:      "juicefs-vol-demo",
					},
				},
			},
		},
	}
}

func testBindingPVName(workspaceID, projectID, bindingID string) string {
	return strings.Replace(workspacebinding.PVCName(workspaceID, projectID, bindingID), "juicefs-pvc-", "juicefs-pv-", 1)
}

func writeTestBindingPVCIfRequested(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) bool {
	return writeTestBindingResourceIfRequested(w, r, nil, nil, workspaceID, projectID, bindingID)
}

func writeTestBindingResourceIfRequested(w http.ResponseWriter, r *http.Request, pvc *v1.PersistentVolumeClaim, pv *v1.PersistentVolume, workspaceID, projectID, bindingID string) bool {
	if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/persistentvolumeclaims/") {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/persistentvolumes/") {
			w.Header().Set("Content-Type", "application/json")
			if pv == nil {
				pv = testBindingPV(workspaceID, projectID, bindingID)
			}
			_ = json.NewEncoder(w).Encode(pv)
			return true
		}
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if pvc == nil {
		pvc = testBindingPVC(workspaceID, projectID, bindingID)
	}
	_ = json.NewEncoder(w).Encode(pvc)
	return true
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
}

// ---------------------------------------------------------------------------
// NewHandler
// ---------------------------------------------------------------------------

func TestNewHandler(t *testing.T) {
	h := newTestHandler(t)
	require.NotNil(t, h)
	assert.NotNil(t, h.k8sClient)
	assert.NotNil(t, h.executor)
}

// ---------------------------------------------------------------------------
// parseRoute
// ---------------------------------------------------------------------------

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantWsID   string
		wantProjID string
		wantWlID   string
		wantAction string
		wantOK     bool
	}{
		{
			name:     "full path no action",
			path:     "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1",
			wantWsID: "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "", wantOK: true,
		},
		{
			name:     "with keepalive action",
			path:     "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/keepalive",
			wantWsID: "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "keepalive", wantOK: true,
		},
		{
			name:     "with exec action",
			path:     "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/exec",
			wantWsID: "ws-1", wantProjID: "proj-1", wantWlID: "wl-1",
			wantAction: "exec", wantOK: true,
		},
		{
			name:   "wrong prefix",
			path:   "/v1/workloads/ws-1/projects/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name:   "too few parts",
			path:   "/v1/workspaces/ws-1/projects",
			wantOK: false,
		},
		{
			name:   "wrong segment - not projects",
			path:   "/v1/workspaces/ws-1/other/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name:   "wrong segment - not workloads",
			path:   "/v1/workspaces/ws-1/projects/proj-1/pods/wl-1",
			wantOK: false,
		},
		{
			name:   "empty workspace ID",
			path:   "/v1/workspaces//projects/proj-1/workloads/wl-1",
			wantOK: false,
		},
		{
			name:   "empty project ID",
			path:   "/v1/workspaces/ws-1/projects//workloads/wl-1",
			wantOK: false,
		},
		{
			name:   "empty workload ID",
			path:   "/v1/workspaces/ws-1/projects/proj-1/workloads/",
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

func TestHandleCreatePodRejectsFieldsOutsideContract(t *testing.T) {
	h := &Handler{}
	tests := []string{
		`{"image":"img","workspace_binding_id":"wmb_demo","mount_path":"/home/task","sub_path":"agent-tasks/task","working_dir":"/home/task/workspace"}`,
		`{"image":"img","workspace_binding_id":"wmb_demo","metadata_url":"postgres://raw","bucket":"raw"}`,
	}

	for _, payload := range tests {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	}
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
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "nonexistent")

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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{"WORKSPACE_PATH": "/workspace"},
		validCreateRequest(CreateRequest{Image: "ubuntu:22.04"}),
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
	require.NotNil(t, pod.Spec.SecurityContext.RunAsGroup)
	assert.Equal(t, int64(1000), *pod.Spec.SecurityContext.RunAsGroup)
	require.NotNil(t, pod.Spec.SecurityContext.FSGroup)
	assert.Equal(t, int64(1000), *pod.Spec.SecurityContext.FSGroup)
	require.NotNil(t, pod.Spec.SecurityContext.FSGroupChangePolicy)
	assert.Equal(t, v1.FSGroupChangeOnRootMismatch, *pod.Spec.SecurityContext.FSGroupChangePolicy)

	require.Len(t, pod.Spec.Containers, 1)
	c := pod.Spec.Containers[0]
	assert.Equal(t, "main", c.Name)
	assert.Equal(t, "ubuntu:22.04", c.Image)
	assert.Equal(t, "/home/task-plan/workspace", c.WorkingDir)
	assert.Nil(t, c.Command)

	require.NotNil(t, c.SecurityContext)
	require.NotNil(t, c.SecurityContext.AllowPrivilegeEscalation)
	assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
	require.NotNil(t, c.SecurityContext.Capabilities)
	assert.Equal(t, []v1.Capability{"ALL"}, c.SecurityContext.Capabilities.Drop)
	require.NotNil(t, c.SecurityContext.SeccompProfile)
	assert.Equal(t, v1.SeccompProfileTypeRuntimeDefault, c.SecurityContext.SeccompProfile.Type)

	require.Len(t, c.VolumeMounts, 1)
	vm := c.VolumeMounts[0]
	assert.Equal(t, "workspace", vm.Name)
	assert.Equal(t, "/home/task-plan", vm.MountPath)
	assert.Empty(t, vm.SubPath)
	assert.False(t, vm.ReadOnly)

	require.Len(t, pod.Spec.Volumes, 1)
	vol := pod.Spec.Volumes[0]
	assert.Equal(t, "workspace", vol.Name)
	require.NotNil(t, vol.PersistentVolumeClaim)
	assert.Equal(t, workspacebinding.PVCName("ws-1", "proj-1", "wmb_demo"), vol.PersistentVolumeClaim.ClaimName)
	assert.False(t, vol.PersistentVolumeClaim.ReadOnly)
}

func TestBuildPod_UsesAFSCPPlanPaths(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	taskHome := "/home/task-plan"
	workspacePath := "/home/task-plan/workspace"

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{
			"API_KEY":        "secret123",
			"HOME":           "/tmp/caller-home",
			"WORKSPACE_PATH": "/workspace/caller",
		},
		validCreateRequest(CreateRequest{Image: "ubuntu:22.04"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	require.Len(t, pod.Spec.Containers, 1)
	c := pod.Spec.Containers[0]
	assert.Equal(t, workspacePath, c.WorkingDir)

	require.Len(t, c.VolumeMounts, 1)
	vm := c.VolumeMounts[0]
	assert.Equal(t, "workspace", vm.Name)
	assert.Equal(t, taskHome, vm.MountPath)
	assert.Empty(t, vm.SubPath)
	assert.False(t, vm.ReadOnly)

	envMap := make(map[string]string, len(c.Env))
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, taskHome, envMap["TASK_HOME"])
	assert.Equal(t, taskHome, envMap["HOME"])
	assert.Equal(t, workspacePath, envMap["WORKSPACE_PATH"])
	assert.Equal(t, "secret123", envMap["API_KEY"])
}

func TestBuildPod_WorkspaceInitContainerPreparesWritableDirs(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	taskHome := "/home/task-plan"
	workspacePath := "/home/task-plan/workspace"
	artifactsPath := "/home/task-plan/workspace/.artifacts"

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "ubuntu:22.04"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	require.Len(t, pod.Spec.InitContainers, 1)
	init := pod.Spec.InitContainers[0]
	assert.Equal(t, "workspace-init", init.Name)
	assert.Equal(t, "ubuntu:22.04", init.Image)
	assert.Equal(t, taskHome, init.WorkingDir)

	require.Len(t, init.Command, 3)
	assert.Equal(t, "sh", init.Command[0])
	assert.Equal(t, "-ceu", init.Command[1])
	assert.Contains(t, init.Command[2], "mkdir -p")
	assert.Contains(t, init.Command[2], "$WORKSPACE_PATH")
	assert.Contains(t, init.Command[2], "$ARTIFACTS_PATH")
	assert.Contains(t, init.Command[2], "test -w")

	envMap := envVarMap(init.Env)
	assert.Equal(t, taskHome, envMap["TASK_HOME"])
	assert.Equal(t, workspacePath, envMap["WORKSPACE_PATH"])
	assert.Equal(t, artifactsPath, envMap["ARTIFACTS_PATH"])

	require.Len(t, init.VolumeMounts, 1)
	vm := init.VolumeMounts[0]
	assert.Equal(t, "workspace", vm.Name)
	assert.Equal(t, taskHome, vm.MountPath)
	assert.Empty(t, vm.SubPath)
	assert.False(t, vm.ReadOnly)

	require.NotNil(t, init.SecurityContext)
	require.NotNil(t, init.SecurityContext.RunAsNonRoot)
	assert.True(t, *init.SecurityContext.RunAsNonRoot)
	require.NotNil(t, init.SecurityContext.RunAsUser)
	assert.Equal(t, int64(1000), *init.SecurityContext.RunAsUser)
	require.NotNil(t, init.SecurityContext.RunAsGroup)
	assert.Equal(t, int64(1000), *init.SecurityContext.RunAsGroup)
	require.NotNil(t, init.SecurityContext.AllowPrivilegeEscalation)
	assert.False(t, *init.SecurityContext.AllowPrivilegeEscalation)
	require.NotNil(t, init.SecurityContext.Capabilities)
	assert.Contains(t, init.SecurityContext.Capabilities.Drop, v1.Capability("ALL"))
	require.NotNil(t, init.SecurityContext.SeccompProfile)
	assert.Equal(t, v1.SeccompProfileTypeRuntimeDefault, init.SecurityContext.SeccompProfile.Type)
}

func TestBuildPod_ReadOnlyAFSCPPlanDoesNotPrepareWritableDirs(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	req := validCreateRequest(CreateRequest{Image: "ubuntu:22.04"})
	req.resolvedMount.ReadOnly = true

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		req,
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	assert.Empty(t, pod.Spec.InitContainers, "read-only AFSCP mounts must not run init that writes or requires writable workspace paths")

	require.Len(t, pod.Spec.Containers, 1)
	require.Len(t, pod.Spec.Containers[0].VolumeMounts, 1)
	assert.True(t, pod.Spec.Containers[0].VolumeMounts[0].ReadOnly)

	require.Len(t, pod.Spec.Volumes, 1)
	require.NotNil(t, pod.Spec.Volumes[0].PersistentVolumeClaim)
	assert.True(t, pod.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly)
}

func TestBuildPod_CustomCommand(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	customCmd := []string{"python", "-m", "http.server", "8080"}
	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "python:3.12", Command: customCmd}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	assert.Equal(t, customCmd, pod.Spec.Containers[0].Command)
}

func TestBuildPod_DefaultTimeouts(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(DefaultIdleTimeout)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img"}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{
			Image:          "img",
			IdleTimeoutSec: customIdle, MaxLifetimeSec: customMax,
		}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1", env,
		validCreateRequest(CreateRequest{Image: "img"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	c := pod.Spec.Containers[0]
	envMap := make(map[string]string, len(c.Env))
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}

	assert.Len(t, c.Env, 5)
	assert.Equal(t, "/home/task-plan", envMap["TASK_HOME"])
	assert.Equal(t, "/home/task-plan", envMap["HOME"])
	assert.Equal(t, "/home/task-plan/workspace", envMap["WORKSPACE_PATH"])
	assert.Equal(t, "secret123", envMap["API_KEY"])
	assert.Equal(t, "postgres://localhost", envMap["DB_URL"])
}

func TestBuildPod_EmptyEnv(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	envMap := make(map[string]string, len(pod.Spec.Containers[0].Env))
	for _, e := range pod.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}
	assert.Len(t, pod.Spec.Containers[0].Env, 3)
	assert.Equal(t, "/home/task-plan", envMap["TASK_HOME"])
	assert.Equal(t, "/home/task-plan", envMap["HOME"])
	assert.Equal(t, "/home/task-plan/workspace", envMap["WORKSPACE_PATH"])
}

func TestBuildPod_ResourceRequestsOnly(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{
			Image:         "img",
			CPURequest:    "250m",
			MemoryRequest: "512Mi",
		}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{
			Image:       "img",
			CPULimit:    "2",
			MemoryLimit: "4Gi",
		}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{
			Image:         "img",
			CPURequest:    "100m",
			CPULimit:      "500m",
			MemoryRequest: "256Mi",
			MemoryLimit:   "1Gi",
		}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img"}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img", CPURequest: "500m"}),
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

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img", MemoryLimit: "2Gi"}),
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
	h := NewHandler(client, executor)

	now := time.Now().UTC()
	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	require.Len(t, pod.Spec.Volumes, 1)
	assert.Equal(t, workspacebinding.PVCName("ws-1", "proj-1", "wmb_demo"), pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
}

func TestBuildPod_AnnotationTimestamps(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 3, 1, 12, 30, 0, 0, time.UTC)
	expiresAt := time.Date(2025, 3, 2, 12, 30, 0, 0, time.UTC)

	pod, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img"}),
		now, expiresAt,
	)
	require.NoError(t, err)

	a := pod.Annotations
	assert.Equal(t, "2025-03-01T12:30:00Z", a["last_activity_at"])
	assert.Equal(t, "2025-03-02T12:30:00Z", a["expires_at"])
	assert.Equal(t, "ns_demo", a["mbos.io/afscp-namespace-id"])
	assert.Equal(t, "wmb_demo", a["mbos.io/afscp-mount-binding-id"])
	assert.Equal(t, "vol_demo", a["mbos.io/afscp-volume-id"])
	assert.Equal(t, "afscp/ns_demo/repos/repo_demo/payload", a["mbos.io/payload-volume-subdir"])
	assert.Equal(t, "/home/task-plan", a["mbos.io/mount-path"])
	assert.Equal(t, "false", a["mbos.io/read-only"])
}

func TestParseResourceRequirements_Invalid(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
	}{
		{"invalid cpu_request", validCreateRequest(CreateRequest{Image: "img", CPURequest: "not-a-number"})},
		{"invalid memory_request", validCreateRequest(CreateRequest{Image: "img", MemoryRequest: "xyz"})},
		{"invalid cpu_limit", validCreateRequest(CreateRequest{Image: "img", CPULimit: "1.2.3"})},
		{"invalid memory_limit", validCreateRequest(CreateRequest{Image: "img", MemoryLimit: "not-a-quantity"})},
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

	_, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "img", CPURequest: "invalid"}),
		now, now.Add(time.Hour),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cpu_request")
}

func TestBuildPod_RequiresAFSCPPlan(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	_, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		CreateRequest{Image: "img", WorkspaceBindingID: "wmb_demo"},
		now, now.Add(time.Hour),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AFSCP workload mount plan")
}

func TestBuildPod_InvalidAFSCPMountPath(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now().UTC()

	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "must be absolute",
			path:    "home/task-abc",
			wantErr: "AFSCP mount_path",
		},
		{
			name:    "must be clean",
			path:    "/home/task-abc/../other",
			wantErr: "AFSCP mount_path",
		},
		{
			name:    "must not be root",
			path:    "/",
			wantErr: "AFSCP mount_path",
		},
		{
			name:    "must not contain backslashes",
			path:    "/home\\task",
			wantErr: "AFSCP mount_path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validCreateRequest(CreateRequest{Image: "img"})
			req.resolvedMount.MountPath = tt.path
			_, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
				map[string]string{}, req, now, now.Add(time.Hour))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWorkloadPodSpecDrift_MissingWorkspaceInitContainer(t *testing.T) {
	h := newTestHandler(t)
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	desired, err := h.buildPod("ws-1", "proj-1", "wl-1", "workload-wl-1",
		map[string]string{},
		validCreateRequest(CreateRequest{Image: "ubuntu:22.04"}),
		now, now.Add(time.Hour),
	)
	require.NoError(t, err)

	existing := desired.DeepCopy()
	existing.Spec.InitContainers = nil

	drift := workloadPodSpecDrift(existing, desired)
	assert.Contains(t, drift, "workspace init")
}

// ---------------------------------------------------------------------------
// handleCreatePod – workspace binding usage
// ---------------------------------------------------------------------------

func TestHandleCreatePod_UsesBindingPVC(t *testing.T) {
	type capturedMount struct {
		claimName string
		mountPath string
		readOnly  bool
	}
	podSpec := make(chan capturedMount, 1)
	fakeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeTestBindingPVCIfRequested(w, r, "ws-abc", "proj-123", "wmb_demo") {
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/pods") {
			var pod v1.Pod
			if err := json.NewDecoder(r.Body).Decode(&pod); err == nil && len(pod.Spec.Volumes) > 0 && pod.Spec.Volumes[0].PersistentVolumeClaim != nil {
				podSpec <- capturedMount{
					claimName: pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName,
					mountPath: pod.Spec.Containers[0].VolumeMounts[0].MountPath,
					readOnly:  pod.Spec.Containers[0].VolumeMounts[0].ReadOnly && pod.Spec.Volumes[0].PersistentVolumeClaim.ReadOnly,
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(pod)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		})
	}))
	t.Cleanup(fakeAPI.Close)

	client := newTestK8sClient(t, fakeAPI.URL)
	executor := k8s.NewExecutor(client)
	h := NewHandler(client, executor)

	payload, _ := json.Marshal(validCreateRequest(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-abc", "proj-123", "wl-test")

	select {
	case got := <-podSpec:
		assert.Equal(t, workspacebinding.PVCName("ws-abc", "proj-123", "wmb_demo"), got.claimName)
		assert.Equal(t, "/home/task-plan", got.mountPath)
		assert.True(t, got.readOnly)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pod creation")
	}
}

func TestHandleCreatePod_InvalidResourceReturns400(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name    string
		req     CreateRequest
		wantMsg string
	}{
		{"invalid cpu_request", validCreateRequest(CreateRequest{Image: "img", CPURequest: "bad-value"}), "cpu_request"},
		{"invalid memory_request", validCreateRequest(CreateRequest{Image: "img", MemoryRequest: "xyz"}), "memory_request"},
		{"invalid cpu_limit", validCreateRequest(CreateRequest{Image: "img", CPULimit: "1.2.3"}), "cpu_limit"},
		{"invalid memory_limit", validCreateRequest(CreateRequest{Image: "img", MemoryLimit: "not-a-qty"}), "memory_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, _ := json.Marshal(tt.req)
			req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var body map[string]string
			decodeJSON(t, rec, &body)
			assert.Contains(t, body["error"], tt.wantMsg)
		})
	}
}

// ---------------------------------------------------------------------------
// Exec timeout capping logic
// ---------------------------------------------------------------------------

func TestExecTimeoutCapping(t *testing.T) {
	tests := []struct {
		name       string
		inputSec   int
		wantResult time.Duration
	}{
		{"zero uses default", 0, 30 * time.Second},
		{"positive uses value", 60, 60 * time.Second},
		{"exactly at cap", 300, 300 * time.Second},
		{"over cap is clamped", 301, maxExecTimeout},
		{"far over cap is clamped", 9999, maxExecTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := 30 * time.Second
			if tt.inputSec > 0 {
				timeout = time.Duration(tt.inputSec) * time.Second
			}
			if timeout > maxExecTimeout {
				timeout = maxExecTimeout
			}
			assert.Equal(t, tt.wantResult, timeout)
		})
	}
}

func TestMaxExecTimeoutConstant(t *testing.T) {
	assert.Equal(t, 300*time.Second, maxExecTimeout, "maxExecTimeout must be 300s per API contract")
}

// ---------------------------------------------------------------------------
// JSON field name contracts (AgentSmith API contract)
// ---------------------------------------------------------------------------

func TestPodStatus_JSONFieldNames(t *testing.T) {
	ps := PodStatus{
		PodName:        "workload-abc",
		Phase:          "Running",
		IP:             "10.0.0.1",
		StartedAt:      "2025-01-01T00:00:00Z",
		LastActivityAt: "2025-01-01T01:00:00Z",
		ExpiresAt:      "2025-01-02T00:00:00Z",
		Message:        "ok",
	}
	b, err := json.Marshal(ps)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))

	assert.Equal(t, "workload-abc", raw["pod_name"])
	assert.Equal(t, "Running", raw["phase"])
	assert.Equal(t, "10.0.0.1", raw["ip"])
	assert.Equal(t, "2025-01-01T00:00:00Z", raw["started_at"])
	assert.Equal(t, "2025-01-01T01:00:00Z", raw["last_activity_at"])
	assert.Equal(t, "2025-01-02T00:00:00Z", raw["expires_at"])
	assert.Equal(t, "ok", raw["message"])
}

func TestKeepaliveResponse_JSONFieldNames(t *testing.T) {
	r := KeepaliveResponse{ExpiresAt: "2025-01-02T00:00:00Z"}
	b, err := json.Marshal(r)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))

	assert.Equal(t, "2025-01-02T00:00:00Z", raw["expires_at"])
	assert.Len(t, raw, 1, "KeepaliveResponse must have exactly one field")
}

func TestDeleteResponse_JSONFieldNames(t *testing.T) {
	r := DeleteResponse{Message: "pod deleted"}
	b, err := json.Marshal(r)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))

	assert.Equal(t, "pod deleted", raw["message"])
	assert.Len(t, raw, 1, "DeleteResponse must have exactly one field")
}

func TestExecResponse_JSONFieldNames(t *testing.T) {
	r := ExecResponse{ExitCode: 1, Stdout: "out", Stderr: "err", DurationMs: 42}
	b, err := json.Marshal(r)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))

	assert.Equal(t, float64(1), raw["exit_code"])
	assert.Equal(t, "out", raw["stdout"])
	assert.Equal(t, "err", raw["stderr"])
	assert.Equal(t, float64(42), raw["duration_ms"])
}

func TestCreateRequest_JSONDeserialization(t *testing.T) {
	raw := `{
		"image": "ubuntu:22.04",
		"command": ["sh", "-c", "echo hi"],
		"env": {"FOO": "bar", "BAZ": "qux"},
		"workspace_binding_id": "wmb_demo",
		"cpu_request": "250m",
		"cpu_limit": "1",
		"memory_request": "256Mi",
		"memory_limit": "1Gi",
		"idle_timeout_sec": 600,
		"max_lifetime_sec": 7200
	}`

	var r CreateRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &r))

	assert.Equal(t, "ubuntu:22.04", r.Image)
	assert.Equal(t, []string{"sh", "-c", "echo hi"}, r.Command)
	assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux"}, r.Env)
	assert.Equal(t, "wmb_demo", r.WorkspaceBindingID)
	assert.Equal(t, "250m", r.CPURequest)
	assert.Equal(t, "1", r.CPULimit)
	assert.Equal(t, "256Mi", r.MemoryRequest)
	assert.Equal(t, "1Gi", r.MemoryLimit)
	assert.Equal(t, 600, r.IdleTimeoutSec)
	assert.Equal(t, 7200, r.MaxLifetimeSec)
}

func TestExecRequest_JSONDeserialization(t *testing.T) {
	raw := `{"cmd": ["echo", "hello"], "timeout_seconds": 120}`

	var r ExecRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &r))

	assert.Equal(t, []string{"echo", "hello"}, r.Cmd)
	assert.Equal(t, 120, r.TimeoutSeconds)
}

func TestExecRequest_CmdFieldRequired(t *testing.T) {
	raw := `{"timeout_seconds": 30}`

	var r ExecRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &r))
	assert.Nil(t, r.Cmd, "missing cmd field should decode as nil slice")
}

func TestPodStatus_OmitsZeroValues(t *testing.T) {
	// phase is the only non-omitempty field – must always appear
	ps := PodStatus{Phase: "offline"}
	b, err := json.Marshal(ps)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))

	assert.Equal(t, "offline", raw["phase"])
	for _, key := range []string{"pod_name", "ip", "started_at", "last_activity_at", "expires_at", "message"} {
		_, ok := raw[key]
		assert.False(t, ok, "zero-value field %q must be omitted from JSON", key)
	}
}
