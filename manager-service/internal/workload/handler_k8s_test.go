package workload

// handler_k8s_test.go – tests that need a realistic K8s API fake (create, get, delete, patch).
// The podRegistry type implements http.Handler and simulates a minimal Kubernetes pod API,
// enabling end-to-end handler tests without any real cluster.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/afscp"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/k8s"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workloadfacts"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workspacebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ---------------------------------------------------------------------------
// podRegistry – minimal fake K8s pod API
// ---------------------------------------------------------------------------

// podRegistry simulates the Kubernetes pod API surface used by the handler:
// POST .../pods, GET .../pods/{name}, DELETE .../pods/{name}, PATCH .../pods/{name}.
type podRegistry struct {
	mu         sync.Mutex
	pods       map[string]*v1.Pod
	configMaps map[string]*v1.ConfigMap
	events     *eventRecorder
	bindingPVC *v1.PersistentVolumeClaim
	bindingPV  *v1.PersistentVolume
	// Optional: return this Kubernetes status for workspace binding PVC GETs.
	bindingPVCStatus *metav1.Status
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

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
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

type fakeStorageFlushBarrier struct {
	events    *eventRecorder
	podName   string
	mountPath string
	err       error
}

func (f *fakeStorageFlushBarrier) FlushWorkloadMount(_ context.Context, pod *v1.Pod, mountPath string) error {
	if pod != nil {
		f.podName = pod.Name
	}
	f.mountPath = mountPath
	f.events.append("flush-" + f.podName + ":" + mountPath)
	return f.err
}

type noopStorageFlushBarrier struct{}

func (noopStorageFlushBarrier) FlushWorkloadMount(context.Context, *v1.Pod, string) error {
	return nil
}

type alwaysFailSaveFactStore struct {
	*workloadfacts.MemoryStore
}

func (s *alwaysFailSaveFactStore) Save(context.Context, workloadfacts.Fact) error {
	return errors.New("fact save failed")
}

type bindingDeleteClientForWorkloadTest struct {
	pv   *v1.PersistentVolume
	pvc  *v1.PersistentVolumeClaim
	pods []v1.Pod
}

func (f *bindingDeleteClientForWorkloadTest) EnsurePersistentVolume(context.Context, *v1.PersistentVolume) error {
	return nil
}

func (f *bindingDeleteClientForWorkloadTest) GetPersistentVolume(context.Context, string) (*v1.PersistentVolume, error) {
	return f.pv, nil
}

func (f *bindingDeleteClientForWorkloadTest) DeletePersistentVolume(context.Context, string) error {
	f.pv = nil
	return nil
}

func (f *bindingDeleteClientForWorkloadTest) EnsurePersistentVolumeClaim(context.Context, string, *v1.PersistentVolumeClaim) error {
	return nil
}

func (f *bindingDeleteClientForWorkloadTest) GetPersistentVolumeClaim(context.Context, string, string) (*v1.PersistentVolumeClaim, error) {
	return f.pvc, nil
}

func (f *bindingDeleteClientForWorkloadTest) DeletePersistentVolumeClaim(context.Context, string, string) error {
	f.pvc = nil
	return nil
}

func (f *bindingDeleteClientForWorkloadTest) ListPods(context.Context, string, metav1.ListOptions) (*v1.PodList, error) {
	return &v1.PodList{Items: append([]v1.Pod(nil), f.pods...)}, nil
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
	r := &podRegistry{
		pods:       make(map[string]*v1.Pod),
		configMaps: make(map[string]*v1.ConfigMap),
	}
	for _, p := range initial {
		r.pods[p.Name] = p
	}
	return r
}

func (r *podRegistry) setForceGetErrorFor(name string)    { r.forceGetErrorFor = name }
func (r *podRegistry) setForceDeleteErrorFor(name string) { r.forceDeleteErrorFor = name }
func (r *podRegistry) setForcePatchErrorFor(name string)  { r.forcePatchErrorFor = name }
func (r *podRegistry) setBindingPVCStatus(status metav1.Status) {
	r.bindingPVCStatus = &status
}

func (r *podRegistry) makeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func (r *podRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := req.URL.Path

	if r.writeBindingResourceIfRequested(w, req) {
		return
	}
	if r.writeConfigMapIfRequested(w, req) {
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
		if testPodNameMatches(name, r.forceGetErrorFor) {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 500, Message: "injected get error"})
			return
		}
	case http.MethodDelete:
		if testPodNameMatches(name, r.forceDeleteErrorFor) {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 500, Message: "injected delete error"})
			return
		}
	case http.MethodPatch:
		if testPodNameMatches(name, r.forcePatchErrorFor) {
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

func (r *podRegistry) writeBindingResourceIfRequested(w http.ResponseWriter, req *http.Request) bool {
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/persistentvolumeclaims/") && r.bindingPVCStatus != nil {
		status := r.bindingPVCStatus.DeepCopy()
		code := int(status.Code)
		if code == 0 {
			code = http.StatusInternalServerError
			status.Code = int32(code)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(status)
		return true
	}
	return writeTestBindingResourceIfRequested(w, req, r.bindingPVC, r.bindingPV, "ws-1", "proj-1", "wmb_demo")
}

func (r *podRegistry) writeConfigMapIfRequested(w http.ResponseWriter, req *http.Request) bool {
	path := req.URL.Path
	idx := strings.LastIndex(path, "/configmaps")
	if idx < 0 {
		return false
	}

	if req.Method == http.MethodPost && strings.HasSuffix(path, "/configmaps") {
		var cm v1.ConfigMap
		if err := json.NewDecoder(req.Body).Decode(&cm); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, exists := r.configMaps[cm.Name]; exists {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(&metav1.Status{Reason: metav1.StatusReasonAlreadyExists, Code: http.StatusConflict})
			return true
		}
		cm.ResourceVersion = "1"
		r.configMaps[cm.Name] = cm.DeepCopy()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(&cm)
		return true
	}

	if req.Method == http.MethodGet && strings.HasSuffix(path, "/configmaps") {
		selector, err := labels.Parse(req.URL.Query().Get("labelSelector"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		list := &v1.ConfigMapList{}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, cm := range r.configMaps {
			if selector.Empty() || selector.Matches(labels.Set(cm.Labels)) {
				list.Items = append(list.Items, *cm.DeepCopy())
			}
		}
		_ = json.NewEncoder(w).Encode(list)
		return true
	}

	name := strings.TrimPrefix(path[idx+len("/configmaps"):], "/")
	if name == "" || strings.Contains(name, "/") {
		r.writeNotFound(w)
		return true
	}

	r.mu.Lock()
	cm := r.configMaps[name]
	r.mu.Unlock()

	switch req.Method {
	case http.MethodGet:
		if cm == nil {
			r.writeNotFound(w)
			return true
		}
		_ = json.NewEncoder(w).Encode(cm)
	case http.MethodPut:
		if cm == nil {
			r.writeNotFound(w)
			return true
		}
		var updated v1.ConfigMap
		if err := json.NewDecoder(req.Body).Decode(&updated); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		updated.ResourceVersion = "2"
		r.mu.Lock()
		r.configMaps[name] = updated.DeepCopy()
		r.mu.Unlock()
		_ = json.NewEncoder(w).Encode(&updated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	return true
}

func testPodNameMatches(got, configured string) bool {
	return got == configured
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
	if options.AFSCPClient != nil && options.StorageFlushBarrier == nil {
		options.StorageFlushBarrier = noopStorageFlushBarrier{}
	}
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
				"mbos.io/mount-path":             "/home/task-plan",
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
}

func workloadPodWithScope(name, workspaceID, projectID, workloadID string) *v1.Pod {
	pod := workloadPodWithMountAnnotations(name)
	pod.Labels = map[string]string{
		"app":          WorkloadLabel,
		"workspace_id": workloadfacts.LabelValue(workspaceID),
		"project_id":   workloadfacts.LabelValue(projectID),
		"workload_id":  workloadfacts.LabelValue(workloadID),
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["mbos.io/workspace-id"] = workspaceID
	pod.Annotations["mbos.io/project-id"] = projectID
	pod.Annotations["mbos.io/workload-id"] = workloadID
	return pod
}

func defaultScopedPodName(workloadID string) string {
	return PodName("ws-1", "proj-1", workloadID)
}

func defaultScopedPod(workloadID string) *v1.Pod {
	return workloadPodWithScope(defaultScopedPodName(workloadID), "ws-1", "proj-1", workloadID)
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
	assert.Empty(t, rec.Header().Get("Retry-After"))

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, defaultScopedPodName("wl-1"), got.PodName)
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
	h := NewHandler(client, executor, Options{WorkloadFactStore: workloadfacts.NewMemoryStore()})

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
	h := NewHandler(client, executor, Options{WorkloadFactStore: workloadfacts.NewMemoryStore()})

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

func TestHandleCreatePod_ReturnsNotReadyWhenBindingPVCNotFoundBeforeCreate(t *testing.T) {
	reg := newPodRegistry()
	reg.setBindingPVCStatus(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReasonNotFound,
		Code:     http.StatusNotFound,
		Message:  "persistentvolumeclaims not found",
	})
	h := newHandlerWithRegistry(t, reg)

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, workspaceMountRetryAfter, rec.Header().Get("Retry-After"))
	body := decodeError(t, rec)
	assert.Equal(t, "not_ready", body.Error.Code)
	assert.Contains(t, body.Error.Message, "workspace binding is not ready")
	assert.Contains(t, body.Error.Message, "not visible yet")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "transient PVC NotFound must fail before pod creation")
}

func TestHandleCreatePod_FailsFastWhenBindingPVCGetReturnsNonReadinessError(t *testing.T) {
	tests := []struct {
		name   string
		status metav1.Status
	}{
		{
			name: "forbidden",
			status: metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonForbidden,
				Code:     http.StatusForbidden,
				Message:  "rbac denied",
			},
		},
		{
			name: "generic",
			status: metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonInternalError,
				Code:     http.StatusInternalServerError,
				Message:  "apiserver unavailable",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newPodRegistry()
			reg.setBindingPVCStatus(tt.status)
			h := newHandlerWithRegistry(t, reg)

			payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
			req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

			require.Equal(t, http.StatusConflict, rec.Code)
			assert.Empty(t, rec.Header().Get("Retry-After"))
			body := decodeError(t, rec)
			assert.Equal(t, "conflict", body.Error.Code)
			assert.Contains(t, body.Error.Message, "re-ensure workspace binding")

			reg.mu.Lock()
			defer reg.mu.Unlock()
			assert.Empty(t, reg.pods, "PVC get errors outside readiness must fail before pod creation")
		})
	}
}

func TestHandleCreatePod_RejectsUnboundPVCBeforeCreate(t *testing.T) {
	pvc := testBindingPVC("ws-1", "proj-1", "wmb_demo")
	pvc.Status.Phase = v1.ClaimPending
	reg := newPodRegistry()
	reg.bindingPVC = pvc
	h := newHandlerWithRegistry(t, reg)

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := decodeError(t, rec)
	assert.Equal(t, "not_ready", body.Error.Code)
	assert.Contains(t, body.Error.Message, "workspace binding is not ready")
	assert.Contains(t, body.Error.Message, "Pending")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "unbound PVC must fail before pod creation")
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
	pv.Spec.MountOptions = []string{"subdir=afscp/ns_demo/repos/repo_old/payload"}

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
	lifecycle := &fakeMountLifecycleClient{planErr: errors.New("afscp unavailable token=raw-secret password=p@ss")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	req.Header.Set("X-Request-Id", "req-create")
	rec := httptest.NewRecorder()
	logs := captureStandardLog(t)
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace binding active check failed")
	assert.NotContains(t, rec.Body.String(), "raw-secret")

	logOutput := logs.String()
	for _, token := range []string{"workspace binding active check failed", "workspace=ws-1", "project=proj-1", "workload=wl-1", "mount_binding_id=wmb_demo", "request_id=req-create", "[REDACTED]"} {
		assert.Contains(t, logOutput, token)
	}
	assert.NotContains(t, logOutput, "raw-secret")
	assert.NotContains(t, logOutput, "p@ss")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.pods, "AFSCP active check failures must fail closed before pod creation")
}

