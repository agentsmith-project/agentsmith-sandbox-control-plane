package workload

// handler_k8s_test.go – tests that need a realistic K8s API fake (create, get, delete, patch).
// The podRegistry type implements http.Handler and simulates a minimal Kubernetes pod API,
// enabling end-to-end handler tests without any real cluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/afscp"
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
	mu         sync.Mutex
	pods       map[string]*v1.Pod
	events     *eventRecorder
	bindingPVC *v1.PersistentVolumeClaim
	bindingPV  *v1.PersistentVolume
	// Optional: return 500 for GET/DELETE/PATCH for this pod name (for error-path tests).
	forceGetErrorFor    string
	forceDeleteErrorFor string
	forcePatchErrorFor  string
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) append(event string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRecorder) snapshot() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type fakeMountLifecycleClient struct {
	events                  *eventRecorder
	planNamespaceID         string
	planMountBindingID      string
	planCorrelationID       string
	plan                    afscp.OrchestratorMountPlan
	planErr                 error
	heartbeatNamespaceID    string
	heartbeatMountBindingID string
	heartbeatCorrelationID  string
	heartbeatIdempotencyKey string
	heartbeatErr            error
	releaseNamespaceID      string
	releaseMountBindingID   string
	releaseCorrelationID    string
	releaseIdempotencyKey   string
	releaseErr              error
	statusNamespaceID       string
	statusMountBindingID    string
	statusValue             string
	statusReason            string
	statusCorrelationID     string
	statusIdempotencyKey    string
	statusErr               error
}

func (f *fakeMountLifecycleClient) GetOrchestratorMountPlan(_ context.Context, namespaceID, mountBindingID, correlationID string) (afscp.OrchestratorMountPlan, error) {
	f.planNamespaceID = namespaceID
	f.planMountBindingID = mountBindingID
	f.planCorrelationID = correlationID
	if f.planErr != nil {
		return afscp.OrchestratorMountPlan{}, f.planErr
	}
	if f.plan.MountBindingID != "" {
		return f.plan, nil
	}
	return validAFSCPMountPlan(mountBindingID), nil
}

func (f *fakeMountLifecycleClient) HeartbeatWorkloadMountBinding(_ context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error) {
	f.heartbeatNamespaceID = namespaceID
	f.heartbeatMountBindingID = mountBindingID
	f.heartbeatCorrelationID = correlationID
	f.heartbeatIdempotencyKey = idempotencyKey
	if f.heartbeatErr != nil {
		return afscp.OperationEnvelope{}, f.heartbeatErr
	}
	return afscp.OperationEnvelope{OperationID: "op_heartbeat", OperationState: "queued"}, nil
}

func (f *fakeMountLifecycleClient) ReleaseWorkloadMountBinding(_ context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error) {
	f.events.append("release")
	f.releaseNamespaceID = namespaceID
	f.releaseMountBindingID = mountBindingID
	f.releaseCorrelationID = correlationID
	f.releaseIdempotencyKey = idempotencyKey
	if f.releaseErr != nil {
		return afscp.OperationEnvelope{}, f.releaseErr
	}
	return afscp.OperationEnvelope{OperationID: "op_release", OperationState: "queued"}, nil
}

func (f *fakeMountLifecycleClient) UpdateWorkloadMountStatus(_ context.Context, namespaceID, mountBindingID, status, reason string, _ time.Time, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error) {
	f.events.append("status-" + status)
	f.statusNamespaceID = namespaceID
	f.statusMountBindingID = mountBindingID
	f.statusValue = status
	f.statusReason = reason
	f.statusCorrelationID = correlationID
	f.statusIdempotencyKey = idempotencyKey
	if f.statusErr != nil {
		return afscp.OperationEnvelope{}, f.statusErr
	}
	return afscp.OperationEnvelope{OperationID: "op_status", OperationState: "queued"}, nil
}

func validAFSCPMountPlan(bindingID string) afscp.OrchestratorMountPlan {
	return afscp.OrchestratorMountPlan{
		MountBindingID:      bindingID,
		VolumeID:            "vol_demo",
		PayloadVolumeSubdir: "afscp/ns_demo/repos/repo_demo/payload",
		MountPath:           "/home/task-plan",
		ReadOnly:            true,
		SecretRef:           afscp.SecretRef{Namespace: "afscp-mounts", Name: "juicefs-vol-demo"},
		SecurityPolicy: afscp.SecurityPolicy{
			RunAsNonRoot:             true,
			AllowPrivileged:          false,
			JVSControlOutsidePayload: true,
		},
	}
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

	if writeTestBindingResourceIfRequested(w, req, r.bindingPVC, r.bindingPV, "ws-1", "proj-1", "wmb_demo") {
		return
	}

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
			r.events.append("confirm-pod-gone")
			r.writeNotFound(w)
			return
		}
		json.NewEncoder(w).Encode(pod)

	case http.MethodDelete:
		if pod == nil {
			r.writeNotFound(w)
			return
		}
		r.events.append("delete-pod")
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

