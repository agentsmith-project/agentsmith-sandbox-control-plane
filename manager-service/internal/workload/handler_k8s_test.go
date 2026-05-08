package workload

// handler_k8s_test.go – tests that need a realistic K8s API fake (create, get, delete, patch).
// The podRegistry type implements http.Handler and simulates a minimal Kubernetes pod API,
// enabling end-to-end handler tests without any real cluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/workspacebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// podRegistry – minimal fake K8s pod API
// ---------------------------------------------------------------------------

// podRegistry simulates the Kubernetes pod API surface used by the handler:
// POST .../pods, GET .../pods/{name}, DELETE .../pods/{name}, PATCH .../pods/{name}.
type podRegistry struct {
	mu   sync.Mutex
	pods map[string]*v1.Pod
	// Optional: return 500 for GET/DELETE/PATCH for this pod name (for error-path tests).
	forceGetErrorFor    string
	forceDeleteErrorFor string
	forcePatchErrorFor  string
}

func newPodRegistry(initial ...*v1.Pod) *podRegistry {
	r := &podRegistry{pods: make(map[string]*v1.Pod)}
	for _, p := range initial {
		r.pods[p.Name] = p
	}
	return r
}

func (r *podRegistry) setForceGetErrorFor(name string)    { r.forceGetErrorFor = name }
func (r *podRegistry) setForceDeleteErrorFor(name string) { r.forceDeleteErrorFor = name }
func (r *podRegistry) setForcePatchErrorFor(name string)  { r.forcePatchErrorFor = name }

func (r *podRegistry) makeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func (r *podRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := req.URL.Path

	// POST .../pods – create
	if req.Method == http.MethodPost && strings.HasSuffix(path, "/pods") {
		r.mu.Lock()
		defer r.mu.Unlock()

		var pod v1.Pod
		if err := json.NewDecoder(req.Body).Decode(&pod); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, exists := r.pods[pod.Name]; exists {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(&metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonAlreadyExists,
				Code:     http.StatusConflict,
			})
			return
		}
		if pod.CreationTimestamp.IsZero() {
			pod.CreationTimestamp = metav1.Now()
		}
		r.pods[pod.Name] = &pod
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(&pod)
		return
	}

	// All other methods operate on a specific pod: .../pods/{name}[/subresource]
	podIdx := strings.LastIndex(path, "/pods/")
	if podIdx < 0 {
		r.writeNotFound(w)
		return
	}
	name := path[podIdx+len("/pods/"):]
	// Strip any trailing subresource path segment.
	if slash := strings.Index(name, "/"); slash >= 0 {
		name = name[:slash]
	}

	r.mu.Lock()
	pod := r.pods[name]
	r.mu.Unlock()

	// Error injection for handler error-path tests.
	switch req.Method {
	case http.MethodGet:
		if name == r.forceGetErrorFor {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 500, Message: "injected get error"})
			return
		}
	case http.MethodDelete:
		if name == r.forceDeleteErrorFor {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 500, Message: "injected delete error"})
			return
		}
	case http.MethodPatch:
		if name == r.forcePatchErrorFor {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 500, Message: "injected patch error"})
			return
		}
	}

	switch req.Method {
	case http.MethodGet:
		if pod == nil {
			r.writeNotFound(w)
			return
		}
		json.NewEncoder(w).Encode(pod)

	case http.MethodDelete:
		if pod == nil {
			r.writeNotFound(w)
			return
		}
		r.mu.Lock()
		delete(r.pods, name)
		r.mu.Unlock()
		json.NewEncoder(w).Encode(&metav1.Status{Status: "Success", Code: http.StatusOK})

	case http.MethodPatch:
		if pod == nil {
			r.writeNotFound(w)
			return
		}
		// Apply merge patch annotations (last_activity_at, expires_at).
		var patch struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.NewDecoder(req.Body).Decode(&patch); err == nil {
			r.mu.Lock()
			if pod.Annotations == nil {
				pod.Annotations = make(map[string]string)
			}
			for k, v := range patch.Metadata.Annotations {
				pod.Annotations[k] = v
			}
			r.mu.Unlock()
		}
		json.NewEncoder(w).Encode(pod)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *podRegistry) writeNotFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(&metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReasonNotFound,
		Code:     http.StatusNotFound,
	})
}

// newHandlerWithRegistry creates a Handler backed by the given podRegistry.
func newHandlerWithRegistry(t *testing.T, reg *podRegistry) *Handler {
	t.Helper()
	srv := reg.makeServer(t)
	client := newTestK8sClient(t, srv.URL)
	executor := k8s.NewExecutor(client)
	return NewHandler(client, executor)
}