func TestHandleCreatePod_FactSaveFailureDoesNotAllowBindingDeleteToMissLivePod(t *testing.T) {
	reg := newPodRegistry()
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		WorkloadFactStore: &alwaysFailSaveFactStore{MemoryStore: workloadfacts.NewMemoryStore()},
	})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequestWithContext(shortCtx(t), http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	reg.mu.Lock()
	livePods := make([]v1.Pod, 0, len(reg.pods))
	for _, pod := range reg.pods {
		livePods = append(livePods, *pod.DeepCopy())
	}
	reg.mu.Unlock()

	bindingClient := &bindingDeleteClientForWorkloadTest{
		pv:   &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:  &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		pods: livePods,
	}
	bindingHandler := workspacebinding.NewHandler(bindingClient, workspacebinding.Options{
		Namespace:     "test-ns",
		WorkloadFacts: workloadfacts.NewMemoryStore(),
	})
	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws-1/projects/proj-1/workspace-bindings/wmb_demo", nil)
	deleteRec := httptest.NewRecorder()
	bindingHandler.ServeHTTP(deleteRec, deleteReq)

	if len(livePods) > 0 && deleteRec.Code == http.StatusOK {
		t.Fatalf("binding delete missed live pod after create fact save failure; body=%s", deleteRec.Body.String())
	}
	if len(livePods) > 0 && (bindingClient.pv == nil || bindingClient.pvc == nil) {
		t.Fatalf("binding delete must not remove PV/PVC while an untracked live pod exists")
	}
}