func newHandlerWithRegistryAndOptions(t *testing.T, reg *podRegistry, options Options) *Handler {
	t.Helper()
	srv := reg.makeServer(t)
	client := newTestK8sClient(t, srv.URL)
	executor := k8s.NewExecutor(client)
	return NewHandler(client, executor, options)
}

func validCreateRequestK8s(req CreateRequest) CreateRequest {
	req.WorkspaceBindingID = "wmb_demo"
	return req
}

func workloadPodWithMountAnnotations(name string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
			Annotations: map[string]string{
				"workload/idleTimeoutSec":        "1800",
				"workload/maxLifetimeSec":        "86400",
				"workload/maxExpiresAt":          time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				"mbos.io/afscp-namespace-id":     "ns_demo",
				"mbos.io/afscp-mount-binding-id": "wmb_demo",
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
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
		if writeTestBindingPVCIfRequested(w, r, "ws-1", "proj-1", "wmb_demo") {
			return
		}
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
		if writeTestBindingPVCIfRequested(w, r, "ws-1", "proj-1", "wmb_demo") {
			return
		}
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
		Image: "ubuntu:22.04",
		Env: map[string]string{
			"HOME":           "/tmp/caller-home",
			"WORKSPACE_PATH": "/workspace/caller",
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
		assert.Equal(t, "/home/task-plan", envMap["TASK_HOME"], "TASK_HOME must always come from AFSCP plan")
		assert.Equal(t, "/home/task-plan", envMap["HOME"], "HOME must match TASK_HOME")
		assert.Equal(t, "/home/task-plan/workspace", envMap["WORKSPACE_PATH"], "WORKSPACE_PATH must derive from AFSCP mount_path")
		assert.Equal(t, "hello", envMap["MY_VAR"], "user-provided env must be present")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pod creation")
	}
}

func TestHandleCreatePod_VerifiesBindingStillActiveBeforeCreate(t *testing.T) {
	lifecycle := &fakeMountLifecycleClient{}
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(), Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	req.Header.Set("X-Correlation-Id", "corr-create")
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "ns_demo", lifecycle.planNamespaceID)
	assert.Equal(t, "wmb_demo", lifecycle.planMountBindingID)
	assert.Equal(t, "corr-create", lifecycle.planCorrelationID)
}

func TestHandleCreatePod_RejectsStaleBindingBeforeCreate(t *testing.T) {
	reg := newPodRegistry()
	stalePlan := validAFSCPMountPlan("wmb_demo")
	stalePlan.VolumeID = "vol_changed"
	lifecycle := &fakeMountLifecycleClient{plan: stalePlan}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace binding is stale")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "stale AFSCP plan must fail before pod creation")
}

func TestHandleCreatePod_RejectsAFSCPSecretRefDriftBeforeCreate(t *testing.T) {
	reg := newPodRegistry()
	stalePlan := validAFSCPMountPlan("wmb_demo")
	stalePlan.SecretRef = afscp.SecretRef{Namespace: "afscp-mounts", Name: "juicefs-rotated"}
	lifecycle := &fakeMountLifecycleClient{plan: stalePlan}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace binding is stale")
	assert.Contains(t, rec.Body.String(), "secret_ref")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "secret_ref drift must fail before pod creation")
}

func TestHandleCreatePod_RejectsPVPlanDriftBeforeCreate(t *testing.T) {
	currentPlan := validAFSCPMountPlan("wmb_demo")
	currentPlan.PayloadVolumeSubdir = "afscp/ns_demo/repos/repo_current/payload"
	pvc := testBindingPVC("ws-1", "proj-1", "wmb_demo")
	pvc.Annotations["mbos.io/payload-volume-subdir"] = currentPlan.PayloadVolumeSubdir
	pv := testBindingPV("ws-1", "proj-1", "wmb_demo")
	pv.Spec.CSI.VolumeAttributes["subdir"] = "afscp/ns_demo/repos/repo_old/payload"

	reg := newPodRegistry()
	reg.bindingPVC = pvc
	reg.bindingPV = pv
	lifecycle := &fakeMountLifecycleClient{plan: currentPlan}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "persistent volume")
	assert.Contains(t, rec.Body.String(), "payload_volume_subdir")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "PV/plan drift must fail before pod creation")
}