func validCreateRequestK8s(req CreateRequest) CreateRequest {
	req.WorkspaceBindingID = "flib-demo"
	return req
}

// shortCtx returns a context that expires quickly so that WaitForPodReady
// returns promptly in tests that only care about the creation response.
func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// ---------------------------------------------------------------------------
// handleCreatePod – full lifecycle (201 success)
// ---------------------------------------------------------------------------

func TestHandleCreatePod_Returns201WithPodName(t *testing.T) {
	h := newHandlerWithRegistry(t, newPodRegistry())

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusCreated, rec.Code)

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "workload-wl-1", got.PodName)
	assert.NotEmpty(t, got.StartedAt)
	assert.NotEmpty(t, got.ExpiresAt)
}

func TestHandleCreatePod_ExpiresAtReflectsIdleTimeout(t *testing.T) {
	h := newHandlerWithRegistry(t, newPodRegistry())

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "img", IdleTimeoutSec: 600}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	before := time.Now().UTC()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")
	after := time.Now().UTC()

	require.Equal(t, http.StatusCreated, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	expiresAt, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)

	// expires_at must be approximately now+600s, never before the request started.
	assert.True(t, expiresAt.After(before.Add(590*time.Second)),
		"expires_at should be at least 590s after request start")
	assert.True(t, expiresAt.Before(after.Add(605*time.Second)),
		"expires_at should be no more than 605s after request end")
}

func TestHandleCreatePod_CustomCommandInPodSpec(t *testing.T) {
	var capturedCmd []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods") {
			var pod v1.Pod
			if err := json.NewDecoder(r.Body).Decode(&pod); err == nil && len(pod.Spec.Containers) > 0 {
				capturedCmd = pod.Spec.Containers[0].Command
			}
			pod.CreationTimestamp = metav1.Now()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(&pod)
			return
		}
		// All GETs return 404 so WaitForPodReady exits quickly.
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newTestK8sClient(t, srv.URL)
	executor := k8s.NewExecutor(client)
	h := NewHandler(client, executor)

	customCmd := []string{"python3", "-m", "http.server", "8080"}
	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "python:3.12", Command: customCmd}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, customCmd, capturedCmd)
}

func TestHandleCreatePod_RuntimeEnvAlwaysInjected(t *testing.T) {
	envCapture := make(chan []v1.EnvVar, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods") {
			var pod v1.Pod
			if err := json.NewDecoder(r.Body).Decode(&pod); err == nil && len(pod.Spec.Containers) > 0 {
				envCapture <- pod.Spec.Containers[0].Env
			}
			pod.CreationTimestamp = metav1.Now()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(&pod)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newTestK8sClient(t, srv.URL)
	executor := k8s.NewExecutor(client)
	h := NewHandler(client, executor)

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{
		Image:      "ubuntu:22.04",
		MountPath:  "/home/task-abc",
		SubPath:    "agent-tasks/task-abc",
		WorkingDir: "/home/task-abc/workspace",
		Env: map[string]string{
			"HOME":           "/tmp/legacy-home",
			"WORKSPACE_PATH": "/workspace/legacy",
			"MY_VAR":         "hello",
		},
	}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")
	require.Equal(t, http.StatusCreated, rec.Code)

	select {
	case envVars := <-envCapture:
		envMap := make(map[string]string, len(envVars))
		for _, e := range envVars {
			envMap[e.Name] = e.Value
		}
		assert.Equal(t, "/home/task-abc", envMap["TASK_HOME"], "TASK_HOME must always be injected")
		assert.Equal(t, "/home/task-abc", envMap["HOME"], "HOME must match TASK_HOME")
		assert.Equal(t, "/home/task-abc/workspace", envMap["WORKSPACE_PATH"], "WORKSPACE_PATH must match working_dir")
		assert.Equal(t, "hello", envMap["MY_VAR"], "user-provided env must be present")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pod creation")
	}
}

// ---------------------------------------------------------------------------
// handleCreatePod – pod already exists
// ---------------------------------------------------------------------------

func TestHandleCreatePod_AlreadyExists_Returns200(t *testing.T) {
	existing := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-1",
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1.PodSpec{
			InitContainers: []v1.Container{
				{
					Name:       "workspace-init",
					WorkingDir: "/workspace",
					Env: []v1.EnvVar{
						{Name: "ARTIFACTS_PATH", Value: "/workspace/.artifacts"},
						{Name: "TASK_HOME", Value: "/workspace"},
						{Name: "WORKSPACE_PATH", Value: "/workspace"},
					},
					VolumeMounts: []v1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
					},
				},
			},
			Containers: []v1.Container{
				{
					Name:       "main",
					WorkingDir: "/workspace",
					Env: []v1.EnvVar{
						{Name: "TASK_HOME", Value: "/workspace"},
						{Name: "HOME", Value: "/workspace"},
						{Name: "WORKSPACE_PATH", Value: "/workspace"},
					},
					VolumeMounts: []v1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "workspace",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: workspacebinding.PVCName("ws-1", "proj-1", "flib-demo"),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.0.0.5"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(existing))

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "workload-wl-1", got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "pod already exists", got.Message)
}

func TestHandleCreatePod_AlreadyExistsSpecDrift_Returns409(t *testing.T) {
	existing := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-1",
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:       "main",
					WorkingDir: "/workspace",
					Env: []v1.EnvVar{
						{Name: "TASK_HOME", Value: "/workspace"},
						{Name: "HOME", Value: "/workspace"},
						{Name: "WORKSPACE_PATH", Value: "/workspace"},
					},
					VolumeMounts: []v1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "workspace",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: workspacebinding.PVCName("ws-1", "proj-1", "flib-demo"),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.0.0.5"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(existing))

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{
		Image:      "ubuntu:22.04",
		MountPath:  "/home/task-abc",
		SubPath:    "agent-tasks/task-abc",
		WorkingDir: "/home/task-abc/workspace",
	}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "existing pod spec drift")
}

// ---------------------------------------------------------------------------
// handleGetPod – pod found / annotations
// ---------------------------------------------------------------------------

func TestHandleGetPod_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"},
		Status:     v1.PodStatus{Phase: v1.PodRunning},
	})
	reg.setForceGetErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleGetPod_RunningPod(t *testing.T) {
	createdAt := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-1",
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "192.168.1.10"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "workload-wl-1", got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "192.168.1.10", got.IP)

	// The handler formats CreationTimestamp with RFC3339, which preserves the local timezone
	// of the deserialized time.Time. Compare by parsing to avoid timezone representation drift.
	require.NotEmpty(t, got.StartedAt)
	gotTime, err := time.Parse(time.RFC3339, got.StartedAt)
	require.NoError(t, err)
	assert.True(t, gotTime.Equal(createdAt),
		"started_at %q should represent %v", got.StartedAt, createdAt)
}