func TestHandleCreatePodSlowReadyReturnsBeforeWriteTimeout(t *testing.T) {
	var podReadyPolls int
	facts := workloadfacts.NewMemoryStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if writeTestBindingResourceIfRequested(w, r, nil, nil, "ws-1", "proj-1", "wmb_demo") {
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods") {
			var pod v1.Pod
			require.NoError(t, json.NewDecoder(r.Body).Decode(&pod))
			pod.CreationTimestamp = metav1.Now()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&pod)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods/") {
			podReadyPolls++
			time.Sleep(650 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: defaultScopedPodName("wl-1")},
				Status:     v1.PodStatus{Phase: v1.PodPending},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(&metav1.Status{Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newTestK8sClient(t, srv.URL)
	h := NewHandler(client, k8s.NewExecutor(client), Options{WorkloadFactStore: facts})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	start := time.Now()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")
	elapsed := time.Since(start)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Less(t, elapsed, 250*time.Millisecond, "PUT ensure must not wait for pod readiness polling")
	assert.Zero(t, podReadyPolls, "Ready must be observed by GET polling, not PUT ensure")
}

func TestCreateResponseContainsWorkloadIDCorrelationIDStatus(t *testing.T) {
	facts := workloadfacts.NewMemoryStore()
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(), Options{WorkloadFactStore: facts})

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	req.Header.Set("X-Correlation-Id", "corr-create")
	rec := httptest.NewRecorder()

	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "wl-1", got.WorkloadID)
	assert.Equal(t, defaultScopedPodName("wl-1"), got.PodName)
	assert.Equal(t, "corr-create", got.CorrelationID)
	assert.Equal(t, "pending", got.Status)
}

// ---------------------------------------------------------------------------
// handleCreatePod – pod already exists
// ---------------------------------------------------------------------------

func TestHandleCreatePod_AlreadyExists_Returns200(t *testing.T) {
	existing := workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1")
	existing.CreationTimestamp = metav1.Now()
	expectedImage := "ubuntu:22.04"
	expectedImageID := "docker-pullable://example.invalid/runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	existing.Spec = v1.PodSpec{
		Containers: []v1.Container{
			{
				Name:       "main",
				Image:      expectedImage,
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
	}
	existing.Status = v1.PodStatus{
		Phase: v1.PodRunning,
		PodIP: "10.0.0.5",
		ContainerStatuses: []v1.ContainerStatus{
			{
				Name:    "main",
				Image:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				ImageID: expectedImageID,
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(existing))

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: expectedImage}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, defaultScopedPodName("wl-1"), got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, expectedImage, got.Image)
	assert.Equal(t, expectedImage, got.ImageRef)
	assert.Equal(t, expectedImageID, got.ImageID)
	assert.Equal(t, "pod already exists", got.Message)
}

func TestHandleCreatePod_AlreadyExistsSpecDrift_Returns409(t *testing.T) {
	existing := workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1")
	existing.CreationTimestamp = metav1.Now()
	existing.Spec = v1.PodSpec{
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
	}
	existing.Status = v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.0.0.5"}
	h := newHandlerWithRegistry(t, newPodRegistry(existing))

	payload, _ := json.Marshal(validCreateRequestK8s(CreateRequest{Image: "ubuntu:22.04"}))
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleCreatePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusConflict, rec.Code)
	body := decodeError(t, rec)
	assert.Contains(t, body.Error.Message, "existing pod spec drift")
}