func TestHandleCreatePod_FailsClosedWhenBindingActiveCheckUnavailable(t *testing.T) {
	reg := newPodRegistry()
	lifecycle := &fakeMountLifecycleClient{planErr: errors.New("afscp unavailable")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace binding active check failed")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "AFSCP active check failures must fail closed before pod creation")
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
			Containers: []v1.Container{
				{
					Name:       "main",
					WorkingDir: "/home/task-plan/workspace",
					Env: []v1.EnvVar{
						{Name: "TASK_HOME", Value: "/home/task-plan"},
						{Name: "HOME", Value: "/home/task-plan"},
						{Name: "WORKSPACE_PATH", Value: "/home/task-plan/workspace"},
					},
					VolumeMounts: []v1.VolumeMount{
						{Name: "workspace", MountPath: "/home/task-plan", ReadOnly: true},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "workspace",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: workspacebinding.PVCName("ws-1", "proj-1", "wmb_demo"),
							ReadOnly:  true,
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
							ClaimName: workspacebinding.PVCName("ws-1", "proj-1", "wmb_demo"),
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

func TestHandleDeletePodReleasesAFSCPMountAndMarksReleased(t *testing.T) {
	events := &eventRecorder{}
	lifecycle := &fakeMountLifecycleClient{events: events}
	reg := newPodRegistry(workloadPodWithMountAnnotations("workload-wl-1"))
	reg.events = events
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ns_demo", lifecycle.releaseNamespaceID)
	assert.Equal(t, "wmb_demo", lifecycle.releaseMountBindingID)
	assert.Equal(t, "corr-delete", lifecycle.releaseCorrelationID)
	assert.Contains(t, lifecycle.releaseIdempotencyKey, "release")
	assert.Equal(t, "ns_demo", lifecycle.statusNamespaceID)
	assert.Equal(t, "wmb_demo", lifecycle.statusMountBindingID)
	assert.Equal(t, "released", lifecycle.statusValue)
	assert.Equal(t, "workload pod deleted", lifecycle.statusReason)
	assert.Equal(t, "corr-delete", lifecycle.statusCorrelationID)
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())
}

func TestHandleDeletePod_AFSCPReleaseFailureKeepsPodForRetry(t *testing.T) {
	reg := newPodRegistry(workloadPodWithMountAnnotations("workload-wl-1"))
	lifecycle := &fakeMountLifecycleClient{releaseErr: errors.New("release failed")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "release failed")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.NotNil(t, reg.pods["workload-wl-1"], "pod annotations must remain available so DELETE can retry AFSCP release")
}

func TestHandleDeletePod_AFSCPStatusFailureHappensAfterPodGone(t *testing.T) {
	events := &eventRecorder{}
	reg := newPodRegistry(workloadPodWithMountAnnotations("workload-wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events, statusErr: errors.New("status failed")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "status failed")
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Nil(t, reg.pods["workload-wl-1"], "released status must only be attempted after the pod is gone")
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

func TestHandleDeletePod_DeletePodFailsDoesNotPatchReleasedAndKeepsPodForRetry(t *testing.T) {
	events := &eventRecorder{}
	reg := newPodRegistry(workloadPodWithMountAnnotations("workload-wl-1"))
	reg.events = events
	reg.setForceDeleteErrorFor("workload-wl-1")
	lifecycle := &fakeMountLifecycleClient{events: events}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "pod deletion failed")
	assert.Equal(t, []string{"release"}, events.snapshot())
	assert.Empty(t, lifecycle.statusValue, "terminal released status must not be written when pod deletion fails")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.NotNil(t, reg.pods["workload-wl-1"], "failed pod deletion must leave pod available for retry")
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

func TestHandleKeepaliveHeartbeatsAFSCPMount(t *testing.T) {
	lifecycle := &fakeMountLifecycleClient{}
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(workloadPodWithMountAnnotations("workload-wl-1")), Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-heartbeat")
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ns_demo", lifecycle.heartbeatNamespaceID)
	assert.Equal(t, "wmb_demo", lifecycle.heartbeatMountBindingID)
	assert.Equal(t, "corr-heartbeat", lifecycle.heartbeatCorrelationID)
	assert.Contains(t, lifecycle.heartbeatIdempotencyKey, "heartbeat")
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