func TestHandleGetPod_PendingPod(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-2",
			CreationTimestamp: metav1.Now(),
		},
		Status: v1.PodStatus{Phase: v1.PodPending},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "wl-2")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "Pending", got.Phase)
}

func TestHandleGetPod_AnnotationsPopulatedInResponse(t *testing.T) {
	lastActivityAt := "2025-06-01T10:00:00Z"
	expiresAt := "2025-06-01T10:30:00Z"

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-3",
			CreationTimestamp: metav1.Now(),
			Annotations: map[string]string{
				"last_activity_at": lastActivityAt,
				"expires_at":       expiresAt,
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "wl-3")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, lastActivityAt, got.LastActivityAt)
	assert.Equal(t, expiresAt, got.ExpiresAt)
}

func TestHandleGetPod_MissingAnnotationsOmittedFromResponse(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl-4",
			CreationTimestamp: metav1.Now(),
			// No last_activity_at or expires_at annotations.
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "wl-4")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Empty(t, got.LastActivityAt)
	assert.Empty(t, got.ExpiresAt)
}

// ---------------------------------------------------------------------------
// handleDeletePod – success
// ---------------------------------------------------------------------------

func TestHandleDeletePod_ExistingPod_Returns200(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got DeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "pod deleted", got.Message)
}

func TestHandleDeletePod_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"}})
	reg.setForceGetErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleDeletePod_DeletePodFails_Returns500(t *testing.T) {
	reg := newPodRegistry(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"}})
	reg.setForceDeleteErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "pod deletion failed")
}

// ---------------------------------------------------------------------------
// handleKeepalive – success / custom timeout / cap logic
// ---------------------------------------------------------------------------

func TestHandleKeepalive_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"}})
	reg.setForceGetErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleKeepalive_PatchFails_Returns500(t *testing.T) {
	now := time.Now().UTC()
	reg := newPodRegistry(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "workload-wl-1",
			Annotations: map[string]string{"workload/maxExpiresAt": now.Add(24 * time.Hour).Format(time.RFC3339)},
		},
	})
	reg.setForcePatchErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "keepalive")
}