// ---------------------------------------------------------------------------
// handleGetPod – pod found / annotations
// ---------------------------------------------------------------------------

func TestWorkloadRoutesDoNotOperateOnSameWorkloadIDAcrossDifferentScope(t *testing.T) {
	const workloadID = "shared-wl"
	foreignPod := workloadPodWithScope("workload-"+workloadID, "ws-a", "proj-a", workloadID)
	for _, tt := range []struct {
		name       string
		method     string
		pathSuffix string
		body       string
		wantStatus int
	}{
		{
			name:       "get returns offline instead of foreign pod status",
			method:     http.MethodGet,
			pathSuffix: "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "keepalive cannot patch foreign pod",
			method:     http.MethodPost,
			pathSuffix: "/keepalive",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "exec cannot target foreign pod",
			method:     http.MethodPost,
			pathSuffix: "/exec",
			body:       `{"cmd":["echo","hi"]}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "delete cannot release or delete foreign pod",
			method:     http.MethodDelete,
			pathSuffix: "",
			wantStatus: http.StatusConflict,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := newPodRegistry(foreignPod.DeepCopy())
			h := newHandlerWithRegistry(t, reg)
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)

			req := httptest.NewRequest(
				tt.method,
				"/v1/workspaces/ws-b/projects/proj-b/workloads/"+workloadID+tt.pathSuffix,
				strings.NewReader(tt.body),
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
			if tt.method == http.MethodGet {
				var got PodStatus
				require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
				assert.Equal(t, "offline", got.Phase)
				assert.Empty(t, got.PodName)
			}
			reg.mu.Lock()
			defer reg.mu.Unlock()
			if _, ok := reg.pods[foreignPod.Name]; !ok {
				t.Fatalf("foreign scoped pod %s must not be mutated or deleted", foreignPod.Name)
			}
		})
	}
}

func TestWorkloadRoutesFailClosedWhenPodNameMatchesButScopeMetadataDrifts(t *testing.T) {
	const workloadID = "scope-drift"
	requestedName := workloadfacts.ObjectName("workload", "ws-b", "proj-b", workloadID)
	driftedPod := workloadPodWithScope(requestedName, "ws-a", "proj-a", workloadID)
	for _, tt := range []struct {
		name       string
		method     string
		pathSuffix string
		body       string
	}{
		{
			name:       "get",
			method:     http.MethodGet,
			pathSuffix: "",
		},
		{
			name:       "keepalive",
			method:     http.MethodPost,
			pathSuffix: "/keepalive",
		},
		{
			name:       "exec",
			method:     http.MethodPost,
			pathSuffix: "/exec",
			body:       `{"cmd":["echo","hi"]}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := newPodRegistry(driftedPod.DeepCopy())
			h := newHandlerWithRegistry(t, reg)
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)

			req := httptest.NewRequest(
				tt.method,
				"/v1/workspaces/ws-b/projects/proj-b/workloads/"+workloadID+tt.pathSuffix,
				strings.NewReader(tt.body),
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			body := decodeError(t, rec)
			assert.Equal(t, "conflict", body.Error.Code)
			assert.Contains(t, body.Error.Message, "scope")
		})
	}
}

func TestHandleGetPod_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1"))
	reg.setForceGetErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleGetPod_RunningPod(t *testing.T) {
	createdAt := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	expectedImage := "kind-registry:5000/mbos/agentsmith-managed-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expectedImageID := "docker-pullable://kind-registry:5000/mbos/agentsmith-managed-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pod := workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1")
	pod.CreationTimestamp = metav1.NewTime(createdAt)
	pod.Spec.Containers = []v1.Container{{Name: "main", Image: expectedImage}}
	pod.Status = v1.PodStatus{
		Phase: v1.PodRunning,
		PodIP: "192.168.1.10",
		ContainerStatuses: []v1.ContainerStatus{
			{
				Name:    "main",
				Image:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				ImageID: expectedImageID,
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)

	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, defaultScopedPodName("wl-1"), got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "192.168.1.10", got.IP)
	assert.Equal(t, expectedImage, got.Image)
	assert.Equal(t, expectedImage, got.ImageRef)
	assert.Equal(t, expectedImageID, got.ImageID)

	// The handler formats CreationTimestamp with RFC3339, which preserves the local timezone
	// of the deserialized time.Time. Compare by parsing to avoid timezone representation drift.
	require.NotEmpty(t, got.StartedAt)
	gotTime, err := time.Parse(time.RFC3339, got.StartedAt)
	require.NoError(t, err)
	assert.True(t, gotTime.Equal(createdAt),
		"started_at %q should represent %v", got.StartedAt, createdAt)
}

func TestHandleGetPod_DoesNotPromoteBareContainerStatusImageToImageID(t *testing.T) {
	expectedImage := "kind-registry:5000/mbos/agentsmith-managed-runner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pod := workloadPodWithScope(defaultScopedPodName("wl-image"), "ws-1", "proj-1", "wl-image")
	pod.CreationTimestamp = metav1.Now()
	pod.Spec.Containers = []v1.Container{{Name: "main", Image: expectedImage}}
	pod.Status = v1.PodStatus{
		Phase: v1.PodRunning,
		ContainerStatuses: []v1.ContainerStatus{
			{
				Name:  "main",
				Image: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-image")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, expectedImage, got.Image)
	assert.Equal(t, expectedImage, got.ImageRef)
	assert.Empty(t, got.ImageID)
}

func TestHandleGetPod_PendingPod(t *testing.T) {
	pod := workloadPodWithScope(defaultScopedPodName("wl-2"), "ws-1", "proj-1", "wl-2")
	pod.CreationTimestamp = metav1.Now()
	pod.Status = v1.PodStatus{Phase: v1.PodPending}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-2")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "Pending", got.Phase)
}

func TestHandleGetPod_AnnotationsPopulatedInResponse(t *testing.T) {
	lastActivityAt := "2025-06-01T10:00:00Z"
	expiresAt := "2025-06-01T10:30:00Z"

	pod := workloadPodWithScope(defaultScopedPodName("wl-3"), "ws-1", "proj-1", "wl-3")
	pod.CreationTimestamp = metav1.Now()
	pod.Annotations["last_activity_at"] = lastActivityAt
	pod.Annotations["expires_at"] = expiresAt
	pod.Status = v1.PodStatus{Phase: v1.PodRunning}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-3")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got PodStatus
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, lastActivityAt, got.LastActivityAt)
	assert.Equal(t, expiresAt, got.ExpiresAt)
}

func TestHandleGetPod_MissingAnnotationsOmittedFromResponse(t *testing.T) {
	pod := workloadPodWithScope(defaultScopedPodName("wl-4"), "ws-1", "proj-1", "wl-4")
	pod.CreationTimestamp = metav1.Now()
	pod.Status = v1.PodStatus{Phase: v1.PodRunning}
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.handleGetPod(rec, req, "ws-1", "proj-1", "wl-4")

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
	h := newHandlerWithRegistry(t, newPodRegistry(defaultScopedPod("wl-1")))

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	var got DeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "pod deleted", got.Message)
}

func TestHandleDeletePod_WithNoCustomFlushBarrierReleasesDeletesAndMarksReleased(t *testing.T) {
	events := &eventRecorder{}
	lifecycle := &fakeMountLifecycleClient{events: events}
	reg := newPodRegistry(defaultScopedPod("wl-1"))
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
	gotEvents := events.snapshot()
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, gotEvents)
	assert.NotContains(t, gotEvents, "flush-"+defaultScopedPodName("wl-1")+":/home/task-plan", "this fixture uses the no-op storage flush barrier injected by the test helper")
}

func TestHandleDeletePodFlushesAFSCPMountBeforeDeletingPod(t *testing.T) {
	events := &eventRecorder{}
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events}
	podName := defaultScopedPodName("wl-1")
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle, StorageFlushBarrier: flush})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, podName, flush.podName)
	assert.Equal(t, "/home/task-plan", flush.mountPath)
	assert.Equal(t, []string{"flush-" + podName + ":/home/task-plan", "release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())
}

func TestHandleDeletePod_PendingPodWithoutStartedMainSkipsFlushAndReleases(t *testing.T) {
	events := &eventRecorder{}
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events, err: errors.New("pending pod cannot exec")}
	podName := defaultScopedPodName("wl-1")
	pod := defaultScopedPod("wl-1")
	pod.Status = v1.PodStatus{Phase: v1.PodPending}
	reg := newPodRegistry(pod)
	reg.events = events
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle, StorageFlushBarrier: flush})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, flush.podName, "pod that never started cannot have runtime writes to flush")
	assert.Equal(t, "wmb_demo", lifecycle.releaseMountBindingID)
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())

	reg.mu.Lock()
	assert.Nil(t, reg.pods[podName], "pending pod should not be retained after delete convergence")
	reg.mu.Unlock()
}

func TestHandleDeletePod_ReleaseDoneFactFlushesBeforeDeletingPod(t *testing.T) {
	events := &eventRecorder{}
	facts := workloadfacts.NewMemoryStore()
	podName := defaultScopedPodName("wl-1")
	require.NoError(t, facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws-1",
		ProjectID:          "proj-1",
		WorkloadID:         "wl-1",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            podName,
		PodUID:             "uid-1",
		ReleaseDone:        true,
		PodDeleted:         false,
		TerminalStatusDone: false,
		WorkspaceBindingID: "wmb_demo",
	}))
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		AFSCPClient:         lifecycle,
		StorageFlushBarrier: flush,
		WorkloadFactStore:   facts,
	})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, podName, flush.podName)
	assert.Equal(t, "/home/task-plan", flush.mountPath)
	assert.Empty(t, lifecycle.releaseMountBindingID, "retry with durable ReleaseDone must not re-release AFSCP mount")
	assert.Equal(t, []string{"flush-" + podName + ":/home/task-plan", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())
	got, err := facts.Get(context.Background(), workloadfacts.Key{WorkspaceID: "ws-1", ProjectID: "proj-1", WorkloadID: "wl-1"})
	require.NoError(t, err)
	assert.True(t, got.Terminal())
}

func TestHandleDeletePod_ReleaseDoneFactFlushFailureKeepsPodForRetry(t *testing.T) {
	events := &eventRecorder{}
	facts := workloadfacts.NewMemoryStore()
	podName := defaultScopedPodName("wl-1")
	require.NoError(t, facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws-1",
		ProjectID:          "proj-1",
		WorkloadID:         "wl-1",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            podName,
		PodUID:             "uid-1",
		ReleaseDone:        true,
		PodDeleted:         false,
		TerminalStatusDone: false,
		WorkspaceBindingID: "wmb_demo",
	}))
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events, err: errors.New("sync failed")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		AFSCPClient:         lifecycle,
		StorageFlushBarrier: flush,
		WorkloadFactStore:   facts,
	})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "storage flush barrier failed")
	assert.Equal(t, []string{"flush-" + podName + ":/home/task-plan"}, events.snapshot())
	assert.Empty(t, lifecycle.releaseMountBindingID, "retry with durable ReleaseDone must not re-release AFSCP mount")
	assert.Empty(t, lifecycle.statusValue, "terminal released status must not be written when storage flush fails")

	reg.mu.Lock()
	assert.NotNil(t, reg.pods[podName], "failed storage flush must leave pod available for retry")
	reg.mu.Unlock()

	got, err := facts.Get(context.Background(), workloadfacts.Key{WorkspaceID: "ws-1", ProjectID: "proj-1", WorkloadID: "wl-1"})
	require.NoError(t, err)
	assert.True(t, got.ReleaseDone)
	assert.False(t, got.PodDeleted)
	assert.False(t, got.TerminalStatusDone)
}