func TestHandleKeepalive_ReturnsExpiresAt(t *testing.T) {
	now := time.Now().UTC()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-wl-1",
			Annotations: map[string]string{
				// maxExpiresAt far in the future so it doesn't interfere.
				"workload/maxExpiresAt": now.Add(24 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got KeepaliveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.NotEmpty(t, got.ExpiresAt)

	expiresAt, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)

	// With default idle timeout (30 min), expires_at ≈ now + 30min.
	expected := time.Now().UTC().Add(DefaultIdleTimeout)
	assert.WithinDuration(t, expected, expiresAt, 10*time.Second)
}

func TestHandleKeepalive_UsesCustomIdleTimeoutFromAnnotation(t *testing.T) {
	const customIdleSec = 600
	now := time.Now().UTC()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-wl-1",
			Annotations: map[string]string{
				"workload/idleTimeoutSec": strconv.Itoa(customIdleSec),
				"workload/maxExpiresAt":   now.Add(24 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	require.Equal(t, http.StatusOK, rec.Code)
	var got KeepaliveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	expiresAt, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)

	expected := time.Now().UTC().Add(customIdleSec * time.Second)
	assert.WithinDuration(t, expected, expiresAt, 10*time.Second)
}

func TestHandleKeepalive_CappedByMaxExpiresAt(t *testing.T) {
	// maxExpiresAt is only 1 minute from now – much less than the default 30-min idle timeout.
	maxExpires := time.Now().UTC().Add(1 * time.Minute)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-wl-1",
			Annotations: map[string]string{
				"workload/maxExpiresAt": maxExpires.Format(time.RFC3339),
				// No idleTimeoutSec – falls back to 30-min default which exceeds maxExpiresAt.
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	require.Equal(t, http.StatusOK, rec.Code)
	var got KeepaliveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	expiresAt, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)

	// expires_at must not exceed maxExpiresAt.
	assert.True(t, !expiresAt.After(maxExpires.Add(time.Second)),
		"expires_at %v must not exceed maxExpiresAt %v", expiresAt, maxExpires)
	// And it should be close to maxExpiresAt (within a few seconds).
	assert.WithinDuration(t, maxExpires, expiresAt, 5*time.Second)
}

func TestHandleKeepalive_NotCapWhenMaxExpiresAtIsFarFuture(t *testing.T) {
	now := time.Now().UTC()
	maxExpires := now.Add(48 * time.Hour) // far future – should not cap
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-wl-1",
			Annotations: map[string]string{
				"workload/maxExpiresAt": maxExpires.Format(time.RFC3339),
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	require.Equal(t, http.StatusOK, rec.Code)
	var got KeepaliveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))

	expiresAt, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)

	// Should be approximately now+30min (default idle), not capped.
	expected := now.Add(DefaultIdleTimeout)
	assert.WithinDuration(t, expected, expiresAt, 10*time.Second)
}

// ---------------------------------------------------------------------------
// handleExec – pod not found via registry / validation edge cases
// ---------------------------------------------------------------------------

func TestHandleExec_PodExistsReturnsError_Returns500(t *testing.T) {
	reg := newPodRegistry(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"}})
	reg.setForceGetErrorFor("workload-wl-1")
	h := newHandlerWithRegistry(t, reg)

	payload, _ := json.Marshal(ExecRequest{Cmd: []string{"echo", "hi"}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleExec_PodExistsButExecFails(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "workload-wl-1"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	payload, _ := json.Marshal(ExecRequest{Cmd: []string{"echo", "hello"}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "wl-1")

	// The pod exists so the handler must NOT return 404 (pod not found).
	// It will return either 200 (exec result returned even on SPDY error) or 500.
	// The key invariant: a valid pod does not produce a "pod not found" response.
	assert.NotEqual(t, http.StatusNotFound, rec.Code, "pod exists – 404 must not be returned")
}

// ---------------------------------------------------------------------------
// Full HTTP routing via RegisterRoutes
// ---------------------------------------------------------------------------

func TestServeHTTP_GetWorkload_RunningPod(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-my-agent",
			CreationTimestamp: metav1.Now(),
		},
		Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.10.10.10"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/workspaces/ws-1/projects/proj-1/workloads/my-agent",
		nil,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "workload-my-agent", got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "10.10.10.10", got.IP)
}

func TestServeHTTP_DeleteWorkload_Success(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "workload-agent-del"},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/v1/workspaces/ws-1/projects/proj-1/workloads/agent-del",
		nil,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got DeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "pod deleted", got.Message)
}

func TestServeHTTP_KeepaliveWorkload_Success(t *testing.T) {
	now := time.Now().UTC()
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workload-agent-ka",
			Annotations: map[string]string{
				"workload/maxExpiresAt": now.Add(24 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/workspaces/ws-1/projects/proj-1/workloads/agent-ka/keepalive",
		nil,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got KeepaliveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.NotEmpty(t, got.ExpiresAt)
}