func TestHandleDeletePod_MissingPodWithDurableFactReleasesWithoutFlush(t *testing.T) {
	events := &eventRecorder{}
	facts := workloadfacts.NewMemoryStore()
	podName := defaultScopedPodName("wl-1")
	require.NoError(t, facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws-1",
		ProjectID:          "proj-1",
		WorkloadID:         "wl-1",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            podName,
		PodUID:             "uid-1",
		ReleaseDone:        false,
		PodDeleted:         false,
		TerminalStatusDone: false,
		WorkspaceBindingID: "wmb_demo",
	}))
	reg := newPodRegistry()
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		AFSCPClient:         lifecycle,
		StorageFlushBarrier: flush,
		WorkloadFactStore:   facts,
	})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, flush.podName, "missing pod cannot be flushed")
	assert.Equal(t, "wmb_demo", lifecycle.releaseMountBindingID)
	assert.Equal(t, []string{"confirm-pod-gone", "release", "status-released"}, events.snapshot())
	got, err := facts.Get(context.Background(), workloadfacts.Key{WorkspaceID: "ws-1", ProjectID: "proj-1", WorkloadID: "wl-1"})
	require.NoError(t, err)
	assert.True(t, got.Terminal())
}

func TestHandleDeletePod_FlushFailureKeepsPodForRetry(t *testing.T) {
	events := &eventRecorder{}
	podName := defaultScopedPodName("wl-1")
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events}
	flush := &fakeStorageFlushBarrier{events: events, err: errors.New("sync failed")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle, StorageFlushBarrier: flush})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "storage flush barrier failed")
	assert.Equal(t, []string{"flush-" + podName + ":/home/task-plan"}, events.snapshot())
	assert.Empty(t, lifecycle.releaseMountBindingID, "AFSCP release must not run when storage flush fails")
	assert.Empty(t, lifecycle.statusValue, "terminal released status must not be written when storage flush fails")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.NotNil(t, reg.pods[podName], "failed storage flush must leave pod available for retry")
}

func TestHandleDeletePod_AFSCPReleaseFailureKeepsPodForRetry(t *testing.T) {
	events := &eventRecorder{}
	podName := defaultScopedPodName("wl-1")
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events, releaseErr: errors.New("release failed token=raw-secret password=p@ss")}
	flush := &fakeStorageFlushBarrier{events: events}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle, StorageFlushBarrier: flush})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	req.Header.Set("X-Request-Id", "req-delete")
	rec := httptest.NewRecorder()
	logs := captureStandardLog(t)
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "release failed")
	assert.NotContains(t, rec.Body.String(), "raw-secret")
	assert.Equal(t, []string{"flush-" + podName + ":/home/task-plan", "release"}, events.snapshot())
	assert.Empty(t, lifecycle.statusValue, "terminal released status must not be written when AFSCP release fails")

	logOutput := logs.String()
	for _, token := range []string{"AFSCP workload mount release failed", "workspace=ws-1", "project=proj-1", "workload=wl-1", "pod=" + podName, "mount_binding_id=wmb_demo", "request_id=req-delete", "correlation_id=corr-delete", "[REDACTED]"} {
		assert.Contains(t, logOutput, token)
	}
	assert.NotContains(t, logOutput, "raw-secret")
	assert.NotContains(t, logOutput, "p@ss")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.NotNil(t, reg.pods[podName], "pod annotations must remain available so DELETE can retry AFSCP release")
}

func TestHandleDeletePod_AFSCPStatusFailureHappensAfterPodGone(t *testing.T) {
	events := &eventRecorder{}
	podName := defaultScopedPodName("wl-1")
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events, statusErr: errors.New("status failed token=raw-secret password=p@ss")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete")
	req.Header.Set("X-Request-Id", "req-delete")
	rec := httptest.NewRecorder()
	logs := captureStandardLog(t)
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "status failed")
	assert.NotContains(t, rec.Body.String(), "raw-secret")
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())

	logOutput := logs.String()
	for _, token := range []string{"AFSCP workload mount released status failed", "workspace=ws-1", "project=proj-1", "workload=wl-1", "pod=" + podName, "mount_binding_id=wmb_demo", "request_id=req-delete", "correlation_id=corr-delete", "[REDACTED]"} {
		assert.Contains(t, logOutput, token)
	}
	assert.NotContains(t, logOutput, "raw-secret")
	assert.NotContains(t, logOutput, "p@ss")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Nil(t, reg.pods[podName], "released status must only be attempted after the pod is gone")
}

func TestDeleteWorkload_StatusFailureThenRetryContinuesTerminalMarkFromDurableFact(t *testing.T) {
	events := &eventRecorder{}
	facts := workloadfacts.NewMemoryStore()
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events, statusErr: errors.New("status failed")}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		AFSCPClient:       lifecycle,
		WorkloadFactStore: facts,
	})

	firstReq := httptest.NewRequest(http.MethodDelete, "/", nil)
	firstReq.Header.Set("X-Correlation-Id", "corr-delete")
	firstRec := httptest.NewRecorder()
	h.handleDeletePod(firstRec, firstReq, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, firstRec.Code)
	firstFact, err := facts.Get(context.Background(), workloadfacts.Key{WorkspaceID: "ws-1", ProjectID: "proj-1", WorkloadID: "wl-1"})
	require.NoError(t, err)
	assert.True(t, firstFact.ReleaseDone)
	assert.True(t, firstFact.PodDeleted)
	assert.False(t, firstFact.TerminalStatusDone)
	assert.Equal(t, []string{"release", "delete-pod", "confirm-pod-gone", "status-released"}, events.snapshot())

	lifecycle.statusErr = nil
	events.mu.Lock()
	events.events = nil
	events.mu.Unlock()

	secondReq := httptest.NewRequest(http.MethodDelete, "/", nil)
	secondReq.Header.Set("X-Correlation-Id", "corr-delete")
	secondRec := httptest.NewRecorder()
	h.handleDeletePod(secondRec, secondReq, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, secondRec.Code, secondRec.Body.String())
	secondFact, err := facts.Get(context.Background(), workloadfacts.Key{WorkspaceID: "ws-1", ProjectID: "proj-1", WorkloadID: "wl-1"})
	require.NoError(t, err)
	assert.True(t, secondFact.Terminal())
	assert.Equal(t, []string{"status-released"}, events.snapshot(), "retry must resume from durable facts instead of re-releasing or re-deleting")
}

func TestDeleteWorkload_PodNotFoundWithoutTerminalFactFailsClosed(t *testing.T) {
	facts := workloadfacts.NewMemoryStore()
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(), Options{WorkloadFactStore: facts})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusConflict, rec.Code)
	body := decodeError(t, rec)
	assert.Equal(t, "workload_release_incomplete", body.Error.Code)
	assert.Contains(t, body.Error.Message, "workload terminal fact")
}

func TestDeleteWorkload_AllTerminalFactsSecondDeleteIsIdempotent(t *testing.T) {
	events := &eventRecorder{}
	facts := workloadfacts.NewMemoryStore()
	require.NoError(t, facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws-1",
		ProjectID:          "proj-1",
		WorkloadID:         "wl-1",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            defaultScopedPodName("wl-1"),
		PodUID:             "uid-1",
		ReleaseDone:        true,
		PodDeleted:         true,
		TerminalStatusDone: true,
		WorkspaceBindingID: "wmb_demo",
	}))
	reg := newPodRegistry()
	reg.events = events
	lifecycle := &fakeMountLifecycleClient{events: events}
	h := newHandlerWithRegistryAndOptions(t, reg, Options{
		AFSCPClient:       lifecycle,
		WorkloadFactStore: facts,
	})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"confirm-pod-gone"}, events.snapshot(), "terminal fact retry must only prove scoped pod absence")
}

func TestDeleteWorkload_TerminalFactStillFailsClosedOnScopedPodMetadataDrift(t *testing.T) {
	const workloadID = "scope-drift-delete"
	requestedName := PodName("ws-b", "proj-b", workloadID)
	facts := workloadfacts.NewMemoryStore()
	require.NoError(t, facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws-b",
		ProjectID:          "proj-b",
		WorkloadID:         workloadID,
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            requestedName,
		PodUID:             "uid-terminal",
		ReleaseDone:        true,
		PodDeleted:         true,
		TerminalStatusDone: true,
		WorkspaceBindingID: "wmb_demo",
	}))
	driftedPod := workloadPodWithScope(requestedName, "ws-a", "proj-a", workloadID)
	reg := newPodRegistry(driftedPod)
	h := newHandlerWithRegistryAndOptions(t, reg, Options{WorkloadFactStore: facts})

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-b", "proj-b", workloadID)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	body := decodeError(t, rec)
	assert.Equal(t, "conflict", body.Error.Code)
	assert.Contains(t, body.Error.Message, "scope")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.NotNil(t, reg.pods[requestedName], "scope-drifted pod must not be deleted when terminal fact exists")
}

func TestHandleDeletePod_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1"))
	reg.setForceGetErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleDeletePod_DeletePodFails_Returns500(t *testing.T) {
	reg := newPodRegistry(workloadPodWithScope(defaultScopedPodName("wl-1"), "ws-1", "proj-1", "wl-1"))
	reg.setForceDeleteErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	h.handleDeletePod(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "pod deletion failed")
}

func TestHandleDeletePod_DeletePodFailsDoesNotPatchReleasedAndKeepsPodForRetry(t *testing.T) {
	events := &eventRecorder{}
	podName := defaultScopedPodName("wl-1")
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.events = events
	reg.setForceDeleteErrorFor(podName)
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
	assert.NotNil(t, reg.pods[podName], "failed pod deletion must leave pod available for retry")
}

// ---------------------------------------------------------------------------
// handleKeepalive – success / custom timeout / cap logic
// ---------------------------------------------------------------------------

func TestHandleKeepalive_GetPodReturnsInternalError_Returns500(t *testing.T) {
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.setForceGetErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleKeepalive_PatchFails_Returns500(t *testing.T) {
	now := time.Now().UTC()
	pod := defaultScopedPod("wl-1")
	pod.Annotations["workload/maxExpiresAt"] = now.Add(24 * time.Hour).Format(time.RFC3339)
	reg := newPodRegistry(pod)
	reg.setForcePatchErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "keepalive")
}

func TestHandleKeepalive_ReturnsExpiresAt(t *testing.T) {
	now := time.Now().UTC()
	pod := defaultScopedPod("wl-1")
	pod.Annotations["workload/maxExpiresAt"] = now.Add(24 * time.Hour).Format(time.RFC3339)
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

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
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(defaultScopedPod("wl-1")), Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-heartbeat")
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ns_demo", lifecycle.heartbeatNamespaceID)
	assert.Equal(t, "wmb_demo", lifecycle.heartbeatMountBindingID)
	assert.Equal(t, "corr-heartbeat", lifecycle.heartbeatCorrelationID)
	assert.Contains(t, lifecycle.heartbeatIdempotencyKey, "heartbeat")
}

func TestHandleKeepaliveUsesRequestIDContextForAFSCPCorrelationAndIdempotency(t *testing.T) {
	lifecycle := &fakeMountLifecycleClient{}
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(defaultScopedPod("wl-1")), Options{AFSCPClient: lifecycle})
	wrapped := observability.RequestIDMiddleware("X-ASBCP-Request-ID")(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws-1/projects/proj-1/workloads/wl-1/keepalive", nil)
	req.Header.Set("X-ASBCP-Request-ID", "custom-request-id")
	req.Header.Set("X-Correlation-Id", "stale-correlation-id")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "custom-request-id", lifecycle.heartbeatCorrelationID)
	assert.Contains(t, lifecycle.heartbeatIdempotencyKey, "custom-request-id")
	assert.NotContains(t, lifecycle.heartbeatIdempotencyKey, "sandbox")
}

func TestHandleKeepalive_AFSCPHeartbeatFailureLogsRedactedEvidence(t *testing.T) {
	lifecycle := &fakeMountLifecycleClient{heartbeatErr: errors.New("heartbeat failed token=raw-secret password=p@ss")}
	h := newHandlerWithRegistryAndOptions(t, newPodRegistry(defaultScopedPod("wl-1")), Options{AFSCPClient: lifecycle})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Correlation-Id", "corr-heartbeat")
	req.Header.Set("X-Request-Id", "req-heartbeat")
	rec := httptest.NewRecorder()
	logs := captureStandardLog(t)
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "AFSCP workload mount heartbeat failed")
	assert.NotContains(t, rec.Body.String(), "raw-secret")

	logOutput := logs.String()
	for _, token := range []string{"AFSCP workload mount heartbeat failed", "workload=wl-1", "pod=" + defaultScopedPodName("wl-1"), "mount_binding_id=wmb_demo", "request_id=req-heartbeat", "correlation_id=corr-heartbeat", "[REDACTED]"} {
		assert.Contains(t, logOutput, token)
	}
	assert.NotContains(t, logOutput, "raw-secret")
	assert.NotContains(t, logOutput, "p@ss")
}

func TestHandleKeepalive_UsesCustomIdleTimeoutFromAnnotation(t *testing.T) {
	const customIdleSec = 600
	now := time.Now().UTC()
	pod := defaultScopedPod("wl-1")
	pod.Annotations["workload/idleTimeoutSec"] = strconv.Itoa(customIdleSec)
	pod.Annotations["workload/maxExpiresAt"] = now.Add(24 * time.Hour).Format(time.RFC3339)
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

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
	pod := defaultScopedPod("wl-1")
	pod.Annotations["workload/maxExpiresAt"] = maxExpires.Format(time.RFC3339)
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

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
	pod := defaultScopedPod("wl-1")
	pod.Annotations["workload/maxExpiresAt"] = maxExpires.Format(time.RFC3339)
	h := newHandlerWithRegistry(t, newPodRegistry(pod))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.handleKeepalive(rec, req, "ws-1", "proj-1", "wl-1")

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
	reg := newPodRegistry(defaultScopedPod("wl-1"))
	reg.setForceGetErrorFor(defaultScopedPodName("wl-1"))
	h := newHandlerWithRegistry(t, reg)

	payload, _ := json.Marshal(ExecRequest{Cmd: []string{"echo", "hi"}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "ws-1", "proj-1", "wl-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleExec_PodExistsButExecFails(t *testing.T) {
	h := newHandlerWithRegistry(t, newPodRegistry(defaultScopedPod("wl-1")))

	payload, _ := json.Marshal(ExecRequest{Cmd: []string{"echo", "hello"}})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.handleExec(rec, req, "ws-1", "proj-1", "wl-1")

	// The pod exists so the handler must NOT return 404 (pod not found).
	// It will return either 200 (exec result returned even on SPDY error) or 500.
	// The key invariant: a valid pod does not produce a "pod not found" response.
	assert.NotEqual(t, http.StatusNotFound, rec.Code, "pod exists – 404 must not be returned")
}

// ---------------------------------------------------------------------------
// Full HTTP routing via RegisterRoutes
// ---------------------------------------------------------------------------

func TestServeHTTP_GetWorkload_RunningPod(t *testing.T) {
	pod := workloadPodWithScope(PodName("ws-1", "proj-1", "my-agent"), "ws-1", "proj-1", "my-agent")
	pod.CreationTimestamp = metav1.Now()
	pod.Status = v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.10.10.10"}
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
	assert.Equal(t, PodName("ws-1", "proj-1", "my-agent"), got.PodName)
	assert.Equal(t, "Running", got.Phase)
	assert.Equal(t, "10.10.10.10", got.IP)
}

func TestServeHTTP_DeleteWorkload_Success(t *testing.T) {
	pod := workloadPodWithScope(PodName("ws-1", "proj-1", "agent-del"), "ws-1", "proj-1", "agent-del")
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
	pod := workloadPodWithScope(PodName("ws-1", "proj-1", "agent-ka"), "ws-1", "proj-1", "agent-ka")
	pod.Annotations["workload/maxExpiresAt"] = now.Add(24 * time.Hour).Format(time.RFC3339)
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
